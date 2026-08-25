package litellm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func isModelNotFoundError(errResp ErrorResponse) bool {
	if msg, ok := errResp.Error.Message.(string); ok {
		if strings.Contains(msg, "model not found") {
			return true
		}
	}

	if msgMap, ok := errResp.Error.Message.(map[string]interface{}); ok {
		if errStr, ok := msgMap["error"].(string); ok {
			if strings.Contains(errStr, "Model with id=") && strings.Contains(errStr, "not found in db") {
				return true
			}
		}
	}

	// Check Detail.Error field for LiteLLM proxy error format
	if errResp.Detail.Error != "" {
		if strings.Contains(errResp.Detail.Error, "not found on litellm proxy") {
			return true
		}
	}

	return false
}

func handleAPIResponse(resp *http.Response, _ interface{}) (*ModelResponse, error) {
	bodyBytes, err := readLegacyResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err == nil {
			if isModelNotFoundError(errResp) {
				return nil, fmt.Errorf("model_not_found")
			}
		}
		return nil, fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	var modelResp ModelResponse
	if err := json.Unmarshal(bodyBytes, &modelResp); err != nil {
		return nil, fmt.Errorf("failed to parse LiteLLM response")
	}

	return &modelResp, nil
}

// MakeRequest is a helper function to make HTTP requests
func MakeRequest(client *Client, method, endpoint string, body interface{}) (*http.Response, error) {
	var req *http.Request
	var err error

	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal LiteLLM request body")
		}
		req, err = http.NewRequest(method, fmt.Sprintf("%s%s", client.APIBase, endpoint), bytes.NewBuffer(jsonData))
	} else {
		req, err = http.NewRequest(method, fmt.Sprintf("%s%s", client.APIBase, endpoint), nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create LiteLLM request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", client.APIKey)

	response, err := client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LiteLLM HTTP request failed")
	}
	return response, nil
}

// Helper functions to handle potential nil values from the API response
func GetStringValue(apiValue, defaultValue string) string {
	if apiValue != "" {
		return apiValue
	}
	return defaultValue
}

func GetIntValue(apiValue, defaultValue int) int {
	if apiValue != 0 {
		return apiValue
	}
	return defaultValue
}

func GetFloatValue(apiValue, defaultValue float64) float64 {
	if apiValue != 0 {
		return apiValue
	}
	return defaultValue
}

func GetBoolValue(apiValue, defaultValue bool) bool {
	return apiValue
}

// handleMCPAPIResponse handles API responses specifically for MCP server operations
func handleMCPAPIResponse(resp *http.Response, result interface{}) error {
	bodyBytes, err := readLegacyResponseBody(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err == nil {
			if isMCPServerNotFoundError(errResp) {
				return fmt.Errorf("mcp_server_not_found")
			}
		}
		return fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	if err := json.Unmarshal(bodyBytes, result); err != nil {
		return fmt.Errorf("failed to parse LiteLLM response")
	}

	return nil
}

// isMCPServerNotFoundError checks if the error response indicates an MCP server not found
func isMCPServerNotFoundError(errResp ErrorResponse) bool {
	if msg, ok := errResp.Error.Message.(string); ok {
		if strings.Contains(msg, "mcp server not found") || strings.Contains(msg, "server not found") {
			return true
		}
	}

	if msgMap, ok := errResp.Error.Message.(map[string]interface{}); ok {
		if errStr, ok := msgMap["error"].(string); ok {
			if strings.Contains(errStr, "MCP server with id=") && strings.Contains(errStr, "not found") {
				return true
			}
		}
	}

	// Check Detail.Error field for LiteLLM proxy error format
	if errResp.Detail.Error != "" {
		if strings.Contains(errResp.Detail.Error, "not found") {
			return true
		}
	}

	return false
}

// isCredentialNotFoundError checks if the error response indicates a credential not found
func isCredentialNotFoundError(errResp ErrorResponse) bool {
	if msg, ok := errResp.Error.Message.(string); ok {
		if strings.Contains(msg, "credential not found") {
			return true
		}
	}

	if msgMap, ok := errResp.Error.Message.(map[string]interface{}); ok {
		if errStr, ok := msgMap["error"].(string); ok {
			if strings.Contains(errStr, "Credential with name=") && strings.Contains(errStr, "not found") {
				return true
			}
		}
	}

	// Check Detail.Error field for LiteLLM proxy error format
	if errResp.Detail.Error != "" {
		if strings.Contains(errResp.Detail.Error, "credential not found") {
			return true
		}
	}

	return false
}

// handleCredentialAPIResponse handles API responses specifically for credential operations
func handleCredentialAPIResponse(resp *http.Response, result interface{}) error {
	bodyBytes, err := readLegacyResponseBody(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("credential_not_found")
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var errResp ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err == nil {
			if isCredentialNotFoundError(errResp) {
				return fmt.Errorf("credential_not_found")
			}
		}
		return fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	// For credential operations, we might get a simple string response or a credential object
	if result != nil {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			// If parsing fails, it might be a simple string response which is fine for create/update/delete
			return nil
		}
	}

	return nil
}

// isVectorStoreNotFoundError checks if the error response indicates a vector store not found
func isVectorStoreNotFoundError(errResp ErrorResponse) bool {
	if msg, ok := errResp.Error.Message.(string); ok {
		if strings.Contains(msg, "vector store not found") {
			return true
		}
	}

	if msgMap, ok := errResp.Error.Message.(map[string]interface{}); ok {
		if errStr, ok := msgMap["error"].(string); ok {
			if strings.Contains(errStr, "Vector store with id=") && strings.Contains(errStr, "not found") {
				return true
			}
		}
	}

	// Check Detail.Error field for LiteLLM proxy error format
	if errResp.Detail.Error != "" {
		if strings.Contains(errResp.Detail.Error, "vector store not found") {
			return true
		}
	}

	return false
}

// handleVectorStoreAPIResponse handles API responses specifically for vector store operations
func handleVectorStoreAPIResponse(resp *http.Response, result interface{}) error {
	bodyBytes, err := readLegacyResponseBody(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("vector_store_not_found")
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var errResp ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err == nil {
			if isVectorStoreNotFoundError(errResp) {
				return fmt.Errorf("vector_store_not_found")
			}
		}
		return fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	if result != nil {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			return fmt.Errorf("failed to parse LiteLLM response")
		}
	}

	return nil
}
