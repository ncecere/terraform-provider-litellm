package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "404 APIError",
			err:      &APIError{StatusCode: http.StatusNotFound, Message: "not found"},
			expected: true,
		},
		{
			name:     "500 APIError",
			err:      &APIError{StatusCode: http.StatusInternalServerError, Message: "server error"},
			expected: false,
		},
		{
			name:     "502 APIError",
			err:      &APIError{StatusCode: http.StatusBadGateway, Message: "bad gateway"},
			expected: false,
		},
		{
			name:     "503 APIError",
			err:      &APIError{StatusCode: http.StatusServiceUnavailable, Message: "service unavailable"},
			expected: false,
		},
		{
			name:     "non-APIError",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNotFoundError(tt.err)
			if got != tt.expected {
				t.Errorf("IsNotFoundError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRetryOnNotFound_Success(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		if callCount < 3 {
			return &APIError{StatusCode: http.StatusNotFound, Message: "not found yet"}
		}
		return nil
	}

	ctx := context.Background()
	err := RetryOnNotFound(ctx, fn, 5)

	if err != nil {
		t.Errorf("RetryOnNotFound() returned error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestRetryOnNotFound_MaxRetriesExceeded(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return &APIError{StatusCode: http.StatusNotFound, Message: "not found"}
	}

	ctx := context.Background()
	err := RetryOnNotFound(ctx, fn, 3)

	if err == nil {
		t.Error("RetryOnNotFound() expected error, got nil")
	}
	if !IsNotFoundError(err) {
		t.Errorf("RetryOnNotFound() error should be 404, got: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestRetryOnNotFound_NonNotFoundError(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return &APIError{StatusCode: http.StatusInternalServerError, Message: "server error"}
	}

	ctx := context.Background()
	err := RetryOnNotFound(ctx, fn, 5)

	if err == nil {
		t.Error("RetryOnNotFound() expected error, got nil")
	}
	if IsNotFoundError(err) {
		t.Error("RetryOnNotFound() should not return 404 for 500 error")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (fail fast), got %d", callCount)
	}
}

func TestRetryOnNotFound_ContextCancellation(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return &APIError{StatusCode: http.StatusNotFound, Message: "not found"}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context after a short delay to test cancellation during retry
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := RetryOnNotFound(ctx, fn, 10)

	if err == nil {
		t.Error("RetryOnNotFound() expected error from context cancellation")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	// Should have attempted at least once before cancellation
	if callCount < 1 {
		t.Errorf("expected at least 1 call, got %d", callCount)
	}
}

func TestRetryOnNotFound_ExponentialBackoff(t *testing.T) {
	callCount := 0
	callTimes := []time.Time{}
	fn := func() error {
		callCount++
		callTimes = append(callTimes, time.Now())
		if callCount < 4 {
			return &APIError{StatusCode: http.StatusNotFound, Message: "not found"}
		}
		return nil
	}

	ctx := context.Background()
	start := time.Now()
	err := RetryOnNotFound(ctx, fn, 5)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("RetryOnNotFound() returned error: %v", err)
	}
	if callCount != 4 {
		t.Errorf("expected 4 calls, got %d", callCount)
	}

	// Verify backoff timing (1s + 2s + 4s = 7s minimum, allow some tolerance)
	expectedMin := 7 * time.Second
	if elapsed < expectedMin {
		t.Errorf("expected elapsed time >= %v, got %v", expectedMin, elapsed)
	}
}

func TestRetryOnNotFound_502Error(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return &APIError{StatusCode: http.StatusBadGateway, Message: "bad gateway"}
	}

	ctx := context.Background()
	err := RetryOnNotFound(ctx, fn, 5)

	if err == nil {
		t.Error("RetryOnNotFound() expected error, got nil")
	}
	if IsNotFoundError(err) {
		t.Error("RetryOnNotFound() should not treat 502 as 404")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (fail fast on 502), got %d", callCount)
	}
}

func TestRetryOnNotFound_TimeoutError(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return fmt.Errorf("request timeout")
	}

	ctx := context.Background()
	err := RetryOnNotFound(ctx, fn, 5)

	if err == nil {
		t.Error("RetryOnNotFound() expected error, got nil")
	}
	if IsNotFoundError(err) {
		t.Error("RetryOnNotFound() should not treat timeout as 404")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (fail fast on timeout), got %d", callCount)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	err := &APIError{StatusCode: 404, Message: "resource not found"}
	expected := "API request failed with status 404: resource not found"
	if err.Error() != expected {
		t.Errorf("APIError.Error() = %q, want %q", err.Error(), expected)
	}
}
