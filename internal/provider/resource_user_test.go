package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFindExistingUserByExactEmailPaginatesAndIgnoresPartialMatches(t *testing.T) {
	t.Parallel()

	const email = "exact+tag@example.com"
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/user/list" || request.URL.Query().Get("user_email") != email || request.URL.Query().Get("page_size") != "100" {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		page := request.URL.Query().Get("page")
		pages.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		if page == "1" {
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"users":       []interface{}{map[string]interface{}{"user_id": "partial", "user_email": "prefix-" + email}},
				"total_pages": 2,
			})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"users":       []interface{}{map[string]interface{}{"user_id": "exact-id", "user_email": email}},
			"total_pages": 2,
		})
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	userID, err := resource.findExistingUserByExactEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("findExistingUserByExactEmail returned error: %v", err)
	}
	if userID != "exact-id" || pages.Load() != 2 {
		t.Fatalf("user_id = %q, pages = %d", userID, pages.Load())
	}
}

func TestFindExistingUserByExactEmailRejectsPartialOnly(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"users":       []interface{}{map[string]interface{}{"user_id": "partial", "user_email": "prefix-target@example.com"}},
			"total_pages": 1,
		})
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	if _, err := resource.findExistingUserByExactEmail(context.Background(), "target@example.com"); err == nil {
		t.Fatal("partial-only email match was accepted")
	}
}

func TestFindExistingUserByExactEmailRejectsAmbiguousMatches(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"users": []interface{}{
				map[string]interface{}{"user_id": "user-a", "user_email": "same@example.com"},
				map[string]interface{}{"user_id": "user-b", "user_email": "same@example.com"},
			},
			"total_pages": 1,
		})
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	if _, err := resource.findExistingUserByExactEmail(context.Background(), "same@example.com"); err == nil {
		t.Fatal("ambiguous exact matches were accepted")
	}
}

func TestAdoptExistingUserVerifiesAndConvergesWithoutCreatingKey(t *testing.T) {
	t.Parallel()

	var updated atomic.Bool
	var membershipUpdated atomic.Bool
	var memberAdds atomic.Int32
	var memberDeletes atomic.Int32
	var updateBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/user/list":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"users":       []interface{}{map[string]interface{}{"user_id": "existing-id", "user_email": "existing@example.com"}},
				"total_pages": 1,
			})
		case request.Method == http.MethodGet && request.URL.Path == "/user/info":
			alias := "old-alias"
			role := "internal_user"
			teams := []interface{}{"team-a"}
			if updated.Load() {
				alias = "managed-alias"
				role = "internal_user_viewer"
			}
			if membershipUpdated.Load() {
				teams = []interface{}{"team-b"}
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"user_info": map[string]interface{}{
				"user_id": "existing-id", "user_email": "existing@example.com", "user_alias": alias,
				"user_role": role, "teams": teams, "models": []interface{}{}, "metadata": map[string]interface{}{},
			}})
		case request.Method == http.MethodPost && request.URL.Path == "/user/update":
			if err := json.NewDecoder(request.Body).Decode(&updateBody); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			updated.Store(true)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"status": "ok"})
		case request.Method == http.MethodPost && request.URL.Path == "/team/member_add":
			memberAdds.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"status": "ok"})
		case request.Method == http.MethodPost && request.URL.Path == "/team/member_delete":
			memberDeletes.Add(1)
			membershipUpdated.Store(true)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"status": "ok"})
		default:
			http.Error(writer, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := UserResourceModel{
		UserID:        types.StringUnknown(),
		UserEmail:     types.StringValue("existing@example.com"),
		UserAlias:     types.StringValue("managed-alias"),
		UserRole:      types.StringValue("internal_user_viewer"),
		AutoCreateKey: types.BoolValue(true),
		Teams:         stringListValue("team-b"),
		Models:        types.ListNull(types.StringType),
		Metadata:      types.MapNull(types.StringType),
		Key:           types.StringUnknown(),
	}

	if err := resource.adoptExistingUser(context.Background(), &data); err != nil {
		t.Fatalf("adoptExistingUser returned error: %v", err)
	}
	if data.ID.ValueString() != "existing-id" || data.UserID.ValueString() != "existing-id" || data.UserAlias.ValueString() != "managed-alias" {
		t.Fatalf("unexpected adopted state: %#v", data)
	}
	if !data.Key.IsNull() {
		t.Fatalf("adoption exposed an unexpected key: %v", data.Key)
	}
	if updateBody["user_id"] != "existing-id" || updateBody["user_email"] != "existing@example.com" || updateBody["user_alias"] != "managed-alias" {
		t.Fatalf("unexpected update request: %#v", updateBody)
	}
	if _, exists := updateBody["auto_create_key"]; exists {
		t.Fatalf("adoption update requested an inaccessible key: %#v", updateBody)
	}
	if _, exists := updateBody["teams"]; exists {
		t.Fatalf("adoption sent teams through /user/update: %#v", updateBody)
	}
	if memberAdds.Load() != 1 || memberDeletes.Load() != 1 || !userTeamMembershipEqual(data.Teams, []string{"team-b"}) {
		t.Fatalf("team membership did not converge: adds=%d deletes=%d teams=%v", memberAdds.Load(), memberDeletes.Load(), data.Teams)
	}
}

func TestAdoptExistingUserRequiresKnownEmail(t *testing.T) {
	t.Parallel()

	resource := &UserResource{}
	for _, email := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("")} {
		data := UserResourceModel{UserEmail: email}
		if err := resource.adoptExistingUser(context.Background(), &data); err == nil {
			t.Fatalf("unsafe email %v was accepted for adoption", email)
		}
	}
}

func TestAdoptExistingUserRejectsConfiguredIDMismatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"users":       []interface{}{map[string]interface{}{"user_id": "existing-id", "user_email": "existing@example.com"}},
			"total_pages": 1,
		})
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := UserResourceModel{UserID: types.StringValue("different-id"), UserEmail: types.StringValue("existing@example.com")}
	if err := resource.adoptExistingUser(context.Background(), &data); err == nil {
		t.Fatal("configured user_id mismatch was accepted")
	}
}

func TestReadUserDoesNotSetAPIInjectedDefaultsWhenUnconfigured(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"user_info": map[string]interface{}{
				"user_id":         "user-defaults",
				"user_alias":      "User Defaults",
				"user_email":      "user-defaults@example.com",
				"user_role":       "internal_user",
				"budget_duration": "30d",
				"max_budget":      25.0,
				"tpm_limit":       10000000.0,
				"rpm_limit":       1000.0,
			},
		})
	}))
	defer server.Close()

	r := &UserResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := UserResourceModel{
		ID:             types.StringValue("user-defaults"),
		UserID:         types.StringValue("user-defaults"),
		UserRole:       types.StringNull(),
		BudgetDuration: types.StringNull(),
		MaxBudget:      types.Float64Null(),
		TPMLimit:       types.Int64Null(),
		RPMLimit:       types.Int64Null(),
		Teams:          types.ListNull(types.StringType),
		Models:         types.ListNull(types.StringType),
		Metadata:       types.MapNull(types.StringType),
	}

	if err := r.readUser(context.Background(), &data); err != nil {
		t.Fatalf("readUser returned error: %v", err)
	}

	if !data.UserRole.IsNull() {
		t.Fatalf("user_role should remain null when unconfigured, got %q", data.UserRole.ValueString())
	}
	if !data.BudgetDuration.IsNull() {
		t.Fatalf("budget_duration should remain null when unconfigured, got %q", data.BudgetDuration.ValueString())
	}
	if !data.MaxBudget.IsNull() {
		t.Fatalf("max_budget should remain null when unconfigured, got %v", data.MaxBudget.ValueFloat64())
	}
	if !data.TPMLimit.IsNull() {
		t.Fatalf("tpm_limit should remain null when unconfigured, got %v", data.TPMLimit.ValueInt64())
	}
	if !data.RPMLimit.IsNull() {
		t.Fatalf("rpm_limit should remain null when unconfigured, got %v", data.RPMLimit.ValueInt64())
	}
}

// TestNullPreservationForListAttributes verifies that when the API returns an
// empty list for an Optional+Computed list attribute that was not specified in
// the user's config (i.e., is null in state), the read-back logic preserves
// null rather than overwriting it with an empty list.
//
// This is the core fix for issue #51:
// "Creating a new user with resource 'litellm_user' required 'teams' for no apparent reason"
//
// The Terraform Framework requires that if an attribute is null in the plan,
// it must remain null in the state after apply. Overwriting null with []
// causes: "Provider produced inconsistent result after apply"

func TestNullListPreservation(t *testing.T) {
	t.Run("null teams stays null when API returns empty array", func(t *testing.T) {
		// Simulate: user did not specify teams in config → teams is null
		teams := types.ListNull(types.StringType)

		// Simulate: API returned "teams": [] (empty array)
		apiTeams := []interface{}{}

		// Apply the same logic as readUser
		if len(apiTeams) > 0 {
			teamsList := make([]attr.Value, len(apiTeams))
			for i, t := range apiTeams {
				if str, ok := t.(string); ok {
					teamsList[i] = types.StringValue(str)
				}
			}
			teams, _ = types.ListValue(types.StringType, teamsList)
		} else if !teams.IsNull() {
			teams, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if !teams.IsNull() {
			t.Errorf("expected teams to remain null when API returns empty array and config didn't specify teams, but got: %v", teams)
		}
	})

	t.Run("non-null teams becomes empty list when API returns empty array", func(t *testing.T) {
		// Simulate: user specified teams = ["team-1"] in config, then removed all teams
		initialTeams := []attr.Value{types.StringValue("team-1")}
		teams, _ := types.ListValue(types.StringType, initialTeams)

		// Simulate: API returned "teams": [] (empty array) after update
		apiTeams := []interface{}{}

		if len(apiTeams) > 0 {
			teamsList := make([]attr.Value, len(apiTeams))
			for i, t := range apiTeams {
				if str, ok := t.(string); ok {
					teamsList[i] = types.StringValue(str)
				}
			}
			teams, _ = types.ListValue(types.StringType, teamsList)
		} else if !teams.IsNull() {
			teams, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if teams.IsNull() {
			t.Error("expected teams to become empty list (not null) when user previously had teams in config")
		}
		if len(teams.Elements()) != 0 {
			t.Errorf("expected teams to be empty list, got %d elements", len(teams.Elements()))
		}
	})

	t.Run("teams populated when API returns non-empty array", func(t *testing.T) {
		// Simulate: user did not specify teams (null), but API returns teams
		teams := types.ListNull(types.StringType)

		// Simulate: API returned "teams": ["team-1", "team-2"]
		apiTeams := []interface{}{"team-1", "team-2"}

		if len(apiTeams) > 0 {
			teamsList := make([]attr.Value, len(apiTeams))
			for i, t := range apiTeams {
				if str, ok := t.(string); ok {
					teamsList[i] = types.StringValue(str)
				}
			}
			teams, _ = types.ListValue(types.StringType, teamsList)
		} else if !teams.IsNull() {
			teams, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if teams.IsNull() {
			t.Error("expected teams to be populated when API returns non-empty array")
		}
		if len(teams.Elements()) != 2 {
			t.Errorf("expected 2 teams, got %d", len(teams.Elements()))
		}
	})
}

func TestNullMapPreservation(t *testing.T) {
	t.Run("null metadata stays null when API returns empty map", func(t *testing.T) {
		// Simulate: user did not specify metadata in config → metadata is null
		metadata := types.MapNull(types.StringType)

		// Simulate: API returned "metadata": {} (empty map)
		apiMetadata := map[string]interface{}{}

		if len(apiMetadata) > 0 {
			metaMap := make(map[string]attr.Value)
			for k, v := range apiMetadata {
				if str, ok := v.(string); ok {
					metaMap[k] = types.StringValue(str)
				}
			}
			metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if !metadata.IsNull() {
			metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}

		if !metadata.IsNull() {
			t.Errorf("expected metadata to remain null when API returns empty map and config didn't specify metadata, but got: %v", metadata)
		}
	})

	t.Run("non-null metadata becomes empty map when API returns empty map", func(t *testing.T) {
		// Simulate: user specified metadata in config, then removed all entries
		initialMeta := map[string]attr.Value{"key": types.StringValue("value")}
		metadata, _ := types.MapValue(types.StringType, initialMeta)

		// Simulate: API returned "metadata": {} (empty map)
		apiMetadata := map[string]interface{}{}

		if len(apiMetadata) > 0 {
			metaMap := make(map[string]attr.Value)
			for k, v := range apiMetadata {
				if str, ok := v.(string); ok {
					metaMap[k] = types.StringValue(str)
				}
			}
			metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if !metadata.IsNull() {
			metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}

		if metadata.IsNull() {
			t.Error("expected metadata to become empty map (not null) when user previously had metadata in config")
		}
		if len(metadata.Elements()) != 0 {
			t.Errorf("expected metadata to be empty map, got %d elements", len(metadata.Elements()))
		}
	})

	t.Run("metadata populated when API returns non-empty map", func(t *testing.T) {
		// Simulate: user did not specify metadata (null), but API returns metadata
		metadata := types.MapNull(types.StringType)

		// Simulate: API returned "metadata": {"department": "Engineering"}
		apiMetadata := map[string]interface{}{"department": "Engineering"}

		if len(apiMetadata) > 0 {
			metaMap := make(map[string]attr.Value)
			for k, v := range apiMetadata {
				if str, ok := v.(string); ok {
					metaMap[k] = types.StringValue(str)
				}
			}
			metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if !metadata.IsNull() {
			metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}

		if metadata.IsNull() {
			t.Error("expected metadata to be populated when API returns non-empty map")
		}
		if len(metadata.Elements()) != 1 {
			t.Errorf("expected 1 metadata entry, got %d", len(metadata.Elements()))
		}
	})
}

// TestOldBehaviorWouldFail demonstrates what the OLD (buggy) behavior did.
// Before the fix, readUser would unconditionally set teams to [] from the API
// response even when the user didn't specify teams, causing:
// "Provider produced inconsistent result after apply: .teams: was null, but now cty.ListValEmpty(cty.String)"
func TestReconcileUnorderedUserTeams(t *testing.T) {
	t.Parallel()

	configured := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("team-b"),
		types.StringValue("team-a"),
	})

	tests := []struct {
		name    string
		current types.List
		remote  []interface{}
		want    types.List
	}{
		{
			name:    "reordered API membership preserves configured order",
			current: configured,
			remote:  []interface{}{"team-a", "team-b"},
			want:    configured,
		},
		{
			name:    "actual membership drift is sorted canonically",
			current: configured,
			remote:  []interface{}{"team-c", "team-a"},
			want: types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("team-a"),
				types.StringValue("team-c"),
			}),
		},
		{
			name:    "unconfigured empty membership remains null",
			current: types.ListNull(types.StringType),
			remote:  []interface{}{},
			want:    types.ListNull(types.StringType),
		},
		{
			name:    "configured membership cleared remotely becomes empty",
			current: configured,
			remote:  []interface{}{},
			want:    types.ListValueMust(types.StringType, []attr.Value{}),
		},
		{
			name:    "unknown import state adopts canonical API order",
			current: types.ListUnknown(types.StringType),
			remote:  []interface{}{"team-b", "team-a"},
			want: types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("team-a"),
				types.StringValue("team-b"),
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := reconcileUnorderedUserTeams(test.current, test.remote)
			if !got.Equal(test.want) {
				t.Fatalf("teams = %v, want %v", got, test.want)
			}
		})
	}
}

func TestReconcileUserTeamsUsesDedicatedEndpoints(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		path string
		body map[string]interface{}
	}
	var requests []capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(request.Body).Decode(&body)
		requests = append(requests, capturedRequest{path: request.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	current := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("team-b"),
		types.StringValue("team-a"),
	})
	planned := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("team-c"),
		types.StringValue("team-a"),
	})

	if err := resource.reconcileUserTeams(context.Background(), "user-1", current, planned); err != nil {
		t.Fatalf("reconcileUserTeams returned error: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	member, ok := requests[0].body["member"].(map[string]interface{})
	if requests[0].path != "/team/member_add" || requests[0].body["team_id"] != "team-c" || !ok || member["user_id"] != "user-1" || member["role"] != "user" {
		t.Fatalf("unexpected add request: %#v", requests[0])
	}
	if requests[1].path != "/team/member_delete" || requests[1].body["team_id"] != "team-b" || requests[1].body["user_id"] != "user-1" {
		t.Fatalf("unexpected delete request: %#v", requests[1])
	}
}

func TestReconcileUserTeamsDoesNotRemoveWhenAdditionFails(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/team/member_add" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"destination team does not exist"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	current := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("team-old")})
	planned := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("team-invalid")})
	if err := resource.reconcileUserTeams(context.Background(), "user-1", current, planned); err == nil {
		t.Fatal("reconcileUserTeams returned nil error for failed addition")
	}
	if len(paths) != 1 || paths[0] != "/team/member_add" {
		t.Fatalf("requests after failed addition = %v, want only /team/member_add", paths)
	}
}

func TestReadUserTeamsAfterUpdateRequiresStableMembership(t *testing.T) {
	t.Parallel()

	var reads atomic.Int32
	sequence := [][]interface{}{
		{"team-c", "team-a"},
		{"team-b", "team-a"},
		{"team-a", "team-c"},
		{"team-c", "team-a"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		index := int(reads.Add(1)) - 1
		if index >= len(sequence) {
			index = len(sequence) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"user_info": map[string]interface{}{
				"user_id": "user-1",
				"teams":   sequence[index],
			},
		})
	}))
	defer server.Close()

	resource := &UserResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := UserResourceModel{
		ID:     types.StringValue("user-1"),
		UserID: types.StringValue("user-1"),
		Teams: types.ListValueMust(types.StringType, []attr.Value{
			types.StringValue("team-c"),
			types.StringValue("team-a"),
		}),
	}

	if err := resource.readUserTeamsAfterUpdate(context.Background(), &data, 5); err != nil {
		t.Fatalf("readUserTeamsAfterUpdate returned error: %v", err)
	}
	if got := reads.Load(); got != 4 {
		t.Fatalf("read count = %d, want 4", got)
	}
	want := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("team-c"),
		types.StringValue("team-a"),
	})
	if !data.Teams.Equal(want) {
		t.Fatalf("teams = %v, want planned order %v", data.Teams, want)
	}
}

func TestUserTeamsRejectDuplicateMemberships(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&UserResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	attribute, ok := schemaResp.Schema.Attributes["teams"].(resourceschema.ListAttribute)
	if !ok {
		t.Fatal("teams is not a list attribute")
	}

	var validationResp validator.ListResponse
	request := validator.ListRequest{
		Path: path.Root("teams"),
		ConfigValue: types.ListValueMust(types.StringType, []attr.Value{
			types.StringValue("team-a"),
			types.StringValue("team-a"),
		}),
	}
	for _, listValidator := range attribute.Validators {
		listValidator.ValidateList(ctx, request, &validationResp)
	}
	if !validationResp.Diagnostics.HasError() {
		t.Fatal("duplicate team membership did not produce a validation error")
	}
}

func TestOldBehaviorWouldFail(t *testing.T) {
	t.Run("OLD behavior: null teams overwritten to empty list (the bug)", func(t *testing.T) {
		// Simulate old readUser behavior:
		// teams starts as null (user didn't specify)
		_ = types.ListNull(types.StringType)

		// Old code: unconditionally sets from API response
		apiTeams := []interface{}{} // API returns empty array
		// Old code was:
		//   if teams, ok := userInfo["teams"].([]interface{}); ok {
		//     teamsList := make([]attr.Value, len(teams))
		//     ...
		//     data.Teams, _ = types.ListValue(types.StringType, teamsList)
		//   }
		// This would succeed because []interface{}{} type-asserts to []interface{} fine.
		teamsList := make([]attr.Value, len(apiTeams))
		_ = teamsList
		oldBehaviorResult, _ := types.ListValue(types.StringType, teamsList)

		// This IS the bug: null was overwritten to []
		if oldBehaviorResult.IsNull() {
			t.Error("old behavior should have created empty list (not null) - this test validates the bug existed")
		}
		// Confirm it was overwritten
		if len(oldBehaviorResult.Elements()) != 0 {
			t.Errorf("old behavior created list with %d elements, expected 0", len(oldBehaviorResult.Elements()))
		}
		// This empty list != null, which is what Terraform would detect as inconsistent
	})
}
