package litellm

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
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
	updateData := map[string]interface{}{
		"key":                        key.Key,
		"models":                     key.Models,
		"allowed_routes":             key.AllowedRoutes,
		"allowed_passthrough_routes": key.AllowedPassthroughRoutes,
		"max_budget":                 key.MaxBudget,
		"team_id":                    key.TeamID,
		"max_parallel_requests":      key.MaxParallelRequests,
		"metadata":                   key.Metadata,
		"tpm_limit":                  key.TPMLimit,
		"rpm_limit":                  key.RPMLimit,
		"budget_duration":            key.BudgetDuration,
		"key_alias":                  key.KeyAlias,
		"aliases":                    key.Aliases,
		"permissions":                key.Permissions,
		"model_max_budget":           key.ModelMaxBudget,
		"model_rpm_limit":            key.ModelRPMLimit,
		"model_tpm_limit":            key.ModelTPMLimit,
		"guardrails":                 key.Guardrails,
		"blocked":                    key.Blocked,
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
			value, err := legacyFloat64FromJSON(v)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field spend")
			}
			createdKey.Spend = value
		case "max_budget":
			value, err := legacyFloat64FromJSON(v)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field max_budget")
			}
			createdKey.MaxBudget = value
		case "user_id":
			if s, ok := v.(string); ok {
				createdKey.UserID = s
			}
		case "team_id":
			if s, ok := v.(string); ok {
				createdKey.TeamID = s
			}
		case "max_parallel_requests":
			value, err := legacyIntFromJSON(v)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field max_parallel_requests")
			}
			createdKey.MaxParallelRequests = value
		case "metadata":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.Metadata = legacyStringMapFromJSON(m)
			}
		case "tpm_limit":
			value, err := legacyIntFromJSON(v)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field tpm_limit")
			}
			createdKey.TPMLimit = value
		case "rpm_limit":
			value, err := legacyIntFromJSON(v)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field rpm_limit")
			}
			createdKey.RPMLimit = value
		case "budget_duration":
			if s, ok := v.(string); ok {
				createdKey.BudgetDuration = s
			}
		case "soft_budget":
			value, err := legacyFloat64FromJSON(v)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field soft_budget")
			}
			createdKey.SoftBudget = value
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
				createdKey.Aliases = legacyStringMapFromJSON(m)
			}
		case "config":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.Config = legacyStringMapFromJSON(m)
			}
		case "permissions":
			if m, ok := v.(map[string]interface{}); ok {
				createdKey.Permissions = legacyStringMapFromJSON(m)
			}
		case "model_max_budget":
			m, ok := v.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid numeric key response field model_max_budget")
			}
			values, err := legacyFloatMapFromJSON(m)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field model_max_budget")
			}
			createdKey.ModelMaxBudget = values
		case "model_rpm_limit":
			m, ok := v.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid numeric key response field model_rpm_limit")
			}
			values, err := legacyIntMapFromJSON(m)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field model_rpm_limit")
			}
			createdKey.ModelRPMLimit = values
		case "model_tpm_limit":
			m, ok := v.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid numeric key response field model_tpm_limit")
			}
			values, err := legacyIntMapFromJSON(m)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric key response field model_tpm_limit")
			}
			createdKey.ModelTPMLimit = values
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
			return nil, fmt.Errorf("error marshaling LiteLLM request body")
		}
		log.Printf("Making %s LiteLLM request (body omitted)", method)
		req, err = http.NewRequest(method, url, bytes.NewBuffer(jsonBody))
	} else {
		log.Printf("Making %s LiteLLM request", method)
		req, err = http.NewRequest(method, url, nil)
	}

	if err != nil {
		return nil, fmt.Errorf("error creating LiteLLM request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LiteLLM HTTP request failed")
	}
	defer resp.Body.Close()

	bodyBytes, err := readLegacyResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}

	log.Printf("LiteLLM response status: %d (body omitted)", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status code %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := decodeJSONUseNumber(bodyBytes, &result); err != nil {
		if method == "POST" && (len(bodyBytes) == 0 || string(bodyBytes) == "null") {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("error parsing LiteLLM response JSON")
	}

	return result, nil
}
