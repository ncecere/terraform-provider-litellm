package litellm

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
)

type Client struct {
	APIBase    string
	APIKey     string
	httpClient *http.Client
}

func NewClient(apiBase, apiKey string) *Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	return &Client{
		APIBase:    apiBase,
		APIKey:     apiKey,
		httpClient: &http.Client{Transport: tr},
	}
}

// Team-related methods
func (c *Client) CreateTeam(team map[string]interface{}) (map[string]interface{}, error) {
	return c.sendRequest("POST", "/team/new", team)
}

func (c *Client) GetTeam(teamID string) (map[string]interface{}, error) {
	return c.sendRequest("GET", fmt.Sprintf("/team/info?team_id=%s", teamID), nil)
}

func (c *Client) UpdateTeam(team map[string]interface{}) (map[string]interface{}, error) {
	return c.sendRequest("POST", "/team/update", team)
}

func (c *Client) DeleteTeam(teamID string) error {
	payload := map[string]interface{}{
		"team_ids": []string{teamID},
	}
	_, err := c.sendRequest("POST", "/team/delete", payload)
	return err
}

// Key-related methods
func (c *Client) CreateKey(key *Key) (*Key, error) {
	resp, err := c.sendRequest("POST", "/key/generate", key)
	if err != nil {
		return nil, err
	}

	return c.parseKeyResponse(resp)
}

// CreateServiceAccountKey creates a key owned by a team service account.
func (c *Client) CreateServiceAccountKey(key *Key) (*Key, error) {
	resp, err := c.sendRequest("POST", "/key/service-account/generate", key)
	if err != nil {
		return nil, err
	}

	return c.parseKeyResponse(resp)
}

func (c *Client) GetKey(keyID string) (*Key, error) {
	resp, err := c.sendRequest("GET", fmt.Sprintf("/key/info?key=%s", keyID), nil)
	if err != nil {
		return nil, err
	}

	return c.parseKeyResponse(resp)
}

func (c *Client) UpdateKey(key *Key) (*Key, error) {
	// Create a new map with only the fields that can be updated
	// Only include optional fields when they have non-zero values (matching omitempty behavior)
	updateData := map[string]interface{}{
		"key":     key.Key,
		"blocked": key.Blocked,
	}

	// Include arrays/slices only when non-empty
	if len(key.Models) > 0 {
		updateData["models"] = key.Models
	}
	if len(key.AllowedRoutes) > 0 {
		updateData["allowed_routes"] = key.AllowedRoutes
	}
	if len(key.AllowedPassthroughRoutes) > 0 {
		updateData["allowed_passthrough_routes"] = key.AllowedPassthroughRoutes
	}
	if len(key.Guardrails) > 0 {
		updateData["guardrails"] = key.Guardrails
	}

	// Include numeric fields only when non-zero
	if key.MaxBudget != 0 {
		updateData["max_budget"] = key.MaxBudget
	}
	if key.MaxParallelRequests != 0 {
		updateData["max_parallel_requests"] = key.MaxParallelRequests
	}
	if key.TPMLimit != 0 {
		updateData["tpm_limit"] = key.TPMLimit
	}
	if key.RPMLimit != 0 {
		updateData["rpm_limit"] = key.RPMLimit
	}

	// Include string fields only when non-empty
	if key.TeamID != "" {
		updateData["team_id"] = key.TeamID
	}
	if key.BudgetDuration != "" {
		updateData["budget_duration"] = key.BudgetDuration
	}
	if key.KeyAlias != "" {
		updateData["key_alias"] = key.KeyAlias
	}

	// Include maps only when non-nil
	if key.Metadata != nil {
		updateData["metadata"] = key.Metadata
	}
	if key.Aliases != nil {
		updateData["aliases"] = key.Aliases
	}
	if key.Permissions != nil {
		updateData["permissions"] = key.Permissions
	}
	if key.ModelMaxBudget != nil {
		updateData["model_max_budget"] = key.ModelMaxBudget
	}
	if key.ModelRPMLimit != nil {
		updateData["model_rpm_limit"] = key.ModelRPMLimit
	}
	if key.ModelTPMLimit != nil {
		updateData["model_tpm_limit"] = key.ModelTPMLimit
	}

	resp, err := c.sendRequest("POST", "/key/update", updateData)
	if err != nil {
		return nil, err
	}

	return c.parseKeyResponse(resp)
}

func (c *Client) DeleteKey(keyID string) error {
	payload := map[string]interface{}{
		"keys": []string{keyID},
	}
	_, err := c.sendRequest("POST", "/key/delete", payload)
	return err
}

func (c *Client) parseKeyResponse(resp map[string]interface{}) (*Key, error) {
	if resp == nil {
		return nil, fmt.Errorf("received nil response")
	}

	createdKey := &Key{}

	for k, v := range resp {
		if v == nil {
			continue
		}

		switch k {
		case "key":
			if s, ok := v.(string); ok {
				createdKey.Key = s
			}
		case "models":
			if models, ok := v.([]interface{}); ok {
				createdKey.Models = make([]string, len(models))
				for i, model := range models {
					if s, ok := model.(string); ok {
						createdKey.Models[i] = s
					}
				}
			}
		case "allowed_routes":
			if routes, ok := v.([]interface{}); ok {
				createdKey.AllowedRoutes = make([]string, len(routes))
				for i, route := range routes {
					if s, ok := route.(string); ok {
						createdKey.AllowedRoutes[i] = s
					}
				}
			}
		case "allowed_passthrough_routes":
			if routes, ok := v.([]interface{}); ok {
				createdKey.AllowedPassthroughRoutes = make([]string, len(routes))
				for i, route := range routes {
					if s, ok := route.(string); ok {
						createdKey.AllowedPassthroughRoutes[i] = s
					}
				}
			}
		case "spend":
			if f, ok := v.(float64); ok {
				createdKey.Spend = f
			}
		case "max_budget":
			if f, ok := v.(float64); ok {
				createdKey.MaxBudget = f
			}
		case "user_id":
			if s, ok := v.(string); ok {
				createdKey.UserID = s
			}
		case "team_id":
			if s, ok := v.(string); ok {
				createdKey.TeamID = s
			}
		case "max_parallel_requests":
			if i, ok := v.(float64); ok {
				createdKey.MaxParallelRequests = int(i)
			}
		case "metadata":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.Metadata = m
			}
		case "tpm_limit":
			if i, ok := v.(float64); ok {
				createdKey.TPMLimit = int(i)
			}
		case "rpm_limit":
			if i, ok := v.(float64); ok {
				createdKey.RPMLimit = int(i)
			}
		case "budget_duration":
			if s, ok := v.(string); ok {
				createdKey.BudgetDuration = s
			}
		case "soft_budget":
			if f, ok := v.(float64); ok {
				createdKey.SoftBudget = f
			}
		case "key_alias":
			if s, ok := v.(string); ok {
				createdKey.KeyAlias = s
			}
		case "duration":
			if s, ok := v.(string); ok {
				createdKey.Duration = s
			}
		case "aliases":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.Aliases = m
			}
		case "config":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.Config = m
			}
		case "permissions":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.Permissions = m
			}
		case "model_max_budget":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.ModelMaxBudget = m
			}
		case "model_rpm_limit":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.ModelRPMLimit = m
			}
		case "model_tpm_limit":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.ModelTPMLimit = m
			}
		case "guardrails":
			if guardrails, ok := v.([]interface{}); ok {
				createdKey.Guardrails = make([]string, len(guardrails))
				for i, guardrail := range guardrails {
					if s, ok := guardrail.(string); ok {
						createdKey.Guardrails[i] = s
					}
				}
			}
		case "blocked":
			if b, ok := v.(bool); ok {
				createdKey.Blocked = b
			}
		case "tags":
			if tags, ok := v.([]interface{}); ok {
				createdKey.Tags = make([]string, len(tags))
				for i, tag := range tags {
					if s, ok := tag.(string); ok {
						createdKey.Tags[i] = s
					}
				}
			}
		}
	}

	return createdKey, nil
}

func (c *Client) sendRequest(method, path string, body interface{}) (map[string]interface{}, error) {
	url := c.APIBase + path

	var req *http.Request
	var err error

	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("error marshaling request body: %v", err)
		}
		log.Printf("Making %s request to %s with body:\n%s", method, url, string(jsonBody))
		req, err = http.NewRequest(method, url, bytes.NewBuffer(jsonBody))
	} else {
		log.Printf("Making %s request to %s", method, url)
		req, err = http.NewRequest(method, url, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	log.Printf("Response status: %d", resp.StatusCode)
	log.Printf("Response body: %s", string(bodyBytes))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		if method == "POST" && (len(bodyBytes) == 0 || string(bodyBytes) == "null") {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("error parsing response JSON: %v\nResponse body: %s", err, string(bodyBytes))
	}

	return result, nil
}
