package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIError represents a structured API error with HTTP status code.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API request failed with status %d: %s", e.StatusCode, e.Message)
}

// IsNotFoundError checks if an error is specifically a 404 Not Found.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	apiErr, ok := err.(*APIError)
	return ok && apiErr.StatusCode == http.StatusNotFound
}

// RetryOnNotFound retries a function up to maxRetries times if it returns a 404,
// with exponential backoff. This handles eventual consistency after resource creation.
func RetryOnNotFound(ctx context.Context, fn func() error, maxRetries int) error {
	var err error
	delay := 1 * time.Second
	maxDelay := 10 * time.Second

	for i := 0; i < maxRetries; i++ {
		err = fn()
		if err == nil || !IsNotFoundError(err) {
			return err
		}

		if i < maxRetries-1 {
			select {
			case <-time.After(delay):
				delay *= 2
				if delay > maxDelay {
					delay = maxDelay
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return err
}

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
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(bodyBytes),
		}
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


