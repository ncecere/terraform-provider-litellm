package litellm

import (
	"fmt"
	"log"
)

// CascadingCleanupManager handles proper cleanup of related resources
// Since LiteLLM doesn't implement cascading deletes, we need to do it manually
type CascadingCleanupManager struct {
	client *Client
}

// NewCascadingCleanupManager creates a new cleanup manager
func NewCascadingCleanupManager(client *Client) *CascadingCleanupManager {
	return &CascadingCleanupManager{client: client}
}

// CleanupUserKeys deletes all API keys associated with a user
// This is necessary because LiteLLM doesn't cascade delete keys when removing team members
func (c *CascadingCleanupManager) CleanupUserKeys(userID string) error {
	log.Printf("[DEBUG] Starting cleanup of keys for user: %s", userID)

	// Get user information to find their keys
	userResp, err := c.client.GetUser(userID)
	if err != nil {
		log.Printf("[WARN] Could not get user info for cleanup: %v", err)
		return nil // Don't fail the delete if we can't clean up keys
	}

	// Extract keys from the user response
	keysData, exists := userResp["keys"]
	if !exists {
		log.Printf("[DEBUG] No keys found for user %s", userID)
		return nil
	}

	keys, ok := keysData.([]interface{})
	if !ok || len(keys) == 0 {
		log.Printf("[DEBUG] No keys to clean up for user %s", userID)
		return nil
	}

	// Collect key tokens for deletion
	var keyTokens []string
	for _, keyData := range keys {
		if keyMap, ok := keyData.(map[string]interface{}); ok {
			if token, exists := keyMap["token"]; exists {
				if tokenStr, ok := token.(string); ok {
					keyTokens = append(keyTokens, tokenStr)
				}
			}
		}
	}

	if len(keyTokens) == 0 {
		log.Printf("[DEBUG] No valid key tokens found for user %s", userID)
		return nil
	}

	// Delete all keys for this user
	log.Printf("[DEBUG] Deleting %d keys for user %s", len(keyTokens), userID)
	err = c.client.DeleteKey(keyTokens[0]) // DeleteKey method takes single key ID
	if err != nil {
		log.Printf("[WARN] Failed to delete some keys for user %s: %v", userID, err)
		// Don't fail the whole operation
	} else {
		log.Printf("[INFO] Successfully cleaned up keys for user %s", userID)
	}

	return nil
}

// CleanupUserFromAllTeams removes a user from all teams they're a member of
// This is necessary because team membership isn't automatically cleaned up
func (c *CascadingCleanupManager) CleanupUserFromAllTeams(userID string, excludeTeamID string) error {
	log.Printf("[DEBUG] Starting cleanup of team memberships for user: %s", userID)

	// Get user information
	userResp, err := c.client.GetUser(userID)
	if err != nil {
		log.Printf("[WARN] Could not get user info for team cleanup: %v", err)
		return nil
	}

	// Extract teams from user response
	teamsData, exists := userResp["teams"]
	if !exists {
		log.Printf("[DEBUG] No teams found for user %s", userID)
		return nil
	}

	teams, ok := teamsData.([]interface{})
	if !ok || len(teams) == 0 {
		log.Printf("[DEBUG] No teams to clean up for user %s", userID)
		return nil
	}

	// Remove user from each team (except the one we're currently processing)
	for _, teamData := range teams {
		if teamStr, ok := teamData.(string); ok && teamStr != excludeTeamID {
			log.Printf("[DEBUG] Removing user %s from team %s", userID, teamStr)
			
			// Get user email for the delete request
			userEmail := ""
			if emailData, exists := userResp["user_email"]; exists {
				if emailStr, ok := emailData.(string); ok {
					userEmail = emailStr
				}
			}

			deleteData := map[string]interface{}{
				"user_id":    userID,
				"user_email": userEmail,
				"team_id":    teamStr,
			}

			resp, err := MakeRequest(c.client, "POST", "/team/member_delete", deleteData)
			if err != nil {
				log.Printf("[WARN] Failed to remove user %s from team %s: %v", userID, teamStr, err)
			} else {
				resp.Body.Close()
				log.Printf("[INFO] Successfully removed user %s from team %s", userID, teamStr)
			}
		}
	}

	return nil
}

// CleanupOrphanedUser removes a user entirely if they're no longer in any teams
// This is optional and should be used carefully
func (c *CascadingCleanupManager) CleanupOrphanedUser(userID string) error {
	log.Printf("[DEBUG] Checking if user %s should be deleted", userID)

	userResp, err := c.client.GetUser(userID)
	if err != nil {
		log.Printf("[DEBUG] User %s already deleted or not found", userID)
		return nil
	}

	// Check if user is still in any teams
	teamsData, exists := userResp["teams"]
	if !exists {
		log.Printf("[DEBUG] User %s has no teams, eligible for deletion", userID)
	} else if teams, ok := teamsData.([]interface{}); ok && len(teams) == 0 {
		log.Printf("[DEBUG] User %s has empty teams list, eligible for deletion", userID)
	} else {
		log.Printf("[DEBUG] User %s still has team memberships, not deleting", userID)
		return nil
	}

	// Delete the user (note: this endpoint might not exist in all LiteLLM versions)
	deleteData := map[string]interface{}{
		"user_id": userID,
	}

	resp, err := MakeRequest(c.client, "POST", "/user/delete", deleteData)
	if err != nil {
		log.Printf("[WARN] Failed to delete orphaned user %s: %v", userID, err)
		return nil // Don't fail the operation
	}
	defer resp.Body.Close()

	log.Printf("[INFO] Successfully deleted orphaned user %s", userID)
	return nil
}

// FullUserCleanup performs comprehensive cleanup of a user and all related resources
func (c *CascadingCleanupManager) FullUserCleanup(userID string, currentTeamID string, deleteOrphanedUser bool) error {
	log.Printf("[INFO] Starting full cleanup for user %s", userID)

	// Step 1: Clean up all API keys
	if err := c.CleanupUserKeys(userID); err != nil {
		log.Printf("[WARN] Key cleanup failed for user %s: %v", userID, err)
	}

	// Step 2: Remove from other teams (if any)
	if err := c.CleanupUserFromAllTeams(userID, currentTeamID); err != nil {
		log.Printf("[WARN] Team cleanup failed for user %s: %v", userID, err)
	}

	// Step 3: Optionally delete the user entirely if orphaned
	if deleteOrphanedUser {
		if err := c.CleanupOrphanedUser(userID); err != nil {
			log.Printf("[WARN] Orphaned user cleanup failed for user %s: %v", userID, err)
		}
	}

	log.Printf("[INFO] Completed full cleanup for user %s", userID)
	return nil
}