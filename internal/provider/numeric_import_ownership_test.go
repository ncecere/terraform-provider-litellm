package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNumericImportOwnershipAdoptsOptionalResourceValues(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/project/info":
			_, _ = writer.Write([]byte(`{"project_id":"project-1","team_id":"team-owner","litellm_budget_table":{"max_budget":10.5,"tpm_limit":9007199254740993,"rpm_limit":60,"model_max_budget":{"gpt":1.25}},"metadata":{"model_rpm_limit":{"gpt":9007199254740993},"model_tpm_limit":{}}}`))
		case "/tag/info":
			_, _ = writer.Write([]byte(`{"tag-1":{"name":"tag-1","litellm_budget_table":{"max_budget":20.5,"tpm_limit":9007199254740993,"rpm_limit":70,"model_max_budget":{"gpt":9007199254740993}}}}`))
		case "/team/info":
			_, _ = writer.Write([]byte(`{"team_info":{"team_id":"team-1","team_alias":"team","max_budget":30.5,"tpm_limit":9007199254740993,"rpm_limit":80,"metadata":{"model_rpm_limit":{"gpt":9007199254740993},"model_tpm_limit":{}}}}`))
		case "/team/permissions_list":
			_, _ = writer.Write([]byte(`{"team_member_permissions":[]}`))
		case "/user/info":
			_, _ = writer.Write([]byte(`{"user_info":{"user_id":"user-1","user_email":"user@example.com","max_budget":40.5,"tpm_limit":9007199254740993,"rpm_limit":90}}`))
		case "/key/info":
			_, _ = writer.Write([]byte(`{"key":"sk-test","info":{"max_budget":50.5,"tpm_limit":9007199254740993,"rpm_limit":100,"model_max_budget":{"gpt":1.5},"metadata":{"model_rpm_limit":{"gpt":9007199254740993},"model_tpm_limit":{}}}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	ctx := context.Background()

	project := &ProjectResourceModel{ID: types.StringValue("project-1")}
	if err := (&ProjectResource{client: client}).readProjectWithNumericOwnership(ctx, project, true); err != nil {
		t.Fatal(err)
	}
	if project.TPMLimit.ValueInt64() != 9007199254740993 || project.MaxBudget.ValueFloat64() != 10.5 {
		t.Fatalf("project import numbers = %#v", project)
	}
	var projectRPM map[string]int64
	if diagnostics := project.ModelRPMLimit.ElementsAs(ctx, &projectRPM, false); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if projectRPM["gpt"] != 9007199254740993 || project.ModelTPMLimit.IsNull() {
		t.Fatalf("project import maps = %#v, %#v", project.ModelRPMLimit, project.ModelTPMLimit)
	}

	tag := &TagResourceModel{ID: types.StringValue("tag-1"), Name: types.StringValue("tag-1")}
	if err := (&TagResource{client: client}).readTagWithNumericOwnership(ctx, tag, true); err != nil {
		t.Fatal(err)
	}
	if tag.TPMLimit.ValueInt64() != 9007199254740993 || tag.MaxBudget.ValueFloat64() != 20.5 || tag.ModelMaxBudget.ValueString() != `{"gpt":9007199254740993}` {
		t.Fatalf("tag import numbers = %#v", tag)
	}

	team := &TeamResourceModel{ID: types.StringValue("team-1")}
	if err := (&TeamResource{client: client}).readTeamWithNumericOwnership(ctx, team, true); err != nil {
		t.Fatal(err)
	}
	if team.TPMLimit.ValueInt64() != 9007199254740993 || team.MaxBudget.ValueFloat64() != 30.5 {
		t.Fatalf("team import numbers = %#v", team)
	}
	var teamRPM map[string]int64
	if diagnostics := team.ModelRPMLimit.ElementsAs(ctx, &teamRPM, false); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if teamRPM["gpt"] != 9007199254740993 || team.ModelTPMLimit.IsNull() {
		t.Fatalf("team import maps = %#v, %#v", team.ModelRPMLimit, team.ModelTPMLimit)
	}

	user := &UserResourceModel{ID: types.StringValue("user-1"), UserID: types.StringValue("user-1")}
	if err := (&UserResource{client: client}).readUserWithNumericOwnership(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	if user.TPMLimit.ValueInt64() != 9007199254740993 || user.MaxBudget.ValueFloat64() != 40.5 {
		t.Fatalf("user import numbers = %#v", user)
	}

	key := &KeyResourceModel{Key: types.StringValue("sk-test")}
	if err := (&KeyResource{client: client}).readKeyWithNumericOwnership(ctx, key, true); err != nil {
		t.Fatal(err)
	}
	if key.TPMLimit.ValueInt64() != 9007199254740993 || key.MaxBudget.ValueFloat64() != 50.5 {
		t.Fatalf("key import numbers = %#v", key)
	}
	var keyRPM map[string]int64
	if diagnostics := key.ModelRPMLimit.ElementsAs(ctx, &keyRPM, false); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if keyRPM["gpt"] != 9007199254740993 || key.ModelTPMLimit.IsNull() {
		t.Fatalf("key import maps = %#v, %#v", key.ModelRPMLimit, key.ModelTPMLimit)
	}
}
