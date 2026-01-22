package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DoRequest performs an HTTP request with context and standard headers.
func (c *Client) DoRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	url := c.APIBase + path

	var req *http.Request
	var err error

	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.APIKey)

	for key, value := range c.AdditionalHeaders {
		req.Header.Set(key, value)
	}

	if c.LiteLLMChangedBy != "" {
		req.Header.Set("litellm-changed-by", c.LiteLLMChangedBy)
	}

	return c.HTTPClient.Do(req)
}

// DoRequestWithResponse performs an HTTP request and decodes the JSON response.
func (c *Client) DoRequestWithResponse(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	resp, err := c.DoRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Handle non-2xx status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// If no result expected, return early
	if result == nil {
		return nil
	}

	// Parse response
	if err := json.Unmarshal(bodyBytes, result); err != nil {
		// For empty responses, this is acceptable
		if len(bodyBytes) == 0 || string(bodyBytes) == "null" {
			return nil
		}
		return fmt.Errorf("failed to parse response: %w", err)
	}

	return nil
}

// IsNotFoundError checks if the error message indicates a not found condition.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "not found") ||
		contains(errStr, "404") ||
		contains(errStr, "does not exist")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
