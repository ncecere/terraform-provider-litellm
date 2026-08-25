package litellm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func decodeJSONUseNumber(data []byte, result interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

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
		if err := decodeJSONUseNumber(bodyBytes, &errResp); err == nil {
			if isModelNotFoundError(errResp) {
				return nil, fmt.Errorf("model_not_found")
			}
		}
		return nil, fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	var modelResp ModelResponse
	if err := decodeJSONUseNumber(bodyBytes, &modelResp); err != nil {
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
		if err := decodeJSONUseNumber(bodyBytes, &errResp); err == nil {
			if isMCPServerNotFoundError(errResp) {
				return fmt.Errorf("mcp_server_not_found")
			}
		}
		return fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	if err := decodeJSONUseNumber(bodyBytes, result); err != nil {
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
		if err := decodeJSONUseNumber(bodyBytes, &errResp); err == nil {
			if isCredentialNotFoundError(errResp) {
				return fmt.Errorf("credential_not_found")
			}
		}
		return fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	trimmed := bytes.TrimSpace(bodyBytes)
	if result != nil {
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return fmt.Errorf("failed to parse LiteLLM response")
		}
		if err := decodeJSONUseNumber(trimmed, result); err != nil {
			return fmt.Errorf("failed to parse LiteLLM response")
		}
		return nil
	}
	// Empty successful mutation responses remain valid, but any body LiteLLM
	// does send must be one complete JSON value rather than silently ignored
	// malformed text.
	if len(trimmed) > 0 {
		var discarded interface{}
		if err := decodeJSONUseNumber(trimmed, &discarded); err != nil {
			return fmt.Errorf("failed to parse LiteLLM response")
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
		if err := decodeJSONUseNumber(bodyBytes, &errResp); err == nil {
			if isVectorStoreNotFoundError(errResp) {
				return fmt.Errorf("vector_store_not_found")
			}
		}
		return fmt.Errorf("API request failed: HTTP %d", resp.StatusCode)
	}

	if result != nil {
		if err := decodeJSONUseNumber(bodyBytes, result); err != nil {
			return fmt.Errorf("failed to parse LiteLLM response")
		}
	}

	return nil
}
