package litellm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
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

	if errResp.Detail.Error != "" {
		if strings.Contains(errResp.Detail.Error, "not found on litellm proxy") {
			return true
		}
	}

	return false
}

func handleAPIResponse(resp *http.Response, reqBody interface{}) (*ModelResponse, error) {
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err == nil {
			if isModelNotFoundError(errResp) {
				return nil, fmt.Errorf("model_not_found")
			}
		}
		reqBodyBytes, _ := json.Marshal(reqBody)
		return nil, fmt.Errorf("API request failed: Status: %s, Response: %s, Request: %s",
			resp.Status, string(bodyBytes), string(reqBodyBytes))
	}

	var modelResp ModelResponse
	if err := json.Unmarshal(bodyBytes, &modelResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
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
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		req, err = http.NewRequest(method, fmt.Sprintf("%s%s", client.APIBase, endpoint), bytes.NewBuffer(jsonData))
	} else {
		req, err = http.NewRequest(method, fmt.Sprintf("%s%s", client.APIBase, endpoint), nil)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", client.APIKey)

	return client.httpClient.Do(req)
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
