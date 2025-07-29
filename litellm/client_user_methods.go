package litellm

import (
	"fmt"
)

// User-related methods for managing LiteLLM users
// These methods handle the user-level operations that team membership operations don't cover

// UpdateUser updates user information including email and budget
func (c *Client) UpdateUser(userID string, userEmail string, maxBudget float64, budgetDuration string, userRole string) (map[string]interface{}, error) {
	userData := map[string]interface{}{
		"user_id":         userID,
		"user_email":      userEmail,
		"max_budget":      maxBudget,
		"budget_duration": budgetDuration,
		"user_role":       userRole,
	}

	return c.sendRequest("POST", "/user/update", userData)
}

// GetUser retrieves user information by user_id
func (c *Client) GetUser(userID string) (map[string]interface{}, error) {
	return c.sendRequest("GET", fmt.Sprintf("/user/info?user_id=%s", userID), nil)
}

// CreateUser creates a new user (if needed)
func (c *Client) CreateUser(userID string, userEmail string, maxBudget float64, budgetDuration string, userRole string) (map[string]interface{}, error) {
	userData := map[string]interface{}{
		"user_id":         userID,
		"user_email":      userEmail,
		"max_budget":      maxBudget,
		"budget_duration": budgetDuration,
		"user_role":       userRole,
	}

	return c.sendRequest("POST", "/user/new", userData)
}

// GetTeamMembers retrieves all members for a specific team
func (c *Client) GetTeamMembers(teamID string) (map[string]interface{}, error) {
	return c.sendRequest("GET", fmt.Sprintf("/team/info?team_id=%s", teamID), nil)
}