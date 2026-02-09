package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestReadTeamResolvesUnknownOptionalComputedCollections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "agent-team",
				"blocked":    false,
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_member_permissions": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := &TeamResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := TeamResourceModel{
		ID:                    types.StringValue("team-123"),
		TeamAlias:             types.StringValue("agent-team"),
		Models:                types.ListUnknown(types.StringType),
		Tags:                  types.ListUnknown(types.StringType),
		Guardrails:            types.ListUnknown(types.StringType),
		Prompts:               types.ListUnknown(types.StringType),
		Metadata:              types.MapUnknown(types.StringType),
		ModelAliases:          types.MapUnknown(types.StringType),
		ModelRPMLimit:         types.MapUnknown(types.Int64Type),
		ModelTPMLimit:         types.MapUnknown(types.Int64Type),
		TeamMemberPermissions: types.ListUnknown(types.StringType),
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	if data.Models.IsUnknown() {
		t.Fatal("models should be known after read")
	}
	if data.Tags.IsUnknown() {
		t.Fatal("tags should be known after read")
	}
	if data.Guardrails.IsUnknown() {
		t.Fatal("guardrails should be known after read")
	}
	if data.Prompts.IsUnknown() {
		t.Fatal("prompts should be known after read")
	}
	if data.Metadata.IsUnknown() {
		t.Fatal("metadata should be known after read")
	}
	if data.ModelAliases.IsUnknown() {
		t.Fatal("model_aliases should be known after read")
	}
	if data.ModelRPMLimit.IsUnknown() {
		t.Fatal("model_rpm_limit should be known after read")
	}
	if data.ModelTPMLimit.IsUnknown() {
		t.Fatal("model_tpm_limit should be known after read")
	}
	if data.TeamMemberPermissions.IsUnknown() {
		t.Fatal("team_member_permissions should be known after read")
	}
}
