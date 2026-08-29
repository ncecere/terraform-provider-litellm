package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func teamMemberTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&TeamMemberResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func teamMemberTestState(t *testing.T, schema resourceschema.Schema, data TeamMemberResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set state: %v", diagnostics)
	}
	return state
}

func teamMemberTestPlan(t *testing.T, schema resourceschema.Schema, data TeamMemberResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set plan: %v", diagnostics)
	}
	return plan
}

func decodeTeamMemberState(t *testing.T, state tfsdk.State) TeamMemberResourceModel {
	t.Helper()
	var data TeamMemberResourceModel
	if diagnostics := state.Get(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("decode state: %v", diagnostics)
	}
	return data
}

func teamMemberClient(server *httptest.Server) *Client {
	return &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
}

type teamMemberTestRoster struct {
	ID    string
	Email string
	Role  string
}

type teamMemberTestMembership struct {
	ID       string
	BudgetID string
	Max      *float64
	Duration *string
	ResetAt  string
}

func float64TestPointer(value float64) *float64 { return &value }
func stringTestPointer(value string) *string    { return &value }

func writeTeamMemberSnapshot(writer http.ResponseWriter, teamID string, roster []teamMemberTestRoster, memberships []teamMemberTestMembership, teamBudgetID string) {
	members := make([]interface{}, 0, len(roster))
	for _, member := range roster {
		members = append(members, map[string]interface{}{"user_id": member.ID, "user_email": member.Email, "role": member.Role})
	}
	rows := make([]interface{}, 0, len(memberships))
	for _, membership := range memberships {
		row := map[string]interface{}{"user_id": membership.ID, "team_id": teamID}
		if membership.BudgetID == "" {
			row["budget_id"] = nil
			row["litellm_budget_table"] = nil
		} else {
			row["budget_id"] = membership.BudgetID
			budget := map[string]interface{}{"budget_id": membership.BudgetID, "max_budget": membership.Max}
			if membership.ResetAt != "" {
				budget["budget_reset_at"] = membership.ResetAt
			}
			if membership.Duration != nil {
				budget["budget_duration"] = *membership.Duration
			} else {
				budget["budget_duration"] = nil
			}
			row["litellm_budget_table"] = budget
		}
		rows = append(rows, row)
	}
	metadata := map[string]interface{}{}
	if teamBudgetID != "" {
		metadata["team_member_budget_id"] = teamBudgetID
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{
		"team_id": teamID,
		"team_info": map[string]interface{}{
			"team_id": teamID, "metadata": metadata, "members_with_roles": members,
		},
		"team_memberships": rows,
	})
}

func writeUserList(writer http.ResponseWriter, users ...map[string]interface{}) {
	if users == nil {
		users = []map[string]interface{}{}
	}
	totalPages := 1
	if len(users) == 0 {
		totalPages = 0
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{
		"users": users, "total": len(users), "page": 1, "page_size": 100, "total_pages": totalPages,
	})
}

func TestTeamMemberSchemaPreservesContractAndUsesAtLeastOneIdentity(t *testing.T) {
	t.Parallel()
	schema := teamMemberTestSchema(t)
	userID, ok := schema.Attributes["user_id"].(resourceschema.StringAttribute)
	if !ok || !userID.Optional || !userID.Computed || userID.Required {
		t.Fatalf("user_id schema = %#v", schema.Attributes["user_id"])
	}
	userEmail, ok := schema.Attributes["user_email"].(resourceschema.StringAttribute)
	if !ok || !userEmail.Optional || userEmail.Computed || userEmail.Required {
		t.Fatalf("user_email schema = %#v", schema.Attributes["user_email"])
	}
	teamID := schema.Attributes["team_id"].(resourceschema.StringAttribute)
	if len(teamID.PlanModifiers) == 0 || len(userID.PlanModifiers) < 2 {
		t.Fatalf("immutable identity plan modifiers missing: team=%#v user=%#v", teamID.PlanModifiers, userID.PlanModifiers)
	}

	validators := (&TeamMemberResource{}).ConfigValidators(context.Background())
	if len(validators) != 1 {
		t.Fatalf("config validators = %d, want one at-least-one validator", len(validators))
	}
}

func TestTeamMemberMutationRequestsUseStoredIdentityAndNullableBudgetContract(t *testing.T) {
	t.Parallel()
	state := &TeamMemberResourceModel{
		TeamID: types.StringValue("stored-team"), UserID: types.StringValue("stored-user"),
		UserEmail: types.StringValue("Stored@Example.com"), Role: types.StringValue("user"),
		MaxBudgetInTeam: types.Float64Value(50), BudgetDuration: types.StringValue("30d"),
	}
	plan := &TeamMemberResourceModel{
		TeamID: types.StringValue("planned-team"), UserID: types.StringValue("planned-user"),
		UserEmail: types.StringValue("planned@example.com"), Role: types.StringValue("admin"),
		MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	}
	request, err := buildTeamMemberUpdateRequest(state, plan)
	if err != nil {
		t.Fatalf("build update: %v", err)
	}
	if request["team_id"] != "stored-team" || request["user_id"] != "stored-user" {
		t.Fatalf("update used plan identity: %#v", request)
	}
	if _, exists := request["user_email"]; exists {
		t.Fatalf("update sent mutable email identity: %#v", request)
	}
	if request["max_budget_in_team"] != nil || request["budget_duration"] != nil {
		t.Fatalf("nullable clears = %#v", request)
	}
	deleteRequest, err := buildTeamMemberDeleteRequest(state)
	if err != nil || deleteRequest["team_id"] != "stored-team" || deleteRequest["user_id"] != "stored-user" || len(deleteRequest) != 2 {
		t.Fatalf("delete request = %#v err=%v", deleteRequest, err)
	}

	emailOnly := &TeamMemberResourceModel{
		TeamID: types.StringValue("team"), UserID: types.StringUnknown(), UserEmail: types.StringValue("User@Example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	}
	add, err := buildTeamMemberAddRequest(emailOnly)
	if err != nil {
		t.Fatalf("build email-only add: %v", err)
	}
	member := add["member"].([]map[string]interface{})[0]
	if member["user_email"] != "User@Example.com" {
		t.Fatalf("email-only request = %#v", member)
	}
	if _, exists := member["user_id"]; exists {
		t.Fatalf("email-only request unexpectedly included user_id: %#v", member)
	}
}

func TestTeamMemberReadUsesCanonicalIDPreservesEmailCaseAndReadsNativeBudget(t *testing.T) {
	t.Parallel()
	const teamID = "team/group #1&ops"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/team/info" || request.URL.Query().Get("team_id") != teamID {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		writeTeamMemberSnapshot(writer, teamID,
			[]teamMemberTestRoster{{ID: "canonical-user", Email: "owner@example.com", Role: "admin"}},
			[]teamMemberTestMembership{{ID: "canonical-user", BudgetID: "budget-1", Max: float64TestPointer(75), Duration: stringTestPointer("7d")}}, "default-budget")
	}))
	defer server.Close()

	schema := teamMemberTestSchema(t)
	state := teamMemberTestState(t, schema, TeamMemberResourceModel{
		ID: types.StringValue(teamID + ":canonical-user"), TeamID: types.StringValue(teamID), UserID: types.StringValue("canonical-user"),
		UserEmail: types.StringValue("Owner@Example.COM"), Role: types.StringValue("user"),
		MaxBudgetInTeam: types.Float64Value(50), BudgetDuration: types.StringValue("30d"),
	})
	response := &resource.ReadResponse{State: state}
	(&TeamMemberResource{client: teamMemberClient(server)}).Read(context.Background(), resource.ReadRequest{State: state}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	read := decodeTeamMemberState(t, response.State)
	if read.ID.ValueString() != teamID+":canonical-user" || read.UserID.ValueString() != "canonical-user" || read.UserEmail.ValueString() != "Owner@Example.COM" {
		t.Fatalf("identity state = %#v", read)
	}
	if read.Role.ValueString() != "admin" || read.MaxBudgetInTeam.ValueFloat64() != 75 || read.BudgetDuration.ValueString() != "7d" {
		t.Fatalf("authoritative state = %#v", read)
	}
}

func TestTeamMemberReadResolvesHistoricalEmailIdentityAndRejectsConflicts(t *testing.T) {
	t.Parallel()
	t.Run("email-only historical state", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeTeamMemberSnapshot(writer, "team-1", []teamMemberTestRoster{{ID: "canonical", Email: "member@example.com", Role: "user"}}, []teamMemberTestMembership{{ID: "canonical"}}, "")
		}))
		defer server.Close()
		schema := teamMemberTestSchema(t)
		state := teamMemberTestState(t, schema, TeamMemberResourceModel{
			ID: types.StringNull(), TeamID: types.StringValue("team-1"), UserID: types.StringNull(), UserEmail: types.StringValue("MEMBER@example.COM"),
			Role: types.StringNull(), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
		})
		response := &resource.ReadResponse{State: state}
		(&TeamMemberResource{client: teamMemberClient(server)}).Read(context.Background(), resource.ReadRequest{State: state}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", response.Diagnostics)
		}
		read := decodeTeamMemberState(t, response.State)
		if read.UserID.ValueString() != "canonical" || read.ID.ValueString() != "team-1:canonical" || read.UserEmail.ValueString() != "MEMBER@example.COM" {
			t.Fatalf("hydrated state = %#v", read)
		}
	})

	t.Run("both identities identify peers", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeTeamMemberSnapshot(writer, "team-1",
				[]teamMemberTestRoster{{ID: "user-a", Email: "a@example.com", Role: "user"}, {ID: "user-b", Email: "b@example.com", Role: "user"}},
				[]teamMemberTestMembership{{ID: "user-a"}, {ID: "user-b"}}, "")
		}))
		defer server.Close()
		schema := teamMemberTestSchema(t)
		state := teamMemberTestState(t, schema, TeamMemberResourceModel{
			ID: types.StringValue("team-1:user-a"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-a"), UserEmail: types.StringValue("b@example.com"),
			Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
		})
		response := &resource.ReadResponse{State: state}
		(&TeamMemberResource{client: teamMemberClient(server)}).Read(context.Background(), resource.ReadRequest{State: state}, response)
		if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
			t.Fatalf("conflicting identity diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
		}
	})

	t.Run("configured email no longer matches canonical roster alias", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeTeamMemberSnapshot(writer, "team-1", []teamMemberTestRoster{{ID: "user-a", Email: "new-alias@example.com", Role: "user"}}, []teamMemberTestMembership{{ID: "user-a"}}, "")
		}))
		defer server.Close()
		schema := teamMemberTestSchema(t)
		state := teamMemberTestState(t, schema, TeamMemberResourceModel{
			ID: types.StringValue("team-1:user-a"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-a"), UserEmail: types.StringValue("configured@example.com"),
			Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
		})
		response := &resource.ReadResponse{State: state}
		(&TeamMemberResource{client: teamMemberClient(server)}).Read(context.Background(), resource.ReadRequest{State: state}, response)
		if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
			t.Fatalf("changed alias diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
		}
		retained := decodeTeamMemberState(t, response.State)
		if retained.UserEmail.ValueString() != "configured@example.com" {
			t.Fatalf("changed remote alias was adopted: %#v", retained)
		}
	})
}

func TestTeamMemberEmailOnlyCreatePersistsCanonicalStableIdentity(t *testing.T) {
	// Stateful handler; do not run in parallel.
	const teamID = "team-1"
	const userID = "canonical-user"
	const configuredEmail = "Member@Example.COM"
	created := false
	var addBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer, map[string]interface{}{"user_id": userID, "user_email": "member@example.com"})
		case "/team/info":
			if !created {
				writeTeamMemberSnapshot(writer, teamID, nil, nil, "")
				return
			}
			writeTeamMemberSnapshot(writer, teamID,
				[]teamMemberTestRoster{{ID: userID, Email: "member@example.com", Role: "admin"}},
				[]teamMemberTestMembership{{ID: userID, BudgetID: "budget-1", Max: float64TestPointer(25), Duration: stringTestPointer("30d")}}, "default-budget")
		case "/team/member_add":
			_ = json.NewDecoder(request.Body).Decode(&addBody)
			created = true
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_id":                  teamID,
				"members_with_roles":       []interface{}{map[string]interface{}{"user_id": userID, "user_email": "member@example.com", "role": "admin"}},
				"updated_users":            []interface{}{map[string]interface{}{"user_id": userID, "user_email": "member@example.com"}},
				"updated_team_memberships": []interface{}{map[string]interface{}{"user_id": userID, "team_id": teamID}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := teamMemberTestSchema(t)
	plan := teamMemberTestPlan(t, schema, TeamMemberResourceModel{
		ID: types.StringUnknown(), TeamID: types.StringValue(teamID), UserID: types.StringUnknown(), UserEmail: types.StringValue(configuredEmail),
		Role: types.StringValue("admin"), MaxBudgetInTeam: types.Float64Value(25), BudgetDuration: types.StringValue("30d"),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&TeamMemberResource{client: teamMemberClient(server)}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}
	createdState := decodeTeamMemberState(t, response.State)
	if createdState.ID.ValueString() != teamID+":"+userID || createdState.UserID.ValueString() != userID || createdState.UserEmail.ValueString() != configuredEmail {
		t.Fatalf("created identity = %#v", createdState)
	}
	member := addBody["member"].([]interface{})[0].(map[string]interface{})
	if member["user_email"] != configuredEmail {
		t.Fatalf("create request = %#v", addBody)
	}
	if _, exists := member["user_id"]; exists {
		t.Fatalf("email-only HCL sent computed canonical ID as plan identity: %#v", addBody)
	}
}

func TestTeamMemberV198BudgetlessLifecycleUsesRosterOnlyAndLaterAddsBudget(t *testing.T) {
	// Stateful endpoint-shape test; do not run in parallel.
	const teamID = "budgetless-team"
	const userID = "new-user"
	const email = "Member@Example.com"
	created := false
	role := "user"
	var max *float64
	var addBody map[string]interface{}
	updateBodies := make([]map[string]interface{}, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer)
		case "/v2/user/info":
			http.NotFound(writer, request)
		case "/team/info":
			if !created {
				writeTeamMemberSnapshot(writer, teamID, nil, nil, "")
				return
			}
			memberships := []teamMemberTestMembership{}
			if max != nil {
				memberships = append(memberships, teamMemberTestMembership{ID: userID, BudgetID: "private-budget", Max: max})
			}
			writeTeamMemberSnapshot(writer, teamID, []teamMemberTestRoster{{ID: userID, Email: email, Role: role}}, memberships, "")
		case "/team/member_add":
			_ = json.NewDecoder(request.Body).Decode(&addBody)
			created = true
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_id": teamID,
				"members_with_roles": []interface{}{
					map[string]interface{}{"user_id": userID, "user_email": email, "role": role},
				},
				// This empty array is the exact v1.98 shape for a member when
				// neither request fields nor team metadata select a budget.
				"updated_users":            []interface{}{map[string]interface{}{"user_id": userID, "user_email": nil}},
				"updated_team_memberships": []interface{}{},
			})
		case "/team/member_update":
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			updateBodies = append(updateBodies, body)
			if value, ok := body["role"].(string); ok {
				role = value
			}
			if value, transmitted := body["max_budget_in_team"]; transmitted {
				if number, ok := value.(float64); ok {
					max = float64TestPointer(number)
				} else if value == nil {
					max = nil
				}
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": teamID, "user_id": userID})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := teamMemberTestSchema(t)
	planModel := TeamMemberResourceModel{
		ID: types.StringUnknown(), TeamID: types.StringValue(teamID), UserID: types.StringValue(userID), UserEmail: types.StringValue(email),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	}
	plan := teamMemberTestPlan(t, schema, planModel)
	resourceUnderTest := &TeamMemberResource{client: teamMemberClient(server)}
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("budgetless create diagnostics: %v", createResponse.Diagnostics)
	}
	if !reflect.DeepEqual(addBody, map[string]interface{}{
		"team_id": teamID,
		"member":  []interface{}{map[string]interface{}{"user_id": userID, "user_email": email, "role": "user"}},
	}) {
		t.Fatalf("budgetless member_add body = %#v", addBody)
	}
	createdState := decodeTeamMemberState(t, createResponse.State)
	if createdState.ID.ValueString() != teamID+":"+userID || createdState.Role.ValueString() != "user" || !createdState.MaxBudgetInTeam.IsNull() || !createdState.BudgetDuration.IsNull() {
		t.Fatalf("budgetless create state = %#v", createdState)
	}

	readResponse := &resource.ReadResponse{State: createResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: createResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("budgetless read diagnostics: %v", readResponse.Diagnostics)
	}

	rolePlanModel := createdState
	rolePlanModel.Role = types.StringValue("admin")
	rolePlan := teamMemberTestPlan(t, schema, rolePlanModel)
	roleResponse := &resource.UpdateResponse{State: createResponse.State}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: createResponse.State, Plan: rolePlan}, roleResponse)
	if roleResponse.Diagnostics.HasError() {
		t.Fatalf("budgetless role update diagnostics: %v", roleResponse.Diagnostics)
	}
	if len(updateBodies) != 1 || !reflect.DeepEqual(updateBodies[0], map[string]interface{}{"team_id": teamID, "user_id": userID, "role": "admin"}) {
		t.Fatalf("role-only member_update bodies = %#v", updateBodies)
	}

	budgetState := decodeTeamMemberState(t, roleResponse.State)
	budgetPlanModel := budgetState
	budgetPlanModel.MaxBudgetInTeam = types.Float64Value(40)
	budgetPlan := teamMemberTestPlan(t, schema, budgetPlanModel)
	budgetResponse := &resource.UpdateResponse{State: roleResponse.State}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: roleResponse.State, Plan: budgetPlan}, budgetResponse)
	if budgetResponse.Diagnostics.HasError() {
		t.Fatalf("add budget diagnostics: %v", budgetResponse.Diagnostics)
	}
	if len(updateBodies) != 2 || !reflect.DeepEqual(updateBodies[1], map[string]interface{}{
		"team_id": teamID, "user_id": userID, "role": "admin", "max_budget_in_team": float64(40),
	}) {
		t.Fatalf("budget-adding member_update bodies = %#v", updateBodies)
	}
	budgeted := decodeTeamMemberState(t, budgetResponse.State)
	if budgeted.MaxBudgetInTeam.ValueFloat64() != 40 || !budgeted.BudgetDuration.IsNull() {
		t.Fatalf("budgeted state = %#v", budgeted)
	}

	clearPlanModel := budgeted
	clearPlanModel.MaxBudgetInTeam = types.Float64Null()
	clearPlan := teamMemberTestPlan(t, schema, clearPlanModel)
	clearResponse := &resource.UpdateResponse{State: budgetResponse.State}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: budgetResponse.State, Plan: clearPlan}, clearResponse)
	if clearResponse.Diagnostics.HasError() {
		t.Fatalf("return to budgetless roster diagnostics: %v", clearResponse.Diagnostics)
	}
	if len(updateBodies) != 3 || !reflect.DeepEqual(updateBodies[2], map[string]interface{}{
		"team_id": teamID, "user_id": userID, "role": "admin", "max_budget_in_team": nil,
	}) {
		t.Fatalf("budget-clearing member_update bodies = %#v", updateBodies)
	}
	cleared := decodeTeamMemberState(t, clearResponse.State)
	if !cleared.MaxBudgetInTeam.IsNull() || !cleared.BudgetDuration.IsNull() || cleared.Role.ValueString() != "admin" {
		t.Fatalf("cleared budgetless state = %#v", cleared)
	}
}

func TestTeamMemberBudgetlessImportAndExpectedMembershipRules(t *testing.T) {
	t.Parallel()
	t.Run("import converges from roster alone", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeTeamMemberSnapshot(writer, "team-1", []teamMemberTestRoster{{ID: "user-1", Email: "member@example.com", Role: "admin"}}, nil, "")
		}))
		defer server.Close()
		schema := teamMemberTestSchema(t)
		nullState := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
		imported := &resource.ImportStateResponse{State: nullState}
		(&TeamMemberResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: "team-1:user-1"}, imported)
		if imported.Diagnostics.HasError() {
			t.Fatalf("import diagnostics: %v", imported.Diagnostics)
		}
		response := &resource.ReadResponse{State: imported.State}
		(&TeamMemberResource{client: teamMemberClient(server)}).Read(context.Background(), resource.ReadRequest{State: imported.State}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("import read diagnostics: %v", response.Diagnostics)
		}
		observed := decodeTeamMemberState(t, response.State)
		if !observed.UserEmail.IsNull() || observed.Role.ValueString() != "admin" || !observed.MaxBudgetInTeam.IsNull() || !observed.BudgetDuration.IsNull() {
			t.Fatalf("imported budgetless state = %#v", observed)
		}
	})

	for name, data := range map[string]struct {
		teamBudget string
		max        types.Float64
		duration   types.String
	}{
		"requested max requires membership":      {max: types.Float64Value(1), duration: types.StringNull()},
		"requested duration requires membership": {max: types.Float64Null(), duration: types.StringValue("7d")},
		"team default requires membership":       {teamBudget: "default-budget", max: types.Float64Null(), duration: types.StringNull()},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := &teamMemberAddSnapshot{Members: []remoteBatchMember{{UserID: "user-1", UserEmail: "member@example.com", Role: "user"}}, TeamBudgetID: data.teamBudget, MembershipsKnown: true}
			model := TeamMemberResourceModel{TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-1"), UserEmail: types.StringValue("member@example.com"), Role: types.StringValue("user"), MaxBudgetInTeam: data.max, BudgetDuration: data.duration}
			observation, err := observeTeamMember(snapshot, &model)
			if err != nil || observation.Status != teamMemberRemoteRosterOnly {
				t.Fatalf("observation status=%v err=%v, want roster-only partial", observation.Status, err)
			}
		})
	}
}

func TestTeamMemberAddResponseMembershipCardinalityFollowsV198BudgetShape(t *testing.T) {
	t.Parallel()
	response := teamMemberAddAPIResponse{
		TeamID:                 "team-1",
		MembersWithRoles:       []teamMemberRosterAPI{{UserID: stringTestPointer("user-1"), Role: "user"}},
		UpdatedUsers:           []teamMemberUserAPI{{UserID: "user-1"}},
		UpdatedTeamMemberships: []teamMemberMembershipAPI{},
	}
	if _, err := validateTeamMemberAddResponseStructure(response, "team-1", false); err != nil {
		t.Fatalf("budgetless updated_team_memberships=[] rejected: %v", err)
	}
	if _, err := validateTeamMemberAddResponseStructure(response, "team-1", true); err == nil {
		t.Fatal("budget-required response accepted updated_team_memberships=[]")
	}
	response.UpdatedTeamMemberships = []teamMemberMembershipAPI{{UserID: "user-1", TeamID: "team-1"}}
	if _, err := validateTeamMemberAddResponseStructure(response, "team-1", true); err != nil {
		t.Fatalf("budget-required canonical membership rejected: %v", err)
	}
}

func TestTeamMemberBothIdentityPreflightTreatsAccountEmailOnlyAsDisproof(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		accountEmail interface{}
		wantError    bool
	}{
		"null account email is inconclusive":       {accountEmail: nil},
		"conflicting account email disproves pair": {accountEmail: "different@example.com", wantError: true},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/user/list":
					writeUserList(writer)
				case "/v2/user/info":
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"user_id": "user-1", "user_email": test.accountEmail})
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()
			model := TeamMemberResourceModel{UserID: types.StringValue("user-1"), UserEmail: types.StringValue("membership@example.com")}
			canonical, err := (&TeamMemberResource{client: teamMemberClient(server)}).resolveConfiguredTeamMemberIdentity(context.Background(), &model)
			if (err != nil) != test.wantError {
				t.Fatalf("resolve canonical=%q err=%v, wantError=%t", canonical, err, test.wantError)
			}
			if !test.wantError && canonical != "user-1" {
				t.Fatalf("resolved canonical=%q, want user-1", canonical)
			}
		})
	}
}

func TestTeamMemberCreateWithBothAcceptsNullUpdatedUserEmail(t *testing.T) {
	t.Parallel()
	const teamID = "team-1"
	const userID = "new-user"
	const email = "membership@example.com"
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer)
		case "/v2/user/info":
			http.NotFound(writer, request)
		case "/team/info":
			if created {
				writeTeamMemberSnapshot(writer, teamID, []teamMemberTestRoster{{ID: userID, Email: email, Role: "user"}}, nil, "")
			} else {
				writeTeamMemberSnapshot(writer, teamID, nil, nil, "")
			}
		case "/team/member_add":
			created = true
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_id":                  teamID,
				"members_with_roles":       []interface{}{map[string]interface{}{"user_id": userID, "user_email": email, "role": "user"}},
				"updated_users":            []interface{}{map[string]interface{}{"user_id": userID, "user_email": nil}},
				"updated_team_memberships": []interface{}{},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	plan := teamMemberTestPlan(t, schema, TeamMemberResourceModel{
		ID: types.StringUnknown(), TeamID: types.StringValue(teamID), UserID: types.StringValue(userID), UserEmail: types.StringValue(email),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&TeamMemberResource{client: teamMemberClient(server)}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("null account-email create diagnostics: %v", response.Diagnostics)
	}
	createdState := decodeTeamMemberState(t, response.State)
	if createdState.UserID.ValueString() != userID || createdState.UserEmail.ValueString() != email {
		t.Fatalf("created identity = %#v", createdState)
	}
}

func TestTeamMemberCreateWithBothRejectsDifferentCanonicalUsersBeforeMutation(t *testing.T) {
	t.Parallel()
	var adds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer, map[string]interface{}{"user_id": "other-user", "user_email": "member@example.com"})
		case "/team/member_add":
			adds.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	plan := teamMemberTestPlan(t, schema, TeamMemberResourceModel{
		ID: types.StringUnknown(), TeamID: types.StringValue("team-1"), UserID: types.StringValue("configured-user"), UserEmail: types.StringValue("member@example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&TeamMemberResource{client: teamMemberClient(server)}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || adds.Load() != 0 {
		t.Fatalf("mismatch diagnostics=%v adds=%d", response.Diagnostics, adds.Load())
	}
}

func TestTeamMemberCreateRefusesUnownedRosterMembership(t *testing.T) {
	t.Parallel()
	var adds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer, map[string]interface{}{"user_id": "existing-user", "user_email": "existing@example.com"})
		case "/team/info":
			writeTeamMemberSnapshot(writer, "team-1", []teamMemberTestRoster{{ID: "existing-user", Email: "Existing@Example.com", Role: "user"}}, []teamMemberTestMembership{{ID: "existing-user"}}, "")
		case "/team/member_add":
			adds.Add(1)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	plan := teamMemberTestPlan(t, schema, TeamMemberResourceModel{
		ID: types.StringUnknown(), TeamID: types.StringValue("team-1"), UserID: types.StringUnknown(), UserEmail: types.StringValue("existing@example.COM"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&TeamMemberResource{client: teamMemberClient(server)}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || adds.Load() != 0 {
		t.Fatalf("unowned preflight diagnostics=%v adds=%d", response.Diagnostics, adds.Load())
	}
}

func TestTeamMemberAcceptedMembershipOnlyCreateRetainsCanonicalOwnershipAndBlocksUnsafeWrites(t *testing.T) {
	// Stateful handler; do not run in parallel.
	const teamID = "team-partial"
	const userID = "canonical-partial"
	orphan := false
	var updateCalls, deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer)
		case "/team/info":
			memberships := []teamMemberTestMembership{}
			if orphan {
				memberships = append(memberships, teamMemberTestMembership{ID: userID, BudgetID: "orphan-budget", Max: float64TestPointer(12), Duration: stringTestPointer("7d")})
			}
			writeTeamMemberSnapshot(writer, teamID, nil, memberships, "default-budget")
		case "/team/member_add":
			orphan = true
			// A successful HTTP mutation with a malformed body exercises Client's
			// accepted=true recovery boundary.
			_, _ = writer.Write([]byte(`{}`))
		case "/team/member_update":
			updateCalls.Add(1)
		case "/team/member_delete":
			deleteCalls.Add(1)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := teamMemberTestSchema(t)
	planModel := TeamMemberResourceModel{
		ID: types.StringUnknown(), TeamID: types.StringValue(teamID), UserID: types.StringUnknown(), UserEmail: types.StringValue("partial@example.com"),
		Role: types.StringValue("admin"), MaxBudgetInTeam: types.Float64Value(12), BudgetDuration: types.StringValue("7d"),
	}
	plan := teamMemberTestPlan(t, schema, planModel)
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest := &TeamMemberResource{client: teamMemberClient(server)}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, createResponse)
	if !createResponse.Diagnostics.HasError() || createResponse.State.Raw.IsNull() {
		t.Fatalf("partial create diagnostics=%v null=%t", createResponse.Diagnostics, createResponse.State.Raw.IsNull())
	}
	partial := decodeTeamMemberState(t, createResponse.State)
	if partial.ID.ValueString() != teamID+":"+userID || partial.UserID.ValueString() != userID || !partial.Role.IsUnknown() {
		t.Fatalf("partial ownership = %#v diagnostics=%v", partial, createResponse.Diagnostics)
	}

	readResponse := &resource.ReadResponse{State: createResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: createResponse.State}, readResponse)
	if !readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() {
		t.Fatalf("orphan read diagnostics=%v", readResponse.Diagnostics)
	}

	updatePlanModel := partial
	updatePlanModel.Role = types.StringValue("user")
	updatePlan := teamMemberTestPlan(t, schema, updatePlanModel)
	updateResponse := &resource.UpdateResponse{State: readResponse.State}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: readResponse.State, Plan: updatePlan}, updateResponse)
	if !updateResponse.Diagnostics.HasError() || updateCalls.Load() != 0 {
		t.Fatalf("unsafe update diagnostics=%v calls=%d", updateResponse.Diagnostics, updateCalls.Load())
	}
	deleteResponse := &resource.DeleteResponse{State: readResponse.State}
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: readResponse.State}, deleteResponse)
	if !deleteResponse.Diagnostics.HasError() || deleteResponse.State.Raw.IsNull() || deleteCalls.Load() != 0 {
		t.Fatalf("unsafe destroy diagnostics=%v null=%t calls=%d", deleteResponse.Diagnostics, deleteResponse.State.Raw.IsNull(), deleteCalls.Load())
	}
}

func TestTeamMemberUpdateUsesStoredIdentityForUnknownEmailAndClearsBudgets(t *testing.T) {
	// Stateful handler; do not run in parallel.
	const teamID = "team-update"
	const userID = "stored-user"
	role := "user"
	max := float64TestPointer(50)
	duration := stringTestPointer("30d")
	var updateBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/team/info":
			writeTeamMemberSnapshot(writer, teamID, []teamMemberTestRoster{{ID: userID, Email: "stored@example.com", Role: role}}, []teamMemberTestMembership{{ID: userID, BudgetID: "default-budget", Max: max, Duration: duration}}, "default-budget")
		case "/team/member_update":
			_ = json.NewDecoder(request.Body).Decode(&updateBody)
			role = "admin"
			max = nil
			duration = nil
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": teamID, "user_id": userID, "max_budget_in_team": nil, "budget_duration": nil})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	stateModel := TeamMemberResourceModel{
		ID: types.StringValue(teamID + ":" + userID), TeamID: types.StringValue(teamID), UserID: types.StringValue(userID), UserEmail: types.StringValue("Stored@Example.COM"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Value(50), BudgetDuration: types.StringValue("30d"),
	}
	planModel := stateModel
	planModel.UserEmail = types.StringUnknown()
	planModel.Role = types.StringValue("admin")
	planModel.MaxBudgetInTeam = types.Float64Null()
	planModel.BudgetDuration = types.StringNull()
	state := teamMemberTestState(t, schema, stateModel)
	plan := teamMemberTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&TeamMemberResource{client: teamMemberClient(server)}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if updateBody["team_id"] != teamID || updateBody["user_id"] != userID || updateBody["max_budget_in_team"] != nil || updateBody["budget_duration"] != nil {
		t.Fatalf("update body = %#v", updateBody)
	}
	if _, exists := updateBody["user_email"]; exists {
		t.Fatalf("update used plan email identity: %#v", updateBody)
	}
	updated := decodeTeamMemberState(t, response.State)
	if updated.UserEmail.ValueString() != "Stored@Example.COM" || updated.Role.ValueString() != "admin" || !updated.MaxBudgetInTeam.IsNull() || !updated.BudgetDuration.IsNull() {
		t.Fatalf("updated state = %#v", updated)
	}
}

func TestTeamMemberEmailAliasChangeForSameCanonicalUserAvoidsRemoteMutation(t *testing.T) {
	t.Parallel()
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer, map[string]interface{}{"user_id": "user-1", "user_email": "new@example.com"})
		case "/team/info":
			writeTeamMemberSnapshot(writer, "team-1", []teamMemberTestRoster{{ID: "user-1", Email: "new@example.com", Role: "user"}}, []teamMemberTestMembership{{ID: "user-1"}}, "")
		case "/team/member_update", "/team/member_add", "/team/member_delete":
			mutations.Add(1)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	stateModel := TeamMemberResourceModel{
		ID: types.StringValue("team-1:user-1"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-1"), UserEmail: types.StringValue("old@example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	}
	planModel := stateModel
	planModel.UserEmail = types.StringValue("New@Example.COM")
	state := teamMemberTestState(t, schema, stateModel)
	plan := teamMemberTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&TeamMemberResource{client: teamMemberClient(server)}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if response.Diagnostics.HasError() || mutations.Load() != 0 {
		t.Fatalf("alias update diagnostics=%v mutations=%d", response.Diagnostics, mutations.Load())
	}
	updated := decodeTeamMemberState(t, response.State)
	if updated.UserID.ValueString() != "user-1" || updated.ID.ValueString() != "team-1:user-1" || updated.UserEmail.ValueString() != "New@Example.COM" {
		t.Fatalf("alias update state = %#v", updated)
	}
}

func TestTeamMemberUpdateRequiresAuthoritativeNullClear(t *testing.T) {
	// Stateful handler; do not run in parallel.
	const teamID = "team-clear"
	const userID = "user-clear"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/team/info":
			// Simulate v1.98 accepting but ignoring both null clears.
			writeTeamMemberSnapshot(writer, teamID,
				[]teamMemberTestRoster{{ID: userID, Email: "clear@example.com", Role: "user"}},
				[]teamMemberTestMembership{{ID: userID, BudgetID: "default-budget", Max: float64TestPointer(50), Duration: stringTestPointer("30d")}}, "default-budget")
		case "/team/member_update":
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": teamID, "user_id": userID})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	stateModel := TeamMemberResourceModel{
		ID: types.StringValue(teamID + ":" + userID), TeamID: types.StringValue(teamID), UserID: types.StringValue(userID), UserEmail: types.StringValue("clear@example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Value(50), BudgetDuration: types.StringValue("30d"),
	}
	planModel := stateModel
	planModel.MaxBudgetInTeam = types.Float64Null()
	planModel.BudgetDuration = types.StringNull()
	state := teamMemberTestState(t, schema, stateModel)
	plan := teamMemberTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&TeamMemberResource{client: teamMemberClient(server)}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("ignored clear diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
	retained := decodeTeamMemberState(t, response.State)
	if retained.MaxBudgetInTeam.IsNull() || retained.MaxBudgetInTeam.ValueFloat64() != 50 || retained.BudgetDuration.IsNull() || retained.BudgetDuration.ValueString() != "30d" {
		t.Fatalf("ignored clear did not retain authoritative values: %#v", retained)
	}
}

func TestTeamMemberUpdateRejectsUnknownCanonicalIdentityWithoutRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	stateModel := TeamMemberResourceModel{
		ID: types.StringValue("team-1:user-1"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-1"), UserEmail: types.StringValue("member@example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	}
	planModel := stateModel
	planModel.UserID = types.StringUnknown()
	state := teamMemberTestState(t, schema, stateModel)
	plan := teamMemberTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&TeamMemberResource{client: teamMemberClient(server)}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if !response.Diagnostics.HasError() || calls.Load() != 0 || response.State.Raw.IsNull() {
		t.Fatalf("unknown identity diagnostics=%v calls=%d null=%t", response.Diagnostics, calls.Load(), response.State.Raw.IsNull())
	}
}

func TestTeamMemberBudgetUpdateRefusesSharedHistoricalRow(t *testing.T) {
	t.Parallel()
	var updateCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/team/info":
			writeTeamMemberSnapshot(writer, "team-1",
				[]teamMemberTestRoster{{ID: "owned", Email: "owned@example.com", Role: "user"}, {ID: "peer", Email: "peer@example.com", Role: "user"}},
				[]teamMemberTestMembership{{ID: "owned", BudgetID: "historical", Max: float64TestPointer(10)}, {ID: "peer", BudgetID: "historical", Max: float64TestPointer(10)}}, "current-default")
		case "/team/member_update":
			updateCalls.Add(1)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	stateModel := TeamMemberResourceModel{
		ID: types.StringValue("team-1:owned"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("owned"), UserEmail: types.StringValue("owned@example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Value(10), BudgetDuration: types.StringNull(),
	}
	planModel := stateModel
	planModel.MaxBudgetInTeam = types.Float64Value(20)
	state := teamMemberTestState(t, schema, stateModel)
	plan := teamMemberTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&TeamMemberResource{client: teamMemberClient(server)}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if !response.Diagnostics.HasError() || updateCalls.Load() != 0 || response.State.Raw.IsNull() {
		t.Fatalf("shared budget diagnostics=%v calls=%d null=%t", response.Diagnostics, updateCalls.Load(), response.State.Raw.IsNull())
	}
}

func TestTeamMemberRoleOnlyUpdateOmitsSharedHistoricalBudgetFieldsAndReset(t *testing.T) {
	// Stateful request-capture test; do not run in parallel.
	const originalPeerReset = "2026-09-01T00:00:00Z"
	role := "user"
	peerReset := originalPeerReset
	var updateBodies []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/team/info":
			writeTeamMemberSnapshot(writer, "team-1",
				[]teamMemberTestRoster{{ID: "owned", Email: "owned@example.com", Role: role}, {ID: "peer", Email: "peer@example.com", Role: "user"}},
				[]teamMemberTestMembership{
					{ID: "owned", BudgetID: "historical", Max: float64TestPointer(10), Duration: stringTestPointer("30d"), ResetAt: "2026-09-01T00:00:00Z"},
					{ID: "peer", BudgetID: "historical", Max: float64TestPointer(10), Duration: stringTestPointer("30d"), ResetAt: peerReset},
				}, "current-default")
		case "/team/member_update":
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			updateBodies = append(updateBodies, body)
			if _, transmitted := body["max_budget_in_team"]; transmitted {
				peerReset = "unexpected-reset"
			}
			if _, transmitted := body["budget_duration"]; transmitted {
				peerReset = "unexpected-reset"
			}
			role = body["role"].(string)
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": "team-1", "user_id": "owned"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := teamMemberTestSchema(t)
	stateModel := TeamMemberResourceModel{
		ID: types.StringValue("team-1:owned"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("owned"), UserEmail: types.StringValue("owned@example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Value(10), BudgetDuration: types.StringValue("30d"),
	}
	rolePlanModel := stateModel
	rolePlanModel.Role = types.StringValue("admin")
	state := teamMemberTestState(t, schema, stateModel)
	rolePlan := teamMemberTestPlan(t, schema, rolePlanModel)
	resourceUnderTest := &TeamMemberResource{client: teamMemberClient(server)}
	roleResponse := &resource.UpdateResponse{State: state}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: state, Plan: rolePlan}, roleResponse)
	if roleResponse.Diagnostics.HasError() {
		t.Fatalf("role-only update diagnostics: %v", roleResponse.Diagnostics)
	}
	wantRoleBody := map[string]interface{}{"team_id": "team-1", "user_id": "owned", "role": "admin"}
	if len(updateBodies) != 1 || !reflect.DeepEqual(updateBodies[0], wantRoleBody) {
		t.Fatalf("role-only body = %#v, want %#v", updateBodies, wantRoleBody)
	}
	if peerReset != originalPeerReset {
		t.Fatalf("role-only update changed peer budget_reset_at to %q", peerReset)
	}

	// Duration is independently budget-bearing. Even though max is unchanged,
	// transmitting duration against this shared historical row must be refused.
	updatedState := decodeTeamMemberState(t, roleResponse.State)
	durationPlanModel := updatedState
	durationPlanModel.BudgetDuration = types.StringValue("7d")
	durationPlan := teamMemberTestPlan(t, schema, durationPlanModel)
	durationResponse := &resource.UpdateResponse{State: roleResponse.State}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: roleResponse.State, Plan: durationPlan}, durationResponse)
	if !durationResponse.Diagnostics.HasError() || len(updateBodies) != 1 {
		t.Fatalf("shared duration diagnostics=%v bodies=%#v", durationResponse.Diagnostics, updateBodies)
	}
	if peerReset != originalPeerReset {
		t.Fatalf("refused duration update changed peer budget_reset_at to %q", peerReset)
	}
}

func TestTeamMemberDeleteRetainsStateOnPartialAndDoesNotLeakHTTPDetail(t *testing.T) {
	// Stateful handler; do not run in parallel.
	rosterExists := true
	membershipExists := true
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/team/info":
			roster := []teamMemberTestRoster{}
			memberships := []teamMemberTestMembership{}
			if rosterExists {
				roster = append(roster, teamMemberTestRoster{ID: "user-1", Email: "secret-person@example.com", Role: "user"})
			}
			if membershipExists {
				memberships = append(memberships, teamMemberTestMembership{ID: "user-1"})
			}
			writeTeamMemberSnapshot(writer, "team-1", roster, memberships, "")
		case "/team/member_delete":
			rosterExists = false
			writer.Header().Set("X-Request-ID", "safe-request-123")
			http.Error(writer, `{"error":"secret-person@example.com raw-secret"}`, http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	model := TeamMemberResourceModel{
		ID: types.StringValue("team-1:user-1"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-1"), UserEmail: types.StringValue("secret-person@example.com"),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	}
	state := teamMemberTestState(t, schema, model)
	response := &resource.DeleteResponse{State: state}
	(&TeamMemberResource{client: teamMemberClient(server)}).Delete(context.Background(), resource.DeleteRequest{State: state}, response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("partial destroy diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
	text := fmt.Sprint(response.Diagnostics)
	for _, forbidden := range []string{"secret-person@example.com", "raw-secret", server.URL} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("diagnostic leaked %q: %s", forbidden, text)
		}
	}
}

func TestTeamMemberReadUsesExactHTTPStatusForRemoteDeletion(t *testing.T) {
	t.Parallel()
	schema := teamMemberTestSchema(t)
	model := TeamMemberResourceModel{
		ID: types.StringValue("team-1:user-1"), TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-1"), UserEmail: types.StringNull(),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	}
	for name, test := range map[string]struct {
		status      int
		body        string
		wantRemoved bool
	}{
		"exact 404 removes":               {status: http.StatusNotFound, body: `{"error":"gone"}`, wantRemoved: true},
		"500 body mentioning 404 retains": {status: http.StatusInternalServerError, body: `{"error":"upstream said 404"}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				http.Error(writer, test.body, test.status)
			}))
			defer server.Close()
			state := teamMemberTestState(t, schema, model)
			response := &resource.ReadResponse{State: state}
			(&TeamMemberResource{client: teamMemberClient(server)}).Read(context.Background(), resource.ReadRequest{State: state}, response)
			if response.State.Raw.IsNull() != test.wantRemoved {
				t.Fatalf("removed=%t diagnostics=%v", response.State.Raw.IsNull(), response.Diagnostics)
			}
			if !test.wantRemoved && !response.Diagnostics.HasError() {
				t.Fatal("non-404 failure must retain state and diagnose")
			}
		})
	}
}

func TestTeamMemberImportPreservesHistoricalGrammarAndAddsUnambiguousVersionedForm(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]struct {
		importID string
		teamID   string
		userID   string
	}{
		"historical":                {importID: "team-1:user:with:colons", teamID: "team-1", userID: "user:with:colons"},
		"historical v1 team prefix": {importID: "v1.production:user-1", teamID: "v1.production", userID: "user-1"},
	} {
		t.Run(name, func(t *testing.T) {
			teamID, userID, err := parseTeamMemberImportID(input.importID)
			if err != nil || teamID != input.teamID || userID != input.userID {
				t.Fatalf("parse(%q) = %q,%q,%v", input.importID, teamID, userID, err)
			}
		})
	}
	versioned, err := formatTeamMemberImportID("team:with:colon", "user:with:colon")
	if err != nil {
		t.Fatalf("format versioned import: %v", err)
	}
	teamID, userID, err := parseTeamMemberImportID(versioned)
	if err != nil || teamID != "team:with:colon" || userID != "user:with:colon" {
		t.Fatalf("versioned round trip = %q,%q,%v (%q)", teamID, userID, err, versioned)
	}
	for _, malformed := range []string{"", "team-only", "team:", ":user", "v1.invalid"} {
		if _, _, err := parseTeamMemberImportID(malformed); err == nil {
			t.Fatalf("malformed import %q accepted", malformed)
		}
	}

	schema := teamMemberTestSchema(t)
	nullState := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	response := &resource.ImportStateResponse{State: nullState}
	(&TeamMemberResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: versioned}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	imported := decodeTeamMemberState(t, response.State)
	if imported.ID.ValueString() != "team:with:colon:user:with:colon" || imported.TeamID.ValueString() != "team:with:colon" || imported.UserID.ValueString() != "user:with:colon" {
		t.Fatalf("imported state = %#v", imported)
	}
}

func teamMemberProtocolValue(schemaType tftypes.Type, id, teamID, userID, userEmail interface{}) tftypes.Value {
	return tftypes.NewValue(schemaType, map[string]tftypes.Value{
		"id":                 tftypes.NewValue(tftypes.String, id),
		"team_id":            tftypes.NewValue(tftypes.String, teamID),
		"user_id":            tftypes.NewValue(tftypes.String, userID),
		"user_email":         tftypes.NewValue(tftypes.String, userEmail),
		"role":               tftypes.NewValue(tftypes.String, "user"),
		"max_budget_in_team": tftypes.NewValue(tftypes.Number, nil),
		"budget_duration":    tftypes.NewValue(tftypes.String, nil),
	})
}

func teamMemberProtocolDynamicValue(t *testing.T, schemaType tftypes.Type, value tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	dynamic, err := tfprotov6.NewDynamicValue(schemaType, value)
	if err != nil {
		t.Fatalf("dynamic value: %v", err)
	}
	return &dynamic
}

func TestTeamMemberIDOnlyProtocolLifecycleKeepsEmailUnmanaged(t *testing.T) {
	// Stateful protocol lifecycle; do not run in parallel.
	const teamID = "team-id-only"
	const userID = "user-id-only"
	var created atomic.Bool
	var addCalls atomic.Int32
	var addSentEmail atomic.Bool
	var updateCalls atomic.Int32
	var remoteEmail atomic.Value
	remoteEmail.Store("initial@example.com")

	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/team/info":
			if !created.Load() {
				writeTeamMemberSnapshot(writer, teamID, nil, nil, "")
				return
			}
			writeTeamMemberSnapshot(writer, teamID, []teamMemberTestRoster{{ID: userID, Email: remoteEmail.Load().(string), Role: "user"}}, nil, "")
		case request.Method == http.MethodPost && request.URL.Path == "/team/member_add":
			addCalls.Add(1)
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "invalid request", http.StatusBadRequest)
				return
			}
			if members, ok := body["member"].([]interface{}); ok && len(members) == 1 {
				if member, ok := members[0].(map[string]interface{}); ok {
					_, sent := member["user_email"]
					addSentEmail.Store(sent)
				}
			}
			created.Store(true)
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_id": teamID,
				"members_with_roles": []interface{}{
					map[string]interface{}{"user_id": userID, "user_email": remoteEmail.Load().(string), "role": "user"},
				},
				"updated_users":            []interface{}{map[string]interface{}{"user_id": userID, "user_email": remoteEmail.Load().(string)}},
				"updated_team_memberships": []interface{}{},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/team/member_update":
			updateCalls.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": teamID, "user_id": userID})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer api.Close()

	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get provider schema: err=%v diagnostics=%v", err, schemaResponse.Diagnostics)
	}
	providerType := schemaResponse.Provider.ValueType()
	providerConfig := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"api_base": tftypes.NewValue(tftypes.String, api.URL), "api_key": tftypes.NewValue(tftypes.String, "admin"),
		"insecure_skip_verify": tftypes.NewValue(tftypes.Bool, false), "litellm_changed_by": tftypes.NewValue(tftypes.String, nil),
	})
	configured, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "test", Config: teamMemberProtocolDynamicValue(t, providerType, providerConfig),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configured.Diagnostics) {
		t.Fatalf("configure provider: err=%v diagnostics=%v", err, configured.Diagnostics)
	}
	resourceSchema := schemaResponse.ResourceSchemas["litellm_team_member"]
	if resourceSchema == nil || resourceSchema.Version != 0 {
		t.Fatalf("historical resource schema = %#v", resourceSchema)
	}
	resourceType := resourceSchema.ValueType()
	nullState := teamMemberProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))
	config := teamMemberProtocolDynamicValue(t, resourceType, teamMemberProtocolValue(resourceType, nil, teamID, userID, nil))
	proposed := teamMemberProtocolDynamicValue(t, resourceType, teamMemberProtocolValue(resourceType, tftypes.UnknownValue, teamID, userID, nil))

	assertNullEmail := func(label string, dynamic *tfprotov6.DynamicValue) map[string]tftypes.Value {
		t.Helper()
		if dynamic == nil {
			t.Fatalf("%s state is nil", label)
		}
		value, decodeErr := dynamic.Unmarshal(resourceType)
		if decodeErr != nil {
			t.Fatalf("decode %s state: %v", label, decodeErr)
		}
		var fields map[string]tftypes.Value
		if decodeErr := value.As(&fields); decodeErr != nil {
			t.Fatalf("decode %s fields: %v", label, decodeErr)
		}
		if email := fields["user_email"]; !email.IsKnown() || !email.IsNull() {
			t.Fatalf("%s user_email = %s, want known null", label, email)
		}
		return fields
	}

	createPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member", Config: config, PriorState: nullState, ProposedNewState: proposed,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createPlan.Diagnostics) {
		t.Fatalf("plan ID-only create: err=%v diagnostics=%v", err, createPlan.Diagnostics)
	}
	assertNullEmail("create plan", createPlan.PlannedState)
	createdState, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_team_member", Config: config, PriorState: nullState,
		PlannedState: createPlan.PlannedState, PlannedPrivate: createPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createdState.Diagnostics) {
		t.Fatalf("apply ID-only create: err=%v diagnostics=%v", err, createdState.Diagnostics)
	}
	createdFields := assertNullEmail("created", createdState.NewState)
	var createdID string
	if err := createdFields["id"].As(&createdID); err != nil || createdID != teamID+":"+userID {
		t.Fatalf("created ID = %q, err=%v", createdID, err)
	}

	// The account/roster email changes outside Terraform. ID-only state must not
	// adopt it or attempt to manage it.
	remoteEmail.Store("mutated@example.com")
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member", CurrentState: createdState.NewState, Private: createdState.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("read after remote email mutation: err=%v diagnostics=%v", err, refreshed.Diagnostics)
	}
	assertNullEmail("refreshed", refreshed.NewState)

	noDrift, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member", Config: config, PriorState: refreshed.NewState,
		ProposedNewState: refreshed.NewState, PriorPrivate: refreshed.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(noDrift.Diagnostics) || len(noDrift.RequiresReplace) != 0 {
		t.Fatalf("plan after remote email mutation: err=%v diagnostics=%v replace=%v", err, noDrift.Diagnostics, noDrift.RequiresReplace)
	}
	assertNullEmail("no-drift plan", noDrift.PlannedState)

	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{
		TypeName: "litellm_team_member", ID: teamID + ":" + userID,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import ID-only member: err=%v diagnostics=%v resources=%d", err, imported.Diagnostics, len(imported.ImportedResources))
	}
	assertNullEmail("imported", imported.ImportedResources[0].State)
	importRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importRead.Diagnostics) {
		t.Fatalf("read ID-only import: err=%v diagnostics=%v", err, importRead.Diagnostics)
	}
	assertNullEmail("import refresh", importRead.NewState)
	if addCalls.Load() != 1 || addSentEmail.Load() || updateCalls.Load() != 0 {
		t.Fatalf("remote mutations: add=%d add_sent_email=%t update=%d", addCalls.Load(), addSentEmail.Load(), updateCalls.Load())
	}
}

func TestTeamMemberV0BothIdentityRefreshConvergesFromBudgetlessRoster(t *testing.T) {
	t.Parallel()
	const teamID = "team-v0"
	const userID = "user-v0"
	const email = "Legacy@Example.com"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/team/info" {
			http.NotFound(writer, request)
			return
		}
		writeTeamMemberSnapshot(writer, teamID, []teamMemberTestRoster{{ID: userID, Email: "legacy@example.com", Role: "admin"}}, nil, "")
	}))
	defer server.Close()

	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get schema: err=%v diagnostics=%v", err, schemaResponse.Diagnostics)
	}
	providerType := schemaResponse.Provider.ValueType()
	providerConfig := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"api_base": tftypes.NewValue(tftypes.String, server.URL), "api_key": tftypes.NewValue(tftypes.String, "admin"),
		"insecure_skip_verify": tftypes.NewValue(tftypes.Bool, false), "litellm_changed_by": tftypes.NewValue(tftypes.String, nil),
	})
	configured, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "test", Config: teamMemberProtocolDynamicValue(t, providerType, providerConfig),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configured.Diagnostics) {
		t.Fatalf("configure provider: err=%v diagnostics=%v", err, configured.Diagnostics)
	}
	resourceSchema := schemaResponse.ResourceSchemas["litellm_team_member"]
	if resourceSchema == nil || resourceSchema.Version != 0 {
		t.Fatalf("historical resource schema = %#v", resourceSchema)
	}
	resourceType := resourceSchema.ValueType()
	current := teamMemberProtocolValue(resourceType, teamID+":"+userID, teamID, userID, email)
	readResponse, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member", CurrentState: teamMemberProtocolDynamicValue(t, resourceType, current),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(readResponse.Diagnostics) || readResponse.NewState == nil {
		t.Fatalf("v0 both-identity refresh: err=%v diagnostics=%v", err, readResponse.Diagnostics)
	}
	value, err := readResponse.NewState.Unmarshal(resourceType)
	if err != nil {
		t.Fatalf("unmarshal refreshed state: %v", err)
	}
	var fields map[string]tftypes.Value
	if err := value.As(&fields); err != nil {
		t.Fatalf("decode refreshed fields: %v", err)
	}
	var refreshedEmail, refreshedRole string
	if err := fields["user_email"].As(&refreshedEmail); err != nil || refreshedEmail != email {
		t.Fatalf("refreshed email=%q err=%v", refreshedEmail, err)
	}
	if err := fields["role"].As(&refreshedRole); err != nil || refreshedRole != "admin" {
		t.Fatalf("refreshed role=%q err=%v", refreshedRole, err)
	}
	if !fields["max_budget_in_team"].IsNull() || !fields["budget_duration"].IsNull() {
		t.Fatalf("budgetless v0 refresh adopted budget values: %#v", fields)
	}
}

func TestTeamMemberAcceptedMalformedCreateRetainsUncertainIdentityThroughProtocol(t *testing.T) {
	// Stateful protocol sequence; do not run in parallel.
	const teamID = "team-uncertain"
	const userID = "user-uncertain"
	const email = "Member@Example.com"
	var phase atomic.Int32
	var deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/list":
			writeUserList(writer, map[string]interface{}{"user_id": userID, "user_email": "member@example.com"})
		case "/team/info":
			if phase.Load() < 2 {
				writeTeamMemberSnapshot(writer, teamID, nil, nil, "")
				return
			}
			writeTeamMemberSnapshot(writer, teamID,
				[]teamMemberTestRoster{{ID: userID, Email: "member@example.com", Role: "admin"}},
				[]teamMemberTestMembership{{ID: userID, BudgetID: "private-budget", Max: float64TestPointer(25), Duration: stringTestPointer("30d")}}, "")
		case "/team/member_add":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"team_id":`))
		case "/team/member_delete":
			deleteCalls.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get schema: err=%v diagnostics=%v", err, schemaResponse.Diagnostics)
	}
	providerType := schemaResponse.Provider.ValueType()
	providerConfig := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"api_base": tftypes.NewValue(tftypes.String, server.URL), "api_key": tftypes.NewValue(tftypes.String, "admin"),
		"insecure_skip_verify": tftypes.NewValue(tftypes.Bool, false), "litellm_changed_by": tftypes.NewValue(tftypes.String, nil),
	})
	configured, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "test", Config: teamMemberProtocolDynamicValue(t, providerType, providerConfig),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configured.Diagnostics) {
		t.Fatalf("configure provider: err=%v diagnostics=%v", err, configured.Diagnostics)
	}
	resourceType := schemaResponse.ResourceSchemas["litellm_team_member"].ValueType()
	resourceValue := func(id interface{}, role interface{}, max interface{}, duration interface{}) tftypes.Value {
		return tftypes.NewValue(resourceType, map[string]tftypes.Value{
			"id": tftypes.NewValue(tftypes.String, id), "team_id": tftypes.NewValue(tftypes.String, teamID),
			"user_id": tftypes.NewValue(tftypes.String, userID), "user_email": tftypes.NewValue(tftypes.String, email),
			"role": tftypes.NewValue(tftypes.String, role), "max_budget_in_team": tftypes.NewValue(tftypes.Number, max),
			"budget_duration": tftypes.NewValue(tftypes.String, duration),
		})
	}
	config := teamMemberProtocolDynamicValue(t, resourceType, resourceValue(nil, "admin", 25, "30d"))
	proposed := teamMemberProtocolDynamicValue(t, resourceType, resourceValue(tftypes.UnknownValue, "admin", 25, "30d"))
	nullState := teamMemberProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member", Config: config, PriorState: nullState, ProposedNewState: proposed,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan create: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_team_member", Config: config, PriorState: nullState,
		PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || applied.NewState == nil || len(applied.Private) == 0 {
		t.Fatalf("accepted malformed create: err=%v diagnostics=%v state_nil=%t private=%q", err, applied.Diagnostics, applied.NewState == nil, applied.Private)
	}
	if !strings.Contains(string(applied.Private), teamMemberUncertainPrivateKey) {
		t.Fatalf("accepted malformed create lacks uncertain marker: %q", applied.Private)
	}
	partialValue, err := applied.NewState.Unmarshal(resourceType)
	if err != nil {
		t.Fatalf("decode partial state: %v", err)
	}
	var partial map[string]tftypes.Value
	if err := partialValue.As(&partial); err != nil {
		t.Fatalf("decode partial fields: %v", err)
	}
	var partialID, partialUserID, partialEmail string
	if err := partial["id"].As(&partialID); err != nil || partialID != teamID+":"+userID {
		t.Fatalf("partial ID=%q err=%v", partialID, err)
	}
	if err := partial["user_id"].As(&partialUserID); err != nil || partialUserID != userID {
		t.Fatalf("partial user ID=%q err=%v", partialUserID, err)
	}
	if err := partial["user_email"].As(&partialEmail); err != nil || partialEmail != email {
		t.Fatalf("partial email=%q err=%v", partialEmail, err)
	}
	for _, name := range []string{"role", "max_budget_in_team", "budget_duration"} {
		if partial[name].IsKnown() {
			t.Fatalf("unconfirmed %s was published as known: %s", name, partial[name])
		}
	}

	phase.Store(1)
	staleRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member", CurrentState: applied.NewState, Private: applied.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(staleRead.Diagnostics) || staleRead.NewState == nil {
		t.Fatalf("stale uncertain read: err=%v diagnostics=%v", err, staleRead.Diagnostics)
	}
	if !strings.Contains(string(staleRead.Private), teamMemberUncertainPrivateKey) {
		t.Fatalf("stale read lost uncertain marker: %q", staleRead.Private)
	}
	staleValue, _ := staleRead.NewState.Unmarshal(resourceType)
	var stale map[string]tftypes.Value
	_ = staleValue.As(&stale)
	for _, name := range []string{"role", "max_budget_in_team", "budget_duration"} {
		if stale[name].IsKnown() {
			t.Fatalf("stale read published unconfirmed %s: %s", name, stale[name])
		}
	}

	destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member", Config: nullState, PriorState: staleRead.NewState,
		ProposedNewState: nullState, PriorPrivate: staleRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
		t.Fatalf("plan uncertain destroy: err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
	}
	blockedDestroy, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_team_member", Config: nullState, PriorState: staleRead.NewState,
		PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedDestroy.Diagnostics) || blockedDestroy.NewState == nil || deleteCalls.Load() != 0 {
		t.Fatalf("blocked uncertain destroy: err=%v diagnostics=%v state_nil=%t delete_calls=%d", err, blockedDestroy.Diagnostics, blockedDestroy.NewState == nil, deleteCalls.Load())
	}

	phase.Store(2)
	converged, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member", CurrentState: staleRead.NewState, Private: staleRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(converged.Diagnostics) || converged.NewState == nil {
		t.Fatalf("converged uncertain read: err=%v diagnostics=%v", err, converged.Diagnostics)
	}
	if strings.Contains(string(converged.Private), teamMemberUncertainPrivateKey) {
		t.Fatalf("converged read retained uncertain marker: %q", converged.Private)
	}
	convergedValue, _ := converged.NewState.Unmarshal(resourceType)
	var final map[string]tftypes.Value
	_ = convergedValue.As(&final)
	var finalRole, finalDuration string
	if err := final["role"].As(&finalRole); err != nil || finalRole != "admin" {
		t.Fatalf("final role=%q err=%v", finalRole, err)
	}
	if err := final["budget_duration"].As(&finalDuration); err != nil || finalDuration != "30d" {
		t.Fatalf("final duration=%q err=%v", finalDuration, err)
	}
	if !final["max_budget_in_team"].Equal(tftypes.NewValue(tftypes.Number, float64(25))) {
		t.Fatalf("final max=%s, want 25", final["max_budget_in_team"])
	}
}

func TestTeamMemberNonAcceptedCreateDoesNotRetainPlannedOwnership(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/team/info":
			writeTeamMemberSnapshot(writer, "team-1", nil, nil, "")
		case "/team/member_add":
			http.Error(writer, `{"error":"rejected"}`, http.StatusBadRequest)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	schema := teamMemberTestSchema(t)
	plan := teamMemberTestPlan(t, schema, TeamMemberResourceModel{
		ID: types.StringUnknown(), TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-1"), UserEmail: types.StringNull(),
		Role: types.StringValue("user"), MaxBudgetInTeam: types.Float64Null(), BudgetDuration: types.StringNull(),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&TeamMemberResource{client: teamMemberClient(server)}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
		t.Fatalf("non-accepted create diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
}

func TestTeamMemberReplacementPlanningHasNoSchemaUpgradeSurprise(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get schema: err=%v diagnostics=%v", err, schemaResponse.Diagnostics)
	}
	resourceSchema := schemaResponse.ResourceSchemas["litellm_team_member"]
	if resourceSchema.Version != 0 {
		t.Fatalf("schema version = %d, want historical version 0", resourceSchema.Version)
	}
	resourceType := resourceSchema.ValueType()
	prior := teamMemberProtocolValue(resourceType, "team-1:user-1", "team-1", "user-1", "Member@Example.com")

	tests := []struct {
		name        string
		config      tftypes.Value
		proposed    tftypes.Value
		wantReplace bool
	}{
		{
			name:     "existing both identity remains in place",
			config:   teamMemberProtocolValue(resourceType, nil, "team-1", "user-1", "Member@Example.com"),
			proposed: teamMemberProtocolValue(resourceType, "team-1:user-1", "team-1", "user-1", "Member@Example.com"),
		},
		{
			name:     "email-only config keeps computed canonical state",
			config:   teamMemberProtocolValue(resourceType, nil, "team-1", nil, "member@example.COM"),
			proposed: teamMemberProtocolValue(resourceType, "team-1:user-1", "team-1", tftypes.UnknownValue, "member@example.COM"),
		},
		{
			name:        "canonical user change replaces",
			config:      teamMemberProtocolValue(resourceType, nil, "team-1", "user-2", "other@example.com"),
			proposed:    teamMemberProtocolValue(resourceType, "team-1:user-1", "team-1", "user-2", "other@example.com"),
			wantReplace: true,
		},
		{
			name:        "team change replaces",
			config:      teamMemberProtocolValue(resourceType, nil, "team-2", "user-1", "Member@Example.com"),
			proposed:    teamMemberProtocolValue(resourceType, "team-1:user-1", "team-2", "user-1", "Member@Example.com"),
			wantReplace: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_team_member",
				Config:           teamMemberProtocolDynamicValue(t, resourceType, test.config),
				PriorState:       teamMemberProtocolDynamicValue(t, resourceType, prior),
				ProposedNewState: teamMemberProtocolDynamicValue(t, resourceType, test.proposed),
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%v", err, response.Diagnostics)
			}
			if got := len(response.RequiresReplace) > 0; got != test.wantReplace {
				t.Fatalf("requires replace=%v paths=%v, want %v", got, response.RequiresReplace, test.wantReplace)
			}
		})
	}
}
