package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestValidatePolicyAttachmentTargeting_GlobalScopeOnly(t *testing.T) {
	t.Parallel()

	err := validatePolicyAttachmentTargeting(
		context.Background(),
		types.StringValue("*"),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
	)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidatePolicyAttachmentTargeting_TargetsOnly(t *testing.T) {
	t.Parallel()

	err := validatePolicyAttachmentTargeting(
		context.Background(),
		types.StringNull(),
		stringListValue("team-a"),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
	)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidatePolicyAttachmentTargeting_InvalidScopeValue(t *testing.T) {
	t.Parallel()

	err := validatePolicyAttachmentTargeting(
		context.Background(),
		types.StringValue("team"),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
	)

	if err == nil {
		t.Fatal("expected error for invalid scope value")
	}
}

func TestValidatePolicyAttachmentTargeting_ScopeWithTargets(t *testing.T) {
	t.Parallel()

	err := validatePolicyAttachmentTargeting(
		context.Background(),
		types.StringValue("*"),
		stringListValue("team-a"),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
	)

	if err == nil {
		t.Fatal("expected error when scope is combined with targeting lists")
	}
}

func TestValidatePolicyAttachmentTargeting_NoScopeAndNoTargets(t *testing.T) {
	t.Parallel()

	err := validatePolicyAttachmentTargeting(
		context.Background(),
		types.StringNull(),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
		types.ListNull(types.StringType),
	)

	if err == nil {
		t.Fatal("expected error when no scope and no targets are set")
	}
}

func TestBuildPolicyAttachmentRequest(t *testing.T) {
	t.Parallel()

	req := buildPolicyAttachmentRequest(
		context.Background(),
		types.StringValue("global-baseline"),
		types.StringNull(),
		stringListValue("team-a", "team-b"),
		types.ListNull(types.StringType),
		stringListValue("gpt-4o"),
		stringListValue("health-*"),
	)

	if req["policy_name"] != "global-baseline" {
		t.Errorf("expected policy_name to be set, got %v", req["policy_name"])
	}

	teams, ok := req["teams"].([]string)
	if !ok || len(teams) != 2 {
		t.Fatalf("expected two teams, got %v", req["teams"])
	}

	if _, exists := req["scope"]; exists {
		t.Error("scope should not be set when null")
	}
}

func TestReadPolicyAttachment_ClearsExistingScopeWhenOmitted(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"attachment_id": "att-123",
			"policy_name":   "baseline",
		})
	}))
	defer server.Close()

	r := &PolicyAttachmentResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := PolicyAttachmentResourceModel{
		ID:    types.StringValue("att-123"),
		Scope: types.StringValue("*"),
	}

	if err := r.readPolicyAttachment(context.Background(), &data); err != nil {
		t.Fatalf("readPolicyAttachment returned error: %v", err)
	}

	if !data.Scope.IsNull() {
		t.Fatalf("expected scope to be cleared to null, got %q", data.Scope.ValueString())
	}
}
