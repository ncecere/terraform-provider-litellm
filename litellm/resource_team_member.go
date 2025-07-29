package litellm

import (
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceLiteLLMTeamMember() *schema.Resource {
	return &schema.Resource{
		Create: resourceLiteLLMTeamMemberCreate,
		Read:   resourceLiteLLMTeamMemberRead,
		Update: resourceLiteLLMTeamMemberUpdate,
		Delete: resourceLiteLLMTeamMemberDelete,

		Schema: map[string]*schema.Schema{
			"team_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the team to add the member to",
			},
			"user_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "User ID for the team member",
			},
			"user_email": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Email address of the user",
			},
			"role": {
				Type:     schema.TypeString,
				Required: true,
				ValidateFunc: validation.StringInSlice([]string{
					"org_admin",
					"internal_user",
					"internal_user_viewer",
					"admin",
					"user",
				}, false),
				Description: "Role of the user in the team",
			},
			"max_budget_in_team": {
				Type:        schema.TypeFloat,
				Optional:    true,
				Description: "Maximum budget for this user within the team",
			},
			// Enhanced features with backward-compatible defaults
			"user_max_budget": {
				Type:        schema.TypeFloat,
				Optional:    true,
				Description: "Maximum budget for the user (user-level setting)",
			},
			"budget_duration": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "1mo",
				Description: "Budget duration (e.g., '1mo', '1d')",
			},
			"update_user_record": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false, // Preserves backward compatibility
				Description: "Whether to update the user record with email and budget information",
			},
			"cascade_delete_keys": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false, // Preserves backward compatibility
				Description: "Whether to delete user's API keys when removing from team",
			},
			"cleanup_orphaned_user": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false, // Preserves backward compatibility
				Description: "Whether to delete the user entirely if they have no team memberships left",
			},
		},
	}
}

func resourceLiteLLMTeamMemberCreate(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	teamID := d.Get("team_id").(string)
	userID := d.Get("user_id").(string)
	userEmail := d.Get("user_email").(string)
	role := d.Get("role").(string)
	maxBudgetInTeam := d.Get("max_budget_in_team").(float64)
	updateUserRecord := d.Get("update_user_record").(bool)

	// Step 1: Add member to team
	memberData := map[string]interface{}{
		"member": []map[string]interface{}{
			{
				"role":       role,
				"user_id":    userID,
				"user_email": userEmail,
			},
		},
		"team_id":            teamID,
		"max_budget_in_team": maxBudgetInTeam,
	}

	log.Printf("[DEBUG] Create team member request payload: %+v", memberData)

	resp, err := MakeRequest(client, "POST", "/team/member_add", memberData)
	if err != nil {
		return fmt.Errorf("error creating team member: %v", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "creating team member"); err != nil {
		return err
	}

	// Step 2: Update user record if requested
	if updateUserRecord {
		log.Printf("[DEBUG] Updating user record for user_id: %s", userID)
		
		// Use user_max_budget if provided, otherwise fall back to max_budget_in_team
		userMaxBudget := d.Get("user_max_budget").(float64)
		budgetToSet := userMaxBudget
		if budgetToSet == 0 {
			budgetToSet = maxBudgetInTeam
		}

		budgetDuration := d.Get("budget_duration").(string)

		// Map role to user role (handle the internal_user mapping)
		userRole := role
		if role == "user" {
			userRole = "internal_user"
		}

		_, err := client.UpdateUser(userID, userEmail, budgetToSet, budgetDuration, userRole)
		if err != nil {
			log.Printf("[WARN] Failed to update user record, attempting to create user: %v", err)
			
			// If update fails, try creating the user
			_, createErr := client.CreateUser(userID, userEmail, budgetToSet, budgetDuration, userRole)
			if createErr != nil {
				log.Printf("[WARN] Failed to create user record: %v", createErr)
				// Don't fail the whole operation if user update fails
				// This maintains backward compatibility
			} else {
				log.Printf("[INFO] Successfully created user record for %s", userID)
			}
		} else {
			log.Printf("[INFO] Successfully updated user record for %s", userID)
		}
	}

	// Set a composite ID since there's no specific member ID returned
	d.SetId(fmt.Sprintf("%s:%s", teamID, userID))

	log.Printf("[INFO] Team member created with ID: %s", d.Id())

	return resourceLiteLLMTeamMemberRead(d, m)
}

func resourceLiteLLMTeamMemberRead(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	// Parse the composite ID
	idParts := strings.Split(d.Id(), ":")
	if len(idParts) != 2 {
		return fmt.Errorf("invalid ID format, expected team_id:user_id, got: %s", d.Id())
	}

	teamID := idParts[0]
	userID := idParts[1]

	log.Printf("[DEBUG] Reading team member: team_id=%s, user_id=%s", teamID, userID)

	// Get team information to verify member still exists
	teamResp, err := client.GetTeam(teamID)
	if err != nil {
		log.Printf("[WARN] Failed to get team info: %v", err)
		d.SetId("")
		return nil
	}

	// Check if user is still a member of the team
	members, exists := teamResp["members"]
	if !exists {
		log.Printf("[WARN] No members field in team response")
		d.SetId("")
		return nil
	}

	memberFound := false
	if membersList, ok := members.([]interface{}); ok {
		for _, member := range membersList {
			if memberStr, ok := member.(string); ok && memberStr == userID {
				memberFound = true
				break
			}
		}
	}

	if !memberFound {
		log.Printf("[INFO] User %s is no longer a member of team %s", userID, teamID)
		d.SetId("")
		return nil
	}

	// Set the known values
	d.Set("team_id", teamID)
	d.Set("user_id", userID)

	// Optionally get user information if update_user_record is enabled
	if d.Get("update_user_record").(bool) {
		userResp, err := client.GetUser(userID)
		if err == nil {
			if email, exists := userResp["user_email"]; exists {
				d.Set("user_email", email)
			}
			if budget, exists := userResp["max_budget"]; exists {
				d.Set("user_max_budget", budget)
			}
			if duration, exists := userResp["budget_duration"]; exists {
				d.Set("budget_duration", duration)
			}
		}
	}

	return nil
}

func resourceLiteLLMTeamMemberUpdate(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	teamID := d.Get("team_id").(string)
	userID := d.Get("user_id").(string)
	userEmail := d.Get("user_email").(string)
	maxBudgetInTeam := d.Get("max_budget_in_team").(float64)
	role := d.Get("role").(string)
	updateUserRecord := d.Get("update_user_record").(bool)

	// Update team membership
	updateData := map[string]interface{}{
		"user_id":            userID,
		"user_email":         userEmail,
		"team_id":            teamID,
		"max_budget_in_team": maxBudgetInTeam,
		"role":               role,
	}

	log.Printf("[DEBUG] Update team member request payload: %+v", updateData)

	resp, err := MakeRequest(client, "POST", "/team/member_update", updateData)
	if err != nil {
		return fmt.Errorf("error updating team member: %v", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "updating team member"); err != nil {
		return err
	}

	// Update user record if requested and relevant fields changed
	if updateUserRecord && (d.HasChange("user_email") || d.HasChange("user_max_budget") || d.HasChange("budget_duration") || d.HasChange("role")) {
		log.Printf("[DEBUG] Updating user record for user_id: %s", userID)
		
		userMaxBudget := d.Get("user_max_budget").(float64)
		budgetToSet := userMaxBudget
		if budgetToSet == 0 {
			budgetToSet = maxBudgetInTeam
		}

		budgetDuration := d.Get("budget_duration").(string)

		userRole := role
		if role == "user" {
			userRole = "internal_user"
		}

		_, err := client.UpdateUser(userID, userEmail, budgetToSet, budgetDuration, userRole)
		if err != nil {
			log.Printf("[WARN] Failed to update user record: %v", err)
			// Don't fail the whole operation
		} else {
			log.Printf("[INFO] Successfully updated user record for %s", userID)
		}
	}

	log.Printf("[INFO] Successfully updated team member with ID: %s", d.Id())

	return resourceLiteLLMTeamMemberRead(d, m)
}

func resourceLiteLLMTeamMemberDelete(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	userID := d.Get("user_id").(string)
	userEmail := d.Get("user_email").(string)
	teamID := d.Get("team_id").(string)
	cascadeDeleteKeys := d.Get("cascade_delete_keys").(bool)
	cleanupOrphanedUser := d.Get("cleanup_orphaned_user").(bool)

	log.Printf("[DEBUG] Starting delete for team member %s from team %s", userID, teamID)

	// Step 1: Remove from team
	deleteData := map[string]interface{}{
		"user_id":    userID,
		"user_email": userEmail,
		"team_id":    teamID,
	}

	log.Printf("[DEBUG] Delete team member request payload: %+v", deleteData)

	resp, err := MakeRequest(client, "POST", "/team/member_delete", deleteData)
	if err != nil {
		return fmt.Errorf("error deleting team member: %v", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "deleting team member"); err != nil {
		return err
	}

	// Step 2: Perform cascading cleanup if requested
	if cascadeDeleteKeys || cleanupOrphanedUser {
		cleanup := NewCascadingCleanupManager(client)
		
		if cascadeDeleteKeys {
			log.Printf("[DEBUG] Performing cascading key cleanup for user %s", userID)
			if err := cleanup.CleanupUserKeys(userID); err != nil {
				log.Printf("[WARN] Cascading key cleanup failed: %v", err)
				// Don't fail the whole operation for cleanup issues
			}
		}

		if cleanupOrphanedUser {
			log.Printf("[DEBUG] Checking for orphaned user cleanup for user %s", userID)
			if err := cleanup.CleanupOrphanedUser(userID); err != nil {
				log.Printf("[WARN] Orphaned user cleanup failed: %v", err)
				// Don't fail the whole operation for cleanup issues
			}
		}
	}

	log.Printf("[INFO] Successfully deleted team member with ID: %s", d.Id())

	d.SetId("")
	return nil
}
