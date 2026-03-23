package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildProjectRequest_Minimal(t *testing.T) {
	t.Parallel()

	r := &ProjectResource{}
	data := &ProjectResourceModel{
		TeamID: types.StringValue("team-123"),
	}

	req := r.buildProjectRequest(context.Background(), data)

	if req["team_id"] != "team-123" {
		t.Errorf("expected team_id 'team-123', got %v", req["team_id"])
	}
	// Should not have optional fields
	if _, exists := req["project_alias"]; exists {
		t.Error("project_alias should not be present when not configured")
	}
	if _, exists := req["max_budget"]; exists {
		t.Error("max_budget should not be present when not configured")
	}
}

func TestBuildProjectRequest_Full(t *testing.T) {
	t.Parallel()

	r := &ProjectResource{}
	data := &ProjectResourceModel{
		TeamID:       types.StringValue("team-456"),
		ProjectAlias: types.StringValue("my-project"),
		Description:  types.StringValue("A test project"),
		MaxBudget:    types.Float64Value(500.0),
		SoftBudget:   types.Float64Value(400.0),
		TPMLimit:     types.Int64Value(50000),
		RPMLimit:     types.Int64Value(500),
		Blocked:      types.BoolValue(false),
		Models:       stringListValue("gpt-4o", "gpt-4o-mini"),
		Tags:         stringListValue("production", "team-a"),
		Metadata: stringMapValue(map[string]string{
			"env":    "prod",
			"config": `{"retries":3}`,
		}),
	}

	req := r.buildProjectRequest(context.Background(), data)

	if req["team_id"] != "team-456" {
		t.Errorf("expected team_id 'team-456', got %v", req["team_id"])
	}
	if req["project_alias"] != "my-project" {
		t.Errorf("expected project_alias 'my-project', got %v", req["project_alias"])
	}
	if req["description"] != "A test project" {
		t.Errorf("expected description 'A test project', got %v", req["description"])
	}
	if req["max_budget"] != 500.0 {
		t.Errorf("expected max_budget 500.0, got %v", req["max_budget"])
	}
	if req["tpm_limit"] != int64(50000) {
		t.Errorf("expected tpm_limit 50000, got %v", req["tpm_limit"])
	}

	models, ok := req["models"].([]string)
	if !ok || len(models) != 2 {
		t.Fatalf("expected 2 models, got %v", req["models"])
	}

	// Metadata should have native JSON for "config"
	meta, ok := req["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata map, got %T", req["metadata"])
	}
	if meta["env"] != "prod" {
		t.Errorf("expected env 'prod', got %v", meta["env"])
	}
	if _, ok := meta["config"].(map[string]interface{}); !ok {
		t.Errorf("expected config to be native map, got %T", meta["config"])
	}
}

func TestReadProject_PopulatesState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"project_id":    "proj-abc-123",
			"project_alias": "my-project",
			"description":   "A test project",
			"team_id":       "team-456",
			"models":        []interface{}{"gpt-4o"},
			"tags":          []interface{}{"production"},
			"metadata": map[string]interface{}{
				"env": "prod",
			},
			"blocked":         false,
			"model_rpm_limit": map[string]interface{}{"gpt-4o": float64(100)},
			"model_tpm_limit": map[string]interface{}{"gpt-4o": float64(10000)},
			"created_at":      "2026-03-20T10:00:00Z",
			"updated_at":      "2026-03-20T11:00:00Z",
			"created_by":      "admin",
			"updated_by":      "admin",
		})
	}))
	defer server.Close()

	r := &ProjectResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ProjectResourceModel{
		ID:             types.StringValue("proj-abc-123"),
		Models:         types.ListUnknown(types.StringType),
		Tags:           types.ListUnknown(types.StringType),
		Metadata:       types.MapUnknown(types.StringType),
		ModelRPMLimit:  types.MapUnknown(types.Int64Type),
		ModelTPMLimit:  types.MapUnknown(types.Int64Type),
		ModelMaxBudget: types.MapUnknown(types.Float64Type),
	}

	if err := r.readProject(context.Background(), &data); err != nil {
		t.Fatalf("readProject returned error: %v", err)
	}

	if data.ID.ValueString() != "proj-abc-123" {
		t.Errorf("expected ID 'proj-abc-123', got %q", data.ID.ValueString())
	}
	if data.ProjectAlias.ValueString() != "my-project" {
		t.Errorf("expected project_alias 'my-project', got %q", data.ProjectAlias.ValueString())
	}
	if data.TeamID.ValueString() != "team-456" {
		t.Errorf("expected team_id 'team-456', got %q", data.TeamID.ValueString())
	}
	if data.CreatedBy.ValueString() != "admin" {
		t.Errorf("expected created_by 'admin', got %q", data.CreatedBy.ValueString())
	}

	// Models
	if data.Models.IsUnknown() || data.Models.IsNull() {
		t.Fatal("models should be known and non-null")
	}
	if len(data.Models.Elements()) != 1 {
		t.Errorf("expected 1 model, got %d", len(data.Models.Elements()))
	}

	// Tags
	if data.Tags.IsUnknown() || data.Tags.IsNull() {
		t.Fatal("tags should be known and non-null")
	}

	// Metadata
	if data.Metadata.IsUnknown() || data.Metadata.IsNull() {
		t.Fatal("metadata should be known and non-null")
	}

	// Model limits
	if data.ModelRPMLimit.IsUnknown() {
		t.Fatal("model_rpm_limit should be known after read")
	}
	if data.ModelTPMLimit.IsUnknown() {
		t.Fatal("model_tpm_limit should be known after read")
	}
}

func TestReadProject_ResolvesUnknownToNull(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"project_id": "proj-minimal",
			"team_id":    "team-1",
			"created_by": "admin",
			"updated_by": "admin",
		})
	}))
	defer server.Close()

	r := &ProjectResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ProjectResourceModel{
		ID:             types.StringValue("proj-minimal"),
		Models:         types.ListUnknown(types.StringType),
		Tags:           types.ListUnknown(types.StringType),
		Metadata:       types.MapUnknown(types.StringType),
		ModelMaxBudget: types.MapUnknown(types.Float64Type),
		ModelRPMLimit:  types.MapUnknown(types.Int64Type),
		ModelTPMLimit:  types.MapUnknown(types.Int64Type),
		Blocked:        types.BoolUnknown(),
	}

	if err := r.readProject(context.Background(), &data); err != nil {
		t.Fatalf("readProject returned error: %v", err)
	}

	if data.Models.IsUnknown() {
		t.Error("models should not be Unknown after read")
	}
	if data.Tags.IsUnknown() {
		t.Error("tags should not be Unknown after read")
	}
	if data.Metadata.IsUnknown() {
		t.Error("metadata should not be Unknown after read")
	}
	if data.ModelMaxBudget.IsUnknown() {
		t.Error("model_max_budget should not be Unknown after read")
	}
	if data.ModelRPMLimit.IsUnknown() {
		t.Error("model_rpm_limit should not be Unknown after read")
	}
	if data.ModelTPMLimit.IsUnknown() {
		t.Error("model_tpm_limit should not be Unknown after read")
	}
	if data.Blocked.IsUnknown() {
		t.Error("blocked should not be Unknown after read")
	}
}
