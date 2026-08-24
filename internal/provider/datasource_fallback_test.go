package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFallbackDataSourceReadRetriesNotFoundAndPopulatesState(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("fallback_type"); got != "general" {
			t.Errorf("fallback_type query = %q, want general", got)
		}

		if attempts.Add(1) < 3 {
			http.Error(w, `{"detail":{"error":"fallback not found"}}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"fallback_type":   "general",
			"fallback_models": []string{"secondary-a", "secondary-b"},
		})
	}))
	defer server.Close()

	d := &FallbackDataSource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}
	data := &FallbackDataSourceModel{
		Model:        types.StringValue("primary"),
		FallbackType: types.StringValue("general"),
	}

	if err := d.readFallbackWithRetry(context.Background(), data, 5, 0, 0); err != nil {
		t.Fatalf("readFallbackWithRetry returned error: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if got := data.ID.ValueString(); got != "primary:general" {
		t.Errorf("id = %q, want primary:general", got)
	}
	if got := data.FallbackType.ValueString(); got != "general" {
		t.Errorf("fallback_type = %q, want general", got)
	}

	var models []string
	if diags := data.FallbackModels.ElementsAs(context.Background(), &models, false); diags.HasError() {
		t.Fatalf("decode fallback_models: %v", diags)
	}
	if len(models) != 2 || models[0] != "secondary-a" || models[1] != "secondary-b" {
		t.Errorf("fallback_models = %v, want [secondary-a secondary-b]", models)
	}
}

func TestRetryFallbackReadStopsAtAttemptLimit(t *testing.T) {
	t.Parallel()

	var attempts int
	expected := errors.New("404 fallback not found")
	err := retryFallbackRead(context.Background(), 3, 0, 0, func() error {
		attempts++
		return expected
	})

	if !errors.Is(err, expected) {
		t.Fatalf("error = %v, want %v", err, expected)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRetryFallbackReadDoesNotRetryOtherErrors(t *testing.T) {
	t.Parallel()

	var attempts int
	expected := errors.New("API request failed with status 500")
	err := retryFallbackRead(context.Background(), 5, 0, 0, func() error {
		attempts++
		return expected
	})

	if !errors.Is(err, expected) {
		t.Fatalf("error = %v, want %v", err, expected)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestRetryFallbackReadHonorsCancellationDuringBackoff(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts int
	err := retryFallbackRead(ctx, 5, time.Hour, time.Hour, func() error {
		attempts++
		cancel()
		return errors.New("404 fallback not found")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}
