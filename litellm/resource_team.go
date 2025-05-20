package litellm

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

const (
	endpointTeamNew    = "/team/new"
	endpointTeamInfo   = "/team/info"
	endpointTeamUpdate = "/team/update"
	endpointTeamDelete = "/team/delete"
)

func ResourceLiteLLMTeam() *schema.Resource {
	return &schema.Resource{
		Create: resourceLiteLLMTeamCreate,
		Read:   resourceLiteLLMTeamRead,
		Update: resourceLiteLLMTeamUpdate,
		Delete: resourceLiteLLMTeamDelete,

		Schema: map[string]*schema.Schema{
			"team_alias": {
				Type:     schema.TypeString,
				Required: true,
			},
			"organization_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"metadata": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"tpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"rpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"max_budget": {
				Type:     schema.TypeFloat,
				Optional: true,
			},
			"budget_duration": {
				Type:     schema.TypeString,
				Optional: true,
				ValidateFunc: validation.StringMatch(
					regexp.MustCompile(`^(\d+[smhd])$`),
					"budget_duration must be a duration string like '30s', '30m', '30h', or '30d'",
				),
			},
			"models": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"blocked": {
				Type:     schema.TypeBool,
				Optional: true,
			},
		},
	}
}

func resourceLiteLLMTeamCreate(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	teamID := uuid.New().String()
	teamData := buildTeamData(d, teamID)

	log.Printf("[DEBUG] Create team request payload: %+v", teamData)

	resp, err := MakeRequest(client, "POST", endpointTeamNew, teamData)
	if err != nil {
		return fmt.Errorf("error creating team: %w", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "creating team"); err != nil {
		return err
	}

	d.SetId(teamID)
	log.Printf("[INFO] Team created with ID: %s", teamID)

	return resourceLiteLLMTeamRead(d, m)
}

func resourceLiteLLMTeamRead(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	log.Printf("[INFO] Reading team with ID: %s", d.Id())

	resp, err := MakeRequest(client, "GET", fmt.Sprintf("%s?team_id=%s", endpointTeamInfo, d.Id()), nil)
	if err != nil {
		return fmt.Errorf("error reading team: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Printf("[WARN] Team with ID %s not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	var teamResp TeamResponse
	if err := json.NewDecoder(resp.Body).Decode(&teamResp); err != nil {
		return fmt.Errorf("error decoding team info response: %w", err)
	}

	// Update the state with values from the response or fall back to the data passed in during creation
	d.Set("team_alias", GetStringValue(teamResp.TeamAlias, d.Get("team_alias").(string)))
	d.Set("organization_id", GetStringValue(teamResp.OrganizationID, d.Get("organization_id").(string)))

	// Handle metadata separately as it's a map
	if teamResp.Metadata != nil {
		d.Set("metadata", teamResp.Metadata)
	} else {
		d.Set("metadata", d.Get("metadata"))
	}

	d.Set("tpm_limit", GetIntValue(teamResp.TPMLimit, d.Get("tpm_limit").(int)))
	d.Set("rpm_limit", GetIntValue(teamResp.RPMLimit, d.Get("rpm_limit").(int)))
	d.Set("max_budget", GetFloatValue(teamResp.MaxBudget, d.Get("max_budget").(float64)))
	d.Set("budget_duration", GetStringValue(teamResp.BudgetDuration, d.Get("budget_duration").(string)))

	// Handle models separately as it's a list
	if teamResp.Models != nil {
		d.Set("models", teamResp.Models)
	} else {
		d.Set("models", d.Get("models"))
	}

	d.Set("blocked", GetBoolValue(teamResp.Blocked, d.Get("blocked").(bool)))

	log.Printf("[INFO] Successfully read team with ID: %s", d.Id())
	return nil
}

func resourceLiteLLMTeamUpdate(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	teamData := buildTeamData(d, d.Id())
	log.Printf("[DEBUG] Update team request payload: %+v", teamData)

	resp, err := MakeRequest(client, "POST", endpointTeamUpdate, teamData)
	if err != nil {
		return fmt.Errorf("error updating team: %w", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "updating team"); err != nil {
		return err
	}

	log.Printf("[INFO] Successfully updated team with ID: %s", d.Id())
	return resourceLiteLLMTeamRead(d, m)
}

func resourceLiteLLMTeamDelete(d *schema.ResourceData, m interface{}) error {
	client := m.(*Client)

	log.Printf("[INFO] Deleting team with ID: %s", d.Id())

	deleteData := map[string]interface{}{
		"team_ids": []string{d.Id()},
	}

	resp, err := MakeRequest(client, "POST", endpointTeamDelete, deleteData)
	if err != nil {
		return fmt.Errorf("error deleting team: %w", err)
	}
	defer resp.Body.Close()

	if err := handleResponse(resp, "deleting team"); err != nil {
		return err
	}

	log.Printf("[INFO] Successfully deleted team with ID: %s", d.Id())
	d.SetId("")
	return nil
}

func buildTeamData(d *schema.ResourceData, teamID string) map[string]interface{} {
	teamData := map[string]interface{}{
		"team_id":    teamID,
		"team_alias": d.Get("team_alias").(string),
	}

	for _, key := range []string{"organization_id", "metadata", "tpm_limit", "rpm_limit", "max_budget", "budget_duration", "models", "blocked"} {
		if v, ok := d.GetOk(key); ok {
			teamData[key] = v
		}
	}

	return teamData
}

func handleResponse(resp *http.Response, action string) error {
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("error %s: %s - %s", action, resp.Status, string(body))
	}
	return nil
}
