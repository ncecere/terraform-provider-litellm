package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type mockBatchRosterMember struct {
	ID       string
	Email    string
	Role     string
	BudgetID string
	Budget   *float64
}

type mockBatchMembership struct {
	UserID   string
	BudgetID string
	Budget   *float64
}

type mockBatchAPI struct {
	mu sync.Mutex

	teamID             string
	teamMemberBudgetID string
	members            []mockBatchRosterMember
	orphanMemberships  []mockBatchMembership
	calls              []string

	addCalls                 int
	updateCalls              int
	deleteCalls              int
	failAddAt                int
	failAddAfterWrite        bool
	failAddAfterMembership   bool
	failUpdate               bool
	failUpdateAt             int
	failDeleteAt             int
	failDeleteAfterRosterAt  int
	staleReads               int
	lastAddedID              string
	partialReads             int
	partialReadsAfterAdd     int
	invalidJSONReads         int
	invalidJSONReadsAfterAdd int
	readStatus               int
	readBody                 string
	omitBudgetTable          bool
	idEmails                 map[string]string
	emailIDs                 map[string]string
}

func (api *mockBatchAPI) handler(writer http.ResponseWriter, request *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.calls = append(api.calls, request.Method+" "+request.URL.Path)
	writer.Header().Set("Content-Type", "application/json")

	if request.Method == http.MethodGet && request.URL.Path == "/team/info" {
		if api.readStatus != 0 {
			writer.WriteHeader(api.readStatus)
			_, _ = writer.Write([]byte(api.readBody))
			return
		}
		if request.URL.Query().Get("team_id") != api.teamID {
			http.Error(writer, "wrong team", http.StatusBadRequest)
			return
		}
		if api.invalidJSONReads > 0 {
			api.invalidJSONReads--
			_, _ = writer.Write([]byte(`{"team_id":`))
			return
		}
		if api.partialReads > 0 {
			api.partialReads--
			api.writeTeamInfoResponse(writer, append([]mockBatchRosterMember(nil), api.members...), false)
			return
		}
		members := append([]mockBatchRosterMember(nil), api.members...)
		if api.staleReads > 0 {
			api.staleReads--
			filtered := members[:0]
			for _, member := range members {
				if member.ID != api.lastAddedID {
					filtered = append(filtered, member)
				}
			}
			members = filtered
		}
		api.writeTeamInfo(writer, members)
		return
	}

	var body map[string]interface{}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}
	switch request.URL.Path {
	case "/team/member_add":
		api.addCalls++
		rawMember, _ := body["member"].(map[string]interface{})
		member := mockBatchRosterMember{
			ID:    stringValue(rawMember["user_id"]),
			Email: stringValue(rawMember["user_email"]),
			Role:  stringValue(rawMember["role"]),
		}
		if member.ID == "" {
			member.ID = api.emailIDs[member.Email]
			if member.ID == "" {
				member.ID = "resolved-" + strings.ReplaceAll(member.Email, "@", "-")
			}
		}
		if member.Email == "" && api.idEmails != nil {
			member.Email = api.idEmails[member.ID]
		}
		if budget, ok := body["max_budget_in_team"].(float64); ok {
			member.BudgetID = "member-budget-" + member.ID
			member.Budget = floatPtr(budget)
		}
		shouldFail := api.failAddAt > 0 && api.addCalls == api.failAddAt
		if shouldFail && api.failAddAfterMembership {
			api.orphanMemberships = append(api.orphanMemberships, mockBatchMembership{UserID: member.ID, BudgetID: member.BudgetID, Budget: member.Budget})
			api.partialReads++
			api.lastAddedID = member.ID
		} else if !shouldFail || api.failAddAfterWrite {
			api.members = append(api.members, member)
			api.partialReads += api.partialReadsAfterAdd
			api.invalidJSONReads += api.invalidJSONReadsAfterAdd
			api.lastAddedID = member.ID
		}
		if shouldFail {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"opaque add failure"}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	case "/team/member_update":
		api.updateCalls++
		if api.failUpdate || (api.failUpdateAt > 0 && api.updateCalls == api.failUpdateAt) {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"opaque update failure"}`))
			return
		}
		index := api.findMember(body)
		if index < 0 {
			// Exact LiteLLM v1.98 behavior: direct user_id update can return 2xx
			// for a membership-only row without appending members_with_roles.
			// A supplied budget may still mutate the orphan's budget relation.
			userID := stringValue(body["user_id"])
			for orphanIndex := range api.orphanMemberships {
				if userID == "" || api.orphanMemberships[orphanIndex].UserID != userID {
					continue
				}
				if budget, exists := body["max_budget_in_team"]; exists && budget != nil {
					if api.orphanMemberships[orphanIndex].BudgetID == "" {
						api.orphanMemberships[orphanIndex].BudgetID = "member-budget-" + userID
					}
					api.orphanMemberships[orphanIndex].Budget = floatPtr(budget.(float64))
				}
				_, _ = writer.Write([]byte(`{}`))
				return
			}
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"member not found"}`))
			return
		}
		if role, ok := body["role"].(string); ok {
			api.members[index].Role = role
		}
		if budget, exists := body["max_budget_in_team"]; exists {
			if budget == nil {
				api.members[index].BudgetID = ""
				api.members[index].Budget = nil
			} else {
				if api.members[index].BudgetID == "" || (api.teamMemberBudgetID != "" && api.members[index].BudgetID == api.teamMemberBudgetID) {
					api.members[index].BudgetID = "member-budget-" + api.members[index].ID
				}
				// A historical shared budget row is updated in place by v1.98.
				budgetID := api.members[index].BudgetID
				for memberIndex := range api.members {
					if api.members[memberIndex].BudgetID == budgetID {
						api.members[memberIndex].Budget = floatPtr(budget.(float64))
					}
				}
			}
		}
		_, _ = writer.Write([]byte(`{}`))
	case "/team/member_delete":
		api.deleteCalls++
		if api.failDeleteAt > 0 && api.deleteCalls == api.failDeleteAt {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"opaque delete failure"}`))
			return
		}
		index := api.findMember(body)
		if index >= 0 {
			member := api.members[index]
			api.members = append(api.members[:index], api.members[index+1:]...)
			if api.failDeleteAfterRosterAt > 0 && api.deleteCalls == api.failDeleteAfterRosterAt {
				api.orphanMemberships = append(api.orphanMemberships, mockBatchMembership{UserID: member.ID, BudgetID: member.BudgetID, Budget: member.Budget})
				writer.WriteHeader(http.StatusInternalServerError)
				_, _ = writer.Write([]byte(`{"error":"opaque failure after roster removal"}`))
				return
			}
			_, _ = writer.Write([]byte(`{}`))
			return
		}
		// LiteLLM v1.98 resolves the member in members_with_roles before deleting
		// team_memberships. A membership-only orphan therefore cannot be deleted
		// through this endpoint, even when user_id identifies the row exactly.
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"member not found"}`))
	default:
		http.NotFound(writer, request)
	}
}

func (api *mockBatchAPI) findMember(body map[string]interface{}) int {
	id := stringValue(body["user_id"])
	email := stringValue(body["user_email"])
	for index, member := range api.members {
		if (id != "" && member.ID == id) || (email != "" && strings.EqualFold(member.Email, email)) {
			return index
		}
	}
	return -1
}

func (api *mockBatchAPI) writeTeamInfo(writer http.ResponseWriter, members []mockBatchRosterMember) {
	api.writeTeamInfoResponse(writer, members, true)
}

func (api *mockBatchAPI) writeTeamInfoResponse(writer http.ResponseWriter, members []mockBatchRosterMember, includeRoster bool) {
	roster := make([]map[string]interface{}, 0, len(members))
	memberships := make([]map[string]interface{}, 0, len(members))
	for _, member := range members {
		roster = append(roster, map[string]interface{}{
			"user_id": member.ID, "user_email": member.Email, "role": member.Role,
		})
		var budget interface{}
		var budgetID interface{}
		memberBudgetID := member.BudgetID
		if memberBudgetID == "" && member.Budget != nil {
			memberBudgetID = "member-budget-" + member.ID
		}
		if memberBudgetID != "" {
			budgetID = memberBudgetID
			budgetObject := map[string]interface{}{"budget_id": memberBudgetID, "max_budget": nil}
			if member.Budget != nil {
				budgetObject["max_budget"] = *member.Budget
			}
			budget = budgetObject
		}
		membershipRow := map[string]interface{}{
			"team_id": api.teamID, "user_id": member.ID, "budget_id": budgetID, "litellm_budget_table": budget,
		}
		if api.omitBudgetTable {
			delete(membershipRow, "litellm_budget_table")
		}
		memberships = append(memberships, membershipRow)
	}
	for _, membership := range api.orphanMemberships {
		var budget interface{}
		var budgetID interface{}
		if membership.BudgetID != "" {
			budgetID = membership.BudgetID
			budget = map[string]interface{}{"budget_id": membership.BudgetID, "max_budget": membership.Budget}
		}
		membershipRow := map[string]interface{}{
			"team_id": api.teamID, "user_id": membership.UserID, "budget_id": budgetID, "litellm_budget_table": budget,
		}
		if api.omitBudgetTable {
			delete(membershipRow, "litellm_budget_table")
		}
		memberships = append(memberships, membershipRow)
	}
	metadata := map[string]interface{}{}
	if api.teamMemberBudgetID != "" {
		metadata["team_member_budget_id"] = api.teamMemberBudgetID
	}
	teamInfo := map[string]interface{}{"team_id": api.teamID, "metadata": metadata}
	if includeRoster {
		teamInfo["members_with_roles"] = roster
	}
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{
		"team_id": api.teamID, "team_info": teamInfo, "team_memberships": memberships,
	})
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func floatPtr(value float64) *float64 {
	return &value
}

func teamMemberAddTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&TeamMemberAddResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func teamMemberAddTestState(t *testing.T, schema resourceschema.Schema, data TeamMemberAddResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set state: %v", diagnostics)
	}
	return state
}

func teamMemberAddTestPlan(t *testing.T, schema resourceschema.Schema, data TeamMemberAddResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set plan: %v", diagnostics)
	}
	return plan
}

func teamMemberAddTestConfig(t *testing.T, schema resourceschema.Schema, data TeamMemberAddResourceModel) tfsdk.Config {
	t.Helper()
	state := teamMemberAddTestState(t, schema, data)
	return tfsdk.Config{Raw: state.Raw, Schema: schema}
}

func teamMemberSet(t *testing.T, members ...MemberModel) types.Set {
	t.Helper()
	value, diagnostics := types.SetValueFrom(context.Background(), MemberObjectType(), members)
	if diagnostics.HasError() {
		t.Fatalf("member set diagnostics: %v", diagnostics)
	}
	return value
}

func memberByID(id, role string) MemberModel {
	return MemberModel{UserID: types.StringValue(id), UserEmail: types.StringNull(), Role: types.StringValue(role)}
}

func memberByEmail(email, role string) MemberModel {
	return MemberModel{UserID: types.StringNull(), UserEmail: types.StringValue(email), Role: types.StringValue(role)}
}

func batchModel(teamID string, budget types.Float64, members ...MemberModel) TeamMemberAddResourceModel {
	return TeamMemberAddResourceModel{
		ID: types.StringValue(teamID), TeamID: types.StringValue(teamID), Members: types.SetNull(MemberObjectType()), MaxBudgetInTeam: budget,
	}
}

func batchModelWithMembers(t *testing.T, teamID string, budget types.Float64, members ...MemberModel) TeamMemberAddResourceModel {
	model := batchModel(teamID, budget)
	model.Members = teamMemberSet(t, members...)
	return model
}

func decodeBatchState(t *testing.T, state tfsdk.State) TeamMemberAddResourceModel {
	t.Helper()
	var model TeamMemberAddResourceModel
	if diagnostics := state.Get(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("decode state: %v", diagnostics)
	}
	return model
}

func TestBatchMembersFromSetCancellationAndDiagnosticsAreSafe(t *testing.T) {
	t.Parallel()
	sensitiveEmail := "sensitive-member@example.invalid"
	members := teamMemberSet(t, MemberModel{
		UserID: types.StringNull(), UserEmail: types.StringValue(sensitiveEmail), Role: types.StringNull(),
	})
	converted, diagnostics := batchMembersFromSet(context.Background(), members, true)
	if !diagnostics.HasError() || converted != nil {
		t.Fatalf("unknown role conversion = %#v, diagnostics=%v; want atomic failure", converted, diagnostics)
	}
	if rendered := fmt.Sprint(diagnostics); strings.Contains(rendered, sensitiveEmail) || !strings.Contains(rendered, "member") {
		t.Fatalf("unsafe member diagnostics: %s", rendered)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	converted, diagnostics = batchMembersFromSet(ctx, members, false)
	if !diagnostics.HasError() || converted != nil {
		t.Fatalf("canceled conversion = %#v, diagnostics=%v; want atomic cancellation", converted, diagnostics)
	}
	if rendered := fmt.Sprint(diagnostics); strings.Contains(rendered, sensitiveEmail) || !strings.Contains(rendered, collectionConversionCanceled) {
		t.Fatalf("unsafe cancellation diagnostics: %s", rendered)
	}
}

func batchStateMembers(t *testing.T, model TeamMemberAddResourceModel) []batchMember {
	t.Helper()
	members, diagnostics := batchMembersFromSet(context.Background(), model.Members, false)
	if diagnostics.HasError() {
		t.Fatalf("decode members: %v", diagnostics)
	}
	sortBatchMembers(members)
	return members
}

func newMockBatchResource(t *testing.T, api *mockBatchAPI) (*TeamMemberAddResource, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(api.handler))
	resourceUnderTest := &TeamMemberAddResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
	return resourceUnderTest, server
}

func teamMemberAddProtocolDynamicValue(t *testing.T, valueType tftypes.Type, value tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	dynamic, err := tfprotov6.NewDynamicValue(valueType, value)
	if err != nil {
		t.Fatalf("build team-member-add protocol value: %v", err)
	}
	return &dynamic
}

func teamMemberAddProtocolMembers(t *testing.T, resourceType tftypes.Type, state *tfprotov6.DynamicValue) []map[string]tftypes.Value {
	t.Helper()
	value, err := state.Unmarshal(resourceType)
	if err != nil {
		t.Fatalf("decode team-member-add protocol state: %v", err)
	}
	var attributes map[string]tftypes.Value
	if err := value.As(&attributes); err != nil {
		t.Fatalf("decode team-member-add protocol attributes: %v", err)
	}
	var elements []tftypes.Value
	if err := attributes["member"].As(&elements); err != nil {
		t.Fatalf("decode team-member-add protocol member set: %v", err)
	}
	members := make([]map[string]tftypes.Value, 0, len(elements))
	for _, element := range elements {
		var member map[string]tftypes.Value
		if err := element.As(&member); err != nil {
			t.Fatalf("decode team-member-add protocol member: %v", err)
		}
		members = append(members, member)
	}
	return members
}

func TestTeamMemberAddEmailUserIDIsOptionalComputed(t *testing.T) {
	t.Parallel()
	schema := teamMemberAddTestSchema(t)
	memberBlock, ok := schema.Blocks["member"].(resourceschema.SetNestedBlock)
	if !ok {
		t.Fatalf("member block type = %T", schema.Blocks["member"])
	}
	userID, ok := memberBlock.NestedObject.Attributes["user_id"].(resourceschema.StringAttribute)
	if !ok || !userID.Optional || !userID.Computed || userID.Required {
		t.Fatalf("member.user_id must be Optional+Computed: %#v", userID)
	}
}

func TestTeamMemberAddFrameworkLifecycle(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{teamID: "team/lifecycle #1"}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)

	createModel := batchModelWithMembers(t, api.teamID, types.Float64Value(10), memberByID("user-a", "user"), memberByID("user-b", "admin"))
	createModel.ID = types.StringUnknown()
	createPlan := teamMemberAddTestPlan(t, schema, createModel)
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: createPlan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: createPlan}, createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	created := decodeBatchState(t, createResponse.State)
	if created.ID.ValueString() != api.teamID || len(batchStateMembers(t, created)) != 2 || created.MaxBudgetInTeam.ValueFloat64() != 10 {
		t.Fatalf("created state = %#v", created)
	}

	api.mu.Lock()
	api.calls = nil
	api.mu.Unlock()
	readResponse := &resource.ReadResponse{State: createResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: createResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("no-drift read diagnostics: %v", readResponse.Diagnostics)
	}
	readState := decodeBatchState(t, readResponse.State)
	if len(batchStateMembers(t, readState)) != 2 || readState.MaxBudgetInTeam.ValueFloat64() != 10 {
		t.Fatalf("no-drift state = %#v", readState)
	}
	api.mu.Lock()
	if got := strings.Join(api.calls, ","); got != "GET /team/info" {
		t.Fatalf("no-drift calls = %q", got)
	}
	api.calls = nil
	api.mu.Unlock()

	updateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(20), memberByID("user-a", "admin"), memberByID("user-b", "admin"))
	updatePlan := teamMemberAddTestPlan(t, schema, updateModel)
	updateResponse := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, updateModel)}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: updatePlan, State: readResponse.State}, updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("role/budget update diagnostics: %v", updateResponse.Diagnostics)
	}
	api.mu.Lock()
	joined := strings.Join(api.calls, ",")
	if strings.Contains(joined, "/team/member_add") || strings.Contains(joined, "/team/member_delete") || strings.Count(joined, "POST /team/member_update") != 3 {
		t.Fatalf("separate native role/grouped-budget update calls = %q", joined)
	}
	api.calls = nil
	api.mu.Unlock()

	replaceModel := batchModelWithMembers(t, api.teamID, types.Float64Value(20), memberByID("user-b", "admin"), memberByID("user-c", "user"))
	replacePlan := teamMemberAddTestPlan(t, schema, replaceModel)
	replaceResponse := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, replaceModel)}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: replacePlan, State: updateResponse.State}, replaceResponse)
	if replaceResponse.Diagnostics.HasError() {
		t.Fatalf("add/remove update diagnostics: %v", replaceResponse.Diagnostics)
	}
	api.mu.Lock()
	joined = strings.Join(api.calls, ",")
	addIndex := strings.Index(joined, "POST /team/member_add")
	deleteIndex := strings.Index(joined, "POST /team/member_delete")
	api.mu.Unlock()
	if addIndex < 0 || deleteIndex < 0 || addIndex > deleteIndex {
		t.Fatalf("addition must precede removal, calls = %q", joined)
	}

	deleteResponse := &resource.DeleteResponse{State: replaceResponse.State}
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: replaceResponse.State}, deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	api.mu.Lock()
	remaining := len(api.members)
	api.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("successful destroy left %d owned members", remaining)
	}
}

func TestTeamMemberAddReadSurfacesOnlyOwnedDrift(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID: "team-drift",
		members: []mockBatchRosterMember{
			{ID: "owned-a", Email: "a@example.com", Role: "admin"},
			{ID: "unrelated", Email: "x@example.com", Role: "user"},
		},
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("owned-a", "user"), memberByID("owned-b", "user"))
	state := teamMemberAddTestState(t, schema, stateModel)
	response := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	members := batchStateMembers(t, decodeBatchState(t, response.State))
	if len(members) != 1 || members[0].UserID != "owned-a" || members[0].Role != "admin" {
		t.Fatalf("owned drift state = %#v", members)
	}
}

func TestTeamMemberAddCompositeImportIsExactAndLegacySafe(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID: "team/import:? #1",
		members: []mockBatchRosterMember{
			{ID: "user/id:1", Email: "one@example.com", Role: "admin", Budget: floatPtr(7)},
			{ID: "resolved-two-example.com", Email: "two@example.com", Role: "user", Budget: floatPtr(7)},
			{ID: "unrelated", Email: "other@example.com", Role: "user", Budget: floatPtr(100)},
		},
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	identity := []batchMember{
		{UserID: "user/id:1", HasUserID: true},
		{UserEmail: "two@example.com", HasUserEmail: true},
	}
	importID, err := formatTeamMemberAddImportID(api.teamID, identity)
	if err != nil {
		t.Fatalf("format import ID: %v", err)
	}
	nullState := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	importResponse := &resource.ImportStateResponse{State: nullState}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: importID}, importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResponse.Diagnostics)
	}
	readResponse := &resource.ReadResponse{State: importResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: importResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("import read diagnostics: %v", readResponse.Diagnostics)
	}
	imported := decodeBatchState(t, readResponse.State)
	members := batchStateMembers(t, imported)
	if imported.ID.ValueString() != api.teamID || imported.TeamID.ValueString() != api.teamID || imported.MaxBudgetInTeam.ValueFloat64() != 7 || len(members) != 2 {
		t.Fatalf("imported state = %#v members=%#v", imported, members)
	}
	var importedEmail *batchMember
	for index := range members {
		if members[index].UserEmail == "two@example.com" {
			importedEmail = &members[index]
		}
	}
	if importedEmail == nil || importedEmail.UserID != "resolved-two-example.com" || !importedEmail.HasUserID || !importedEmail.HasUserEmail {
		t.Fatalf("email import did not retain email and backfill canonical user_id: %#v", members)
	}

	legacyResponse := &resource.ImportStateResponse{State: nullState}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: api.teamID}, legacyResponse)
	if legacyResponse.Diagnostics.HasError() || len(legacyResponse.Diagnostics) == 0 {
		t.Fatalf("legacy import diagnostics = %v", legacyResponse.Diagnostics)
	}
	legacyRead := &resource.ReadResponse{State: legacyResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: legacyResponse.State}, legacyRead)
	if legacyRead.Diagnostics.HasError() || len(batchStateMembers(t, decodeBatchState(t, legacyRead.State))) != 0 {
		t.Fatalf("legacy import must remain empty ownership: %v", legacyRead.Diagnostics)
	}

	for _, historicalTeamID := range []string{"ordinary-team", "v1.production", "v1.not-base64.i~%%%", "v1..", "v1.Zg.i~Zh"} {
		plainResponse := &resource.ImportStateResponse{State: nullState}
		resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: historicalTeamID}, plainResponse)
		if plainResponse.Diagnostics.HasError() {
			t.Fatalf("historical plain team ID %q was rejected: %v", historicalTeamID, plainResponse.Diagnostics)
		}
		plain := decodeBatchState(t, plainResponse.State)
		if plain.TeamID.ValueString() != historicalTeamID || len(batchStateMembers(t, plain)) != 0 {
			t.Fatalf("historical plain import = %#v", plain)
		}
	}

	// A historical team ID can itself be a fully valid composite or a valid
	// escaped-token-looking string. The explicit t~ form removes both collisions.
	compositeLookingTeamID, err := formatTeamMemberAddImportID("decoded-team", []batchMember{{UserID: "decoded-user", HasUserID: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, collidingTeamID := range []string{compositeLookingTeamID, "t~dGVhbQ"} {
		escaped, formatErr := formatTeamMemberAddPlainImportID(collidingTeamID)
		if formatErr != nil {
			t.Fatalf("format escaped team ID %q: %v", collidingTeamID, formatErr)
		}
		plainResponse := &resource.ImportStateResponse{State: nullState}
		resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: escaped}, plainResponse)
		if plainResponse.Diagnostics.HasError() {
			t.Fatalf("escaped collision %q was rejected: %v", collidingTeamID, plainResponse.Diagnostics)
		}
		plain := decodeBatchState(t, plainResponse.State)
		if plain.TeamID.ValueString() != collidingTeamID || plain.ID.ValueString() != collidingTeamID || len(batchStateMembers(t, plain)) != 0 {
			t.Fatalf("escaped collision import = %#v", plain)
		}
	}
	decodedTeam, decodedMembers, legacy, err := parseTeamMemberAddImportID(compositeLookingTeamID)
	if err != nil || legacy || decodedTeam != "decoded-team" || len(decodedMembers) != 1 {
		t.Fatalf("prior composite syntax changed: team=%q members=%#v legacy=%t err=%v", decodedTeam, decodedMembers, legacy, err)
	}

	for _, malformedEscaped := range []string{"t~", "t~%%%", "t~Zg==", "t~Zh", "t~_w"} {
		malformedResponse := &resource.ImportStateResponse{State: nullState}
		resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: malformedEscaped}, malformedResponse)
		if !malformedResponse.Diagnostics.HasError() {
			t.Fatalf("malformed escaped plain import %q must fail", malformedEscaped)
		}
	}

	emptyResponse := &resource.ImportStateResponse{State: nullState}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: ""}, emptyResponse)
	if !emptyResponse.Diagnostics.HasError() {
		t.Fatal("empty import ID must fail")
	}
}

func TestTeamMemberAddValidationAndAmbiguousAliases(t *testing.T) {
	t.Parallel()
	schema := teamMemberAddTestSchema(t)
	resourceUnderTest := &TeamMemberAddResource{}

	tests := []struct {
		name    string
		members []MemberModel
	}{
		{name: "empty"},
		{name: "identity-less", members: []MemberModel{{UserID: types.StringNull(), UserEmail: types.StringNull(), Role: types.StringValue("user")}}},
		{name: "duplicate id", members: []MemberModel{memberByID("same", "user"), memberByID("same", "admin")}},
		{name: "duplicate email", members: []MemberModel{memberByEmail("same@example.com", "user"), memberByEmail("same@example.com", "admin")}},
		{name: "case-only duplicate email", members: []MemberModel{memberByEmail("Same@Example.com", "user"), memberByEmail("same@example.COM", "admin")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := batchModelWithMembers(t, "team-validation", types.Float64Null(), test.members...)
			response := &resource.ValidateConfigResponse{}
			resourceUnderTest.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: teamMemberAddTestConfig(t, schema, model)}, response)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected plan-time identity validation error")
			}
		})
	}

	snapshot := &teamMemberAddSnapshot{Members: []remoteBatchMember{{UserID: "user-1", UserEmail: "same@example.com", Role: "user"}}}
	_, err := observeBatch(snapshot, []batchMember{
		{UserID: "user-1", HasUserID: true, RoleKnown: true, Role: "user"},
		{UserEmail: "same@example.com", HasUserEmail: true, RoleKnown: true, Role: "user"},
	}, types.Float64Null())
	if err == nil {
		t.Fatal("email-vs-ID aliases resolving to one remote member must be rejected")
	}
}

func TestTeamMemberAddEmailIdentityIsCaseInsensitiveAndPreservesConfiguration(t *testing.T) {
	t.Parallel()
	schema := teamMemberAddTestSchema(t)

	t.Run("preflight rejects unowned case alias without mutation", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-email-preflight", members: []mockBatchRosterMember{{ID: "existing", Email: "Owner@Example.COM", Role: "user"}}}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByEmail("owner@example.com", "user"))
		model.ID = types.StringUnknown()
		plan := teamMemberAddTestPlan(t, schema, model)
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
		if !response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
			t.Fatalf("case alias preflight diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
		}
		api.mu.Lock()
		addCalls := api.addCalls
		api.mu.Unlock()
		if addCalls != 0 {
			t.Fatalf("case alias preflight sent %d adds", addCalls)
		}
	})

	t.Run("successful create persists canonical id and configured email", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-email-create", emailIDs: map[string]string{"Configured@Example.COM": "canonical-created"}}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByEmail("Configured@Example.COM", "user"))
		model.ID = types.StringUnknown()
		plan := teamMemberAddTestPlan(t, schema, model)
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("email-only create diagnostics: %v", response.Diagnostics)
		}
		members := batchStateMembers(t, decodeBatchState(t, response.State))
		if len(members) != 1 || members[0].UserID != "canonical-created" || members[0].UserEmail != "Configured@Example.COM" || !members[0].RoleKnown {
			t.Fatalf("email-only create state = %#v", members)
		}
	})

	t.Run("refresh preserves configured email spelling", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-email-preserve", members: []mockBatchRosterMember{{ID: "canonical", Email: "owner@example.com", Role: "admin"}}}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByEmail("Owner@Example.COM", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		response := &resource.ReadResponse{State: state}
		resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("case-insensitive read diagnostics: %v", response.Diagnostics)
		}
		members := batchStateMembers(t, decodeBatchState(t, response.State))
		if len(members) != 1 || members[0].UserID != "canonical" || members[0].UserEmail != "Owner@Example.COM" || members[0].Role != "admin" {
			t.Fatalf("case-preserving canonical read = %#v", members)
		}
	})
}

func TestTeamMemberAddLegacyEmailOnlyAmbiguityRetainsOwnership(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID:            "team-legacy-email-ambiguity",
		orphanMemberships: []mockBatchMembership{{UserID: "canonical-but-unknown", BudgetID: "orphan-budget", Budget: floatPtr(7)}},
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	model := batchModelWithMembers(t, api.teamID, types.Float64Value(7), memberByEmail("legacy@example.com", "user"))
	state := teamMemberAddTestState(t, schema, model)

	readResponse := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, readResponse)
	if !readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() || !strings.Contains(readResponse.Diagnostics.Errors()[0].Summary(), "Legacy Email-Only") {
		t.Fatalf("legacy ambiguous read diagnostics=%v state_null=%t", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
	}
	readMembers := batchStateMembers(t, decodeBatchState(t, readResponse.State))
	if len(readMembers) != 1 || readMembers[0].HasUserID || readMembers[0].UserEmail != "legacy@example.com" || !readMembers[0].RoleKnown {
		t.Fatalf("legacy ambiguous read changed prior ownership: %#v", readMembers)
	}

	plan := teamMemberAddTestPlan(t, schema, model)
	updateResponse := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, model)}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: readResponse.State}, updateResponse)
	if !updateResponse.Diagnostics.HasError() || updateResponse.State.Raw.IsNull() {
		t.Fatalf("legacy ambiguous update diagnostics=%v", updateResponse.Diagnostics)
	}
	deleteResponse := &resource.DeleteResponse{State: updateResponse.State}
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: updateResponse.State}, deleteResponse)
	if !deleteResponse.Diagnostics.HasError() || deleteResponse.State.Raw.IsNull() {
		t.Fatalf("legacy ambiguous destroy reported success: %v", deleteResponse.Diagnostics)
	}
	api.mu.Lock()
	addCalls, updateCalls, deleteCalls, orphanCount := api.addCalls, api.updateCalls, api.deleteCalls, len(api.orphanMemberships)
	api.mu.Unlock()
	if addCalls != 0 || updateCalls != 0 || deleteCalls != 0 || orphanCount != 1 {
		t.Fatalf("legacy ambiguity mutated API: add=%d update=%d delete=%d orphan=%d", addCalls, updateCalls, deleteCalls, orphanCount)
	}
}

func TestTeamMemberAddPartialCreateRetainsOnlyConfirmedMembers(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{teamID: "team-partial-create", failAddAt: 2}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("a", "user"), memberByID("b", "user"))
	model.ID = types.StringUnknown()
	plan := teamMemberAddTestPlan(t, schema, model)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("partial create response: diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
	members := batchStateMembers(t, decodeBatchState(t, response.State))
	if len(members) != 1 || members[0].UserID != "a" {
		t.Fatalf("partial create state = %#v", members)
	}
}

func TestTeamMemberAddFailedCreateRetainsNewMembershipAndConfirmedRole(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{teamID: "team-failed-after-roster", failAddAt: 1, failAddAfterWrite: true}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("owned-after-error", "user"))
	model.ID = types.StringUnknown()
	plan := teamMemberAddTestPlan(t, schema, model)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("post-write failure must retain owned state: diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
	members := batchStateMembers(t, decodeBatchState(t, response.State))
	if len(members) != 1 || members[0].UserID != "owned-after-error" || !members[0].RoleKnown || members[0].Role != "user" {
		t.Fatalf("confirmed post-write state = %#v", members)
	}
}

func TestTeamMemberAddV198MembershipBeforeRosterFailureLifecycle(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{teamID: "team-v198-partial-order", failAddAt: 1, failAddAfterMembership: true}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	model := batchModelWithMembers(t, api.teamID, types.Float64Value(12), memberByEmail("Partial@Example.COM", "admin"))
	model.ID = types.StringUnknown()
	plan := teamMemberAddTestPlan(t, schema, model)
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, createResponse)
	if !createResponse.Diagnostics.HasError() || createResponse.State.Raw.IsNull() {
		t.Fatalf("partial-order create diagnostics=%v null=%t", createResponse.Diagnostics, createResponse.State.Raw.IsNull())
	}
	created := decodeBatchState(t, createResponse.State)
	createdMembers := batchStateMembers(t, created)
	if len(createdMembers) != 1 || !createdMembers[0].HasUserID || createdMembers[0].UserEmail != "Partial@Example.COM" || createdMembers[0].RoleKnown || created.MaxBudgetInTeam.ValueFloat64() != 12 {
		t.Fatalf("partial-order state = %#v members=%#v", created, createdMembers)
	}

	readResponse := &resource.ReadResponse{State: createResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: createResponse.State}, readResponse)
	if !readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() {
		t.Fatalf("orphan read must retain state and error: %v", readResponse.Diagnostics)
	}
	if members := batchStateMembers(t, decodeBatchState(t, readResponse.State)); len(members) != 1 || members[0].RoleKnown {
		t.Fatalf("orphan read claimed a role: %#v", members)
	}

	api.mu.Lock()
	callsBeforeRetry := len(api.calls)
	api.mu.Unlock()
	updateResponse := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, model)}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: readResponse.State}, updateResponse)
	if !updateResponse.Diagnostics.HasError() || updateResponse.State.Raw.IsNull() {
		t.Fatalf("unsafe orphan retry must retain/error: %v", updateResponse.Diagnostics)
	}
	api.mu.Lock()
	retryCalls := strings.Join(api.calls[callsBeforeRetry:], ",")
	api.mu.Unlock()
	if strings.Contains(retryCalls, "POST /team/member_add") || strings.Contains(retryCalls, "POST /team/member_update") {
		t.Fatalf("orphan retry sent an unsafe repair mutation: %q", retryCalls)
	}

	api.mu.Lock()
	callsBeforeDestroy := len(api.calls)
	api.mu.Unlock()
	deleteResponse := &resource.DeleteResponse{State: updateResponse.State}
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: updateResponse.State}, deleteResponse)
	if !deleteResponse.Diagnostics.HasError() || deleteResponse.State.Raw.IsNull() {
		t.Fatalf("destroy must retain unresolved membership-only ownership: %v", deleteResponse.Diagnostics)
	}
	api.mu.Lock()
	destroyCalls := strings.Join(api.calls[callsBeforeDestroy:], ",")
	orphanCount, deleteCalls, updateCalls := len(api.orphanMemberships), api.deleteCalls, api.updateCalls
	api.mu.Unlock()
	if strings.Contains(destroyCalls, "POST /team/member_delete") || orphanCount != 1 || deleteCalls != 0 || updateCalls != 0 {
		t.Fatalf("destroy sent unsafe cleanup or changed orphan: calls=%q orphan=%d delete=%d update=%d", destroyCalls, orphanCount, deleteCalls, updateCalls)
	}
	if members := batchStateMembers(t, decodeBatchState(t, deleteResponse.State)); len(members) != 1 || members[0].RoleKnown {
		t.Fatalf("destroy abandoned membership-only ownership: %#v", members)
	}

	// Match the exact LiteLLM v1.98 defect. A direct user_id update reports
	// success and may mutate budget data, but it does not append the roster.
	updateErr := resourceUnderTest.client.DoRequestWithResponse(context.Background(), http.MethodPost, "/team/member_update", map[string]interface{}{
		"team_id": api.teamID, "user_id": createdMembers[0].UserID, "role": "admin", "max_budget_in_team": float64(99),
	}, nil)
	if updateErr != nil {
		t.Fatalf("v1.98 membership-only update should report 2xx: %v", updateErr)
	}
	deleteErr := resourceUnderTest.client.DoRequestWithResponse(context.Background(), http.MethodPost, "/team/member_delete", map[string]interface{}{
		"team_id": api.teamID, "user_id": createdMembers[0].UserID,
	}, nil)
	if !IsAPIErrorStatus(deleteErr, http.StatusBadRequest) {
		t.Fatalf("v1.98 membership-only delete = %v, want HTTP 400", deleteErr)
	}
	api.mu.Lock()
	if len(api.orphanMemberships) != 1 || api.orphanMemberships[0].Budget == nil || *api.orphanMemberships[0].Budget != 99 || len(api.members) != 0 {
		api.mu.Unlock()
		t.Fatal("v1.98 no-op update did not preserve roster absence and mutate only orphan budget")
	}
	// Simulate required administrator-side upstream remediation; this is not an
	// operation available through either v1.98 team-member endpoint.
	api.orphanMemberships = nil
	api.failAddAt = 0
	api.mu.Unlock()

	retryResponse := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, retryResponse)
	if retryResponse.Diagnostics.HasError() {
		t.Fatalf("apply after cleanup diagnostics: %v", retryResponse.Diagnostics)
	}
	if members := batchStateMembers(t, decodeBatchState(t, retryResponse.State)); len(members) != 1 || !members[0].RoleKnown || members[0].Role != "admin" {
		t.Fatalf("apply after cleanup state = %#v", members)
	}
}

func TestTeamMemberAddOrphanRemediationProtocolSequence(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{teamID: "team-protocol-remediation", failAddAt: 2, failAddAfterMembership: true}
	_, server := newMockBatchResource(t, api)
	defer server.Close()

	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get provider schema: err=%v diagnostics=%v", err, schemaResponse.Diagnostics)
	}
	providerType := schemaResponse.Provider.ValueType()
	providerConfig := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"api_base":             tftypes.NewValue(tftypes.String, server.URL),
		"api_key":              tftypes.NewValue(tftypes.String, "admin"),
		"insecure_skip_verify": tftypes.NewValue(tftypes.Bool, false),
		"litellm_changed_by":   tftypes.NewValue(tftypes.String, nil),
	})
	configureResponse, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "test",
		Config:           teamMemberAddProtocolDynamicValue(t, providerType, providerConfig),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configureResponse.Diagnostics) {
		t.Fatalf("configure provider: err=%v diagnostics=%v", err, configureResponse.Diagnostics)
	}

	resourceSchema := schemaResponse.ResourceSchemas["litellm_team_member_add"]
	if resourceSchema == nil {
		t.Fatal("team-member-add protocol schema is missing")
	}
	resourceType := resourceSchema.ValueType()
	memberType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"user_id": tftypes.String, "user_email": tftypes.String, "role": tftypes.String,
	}}
	memberSetType := tftypes.Set{ElementType: memberType}
	memberValue := func(email string, userID interface{}, role string) tftypes.Value {
		return tftypes.NewValue(memberType, map[string]tftypes.Value{
			"user_id":    tftypes.NewValue(tftypes.String, userID),
			"user_email": tftypes.NewValue(tftypes.String, email),
			"role":       tftypes.NewValue(tftypes.String, role),
		})
	}
	resourceValue := func(id interface{}, userID interface{}) tftypes.Value {
		return tftypes.NewValue(resourceType, map[string]tftypes.Value{
			"id":      tftypes.NewValue(tftypes.String, id),
			"team_id": tftypes.NewValue(tftypes.String, api.teamID),
			"member": tftypes.NewValue(memberSetType, []tftypes.Value{
				memberValue("a-stable@example.com", userID, "user"),
				memberValue("member@example.com", userID, "admin"),
			}),
			"max_budget_in_team": tftypes.NewValue(tftypes.Number, float64(12)),
		})
	}
	config := teamMemberAddProtocolDynamicValue(t, resourceType, resourceValue(nil, nil))
	proposed := teamMemberAddProtocolDynamicValue(t, resourceType, resourceValue(tftypes.UnknownValue, tftypes.UnknownValue))
	nullState := teamMemberAddProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))

	createPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: config, PriorState: nullState, ProposedNewState: proposed,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createPlan.Diagnostics) {
		t.Fatalf("plan partial add: err=%v diagnostics=%v", err, createPlan.Diagnostics)
	}
	partialAdd, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: config, PriorState: nullState,
		PlannedState: createPlan.PlannedState, PlannedPrivate: createPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(partialAdd.Diagnostics) || partialAdd.NewState == nil || len(partialAdd.Private) == 0 {
		t.Fatalf("partial add: err=%v diagnostics=%v state_nil=%t private=%q", err, partialAdd.Diagnostics, partialAdd.NewState == nil, partialAdd.Private)
	}
	partialMembers := teamMemberAddProtocolMembers(t, resourceType, partialAdd.NewState)
	if len(partialMembers) != 2 {
		t.Fatalf("partial add members=%#v, want stable member plus orphan", partialMembers)
	}
	var partialUserID, partialEmail string
	foundOrphan := false
	for _, member := range partialMembers {
		var email string
		if err := member["user_email"].As(&email); err != nil {
			t.Fatalf("decode partial email: %v", err)
		}
		if email != "member@example.com" {
			continue
		}
		foundOrphan = true
		partialEmail = email
		if err := member["user_id"].As(&partialUserID); err != nil {
			t.Fatalf("decode canonical partial user_id: %v", err)
		}
		if member["role"].IsKnown() {
			t.Fatalf("membership-only role unexpectedly known: %s", member["role"])
		}
	}
	if !foundOrphan || partialUserID == "" || partialEmail != "member@example.com" {
		t.Fatalf("partial canonical orphan identity user_id=%q email=%q members=%#v", partialUserID, partialEmail, partialMembers)
	}

	orphanRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member_add", CurrentState: partialAdd.NewState, Private: partialAdd.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(orphanRead.Diagnostics) || orphanRead.NewState == nil {
		t.Fatalf("orphan read: err=%v diagnostics=%v", err, orphanRead.Diagnostics)
	}
	api.mu.Lock()
	callsBeforeCleanup := len(api.calls)
	updatesBeforeCleanup := api.updateCalls
	api.orphanMemberships = nil
	api.failAddAt = 0
	api.mu.Unlock()
	if updatesBeforeCleanup != 0 {
		t.Fatalf("provider used v1.98 membership-only member_update as repair: %d calls", updatesBeforeCleanup)
	}

	cleanRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member_add", CurrentState: orphanRead.NewState, Private: orphanRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleanRead.Diagnostics) || cleanRead.NewState == nil {
		t.Fatalf("read after administrator cleanup: err=%v diagnostics=%v", err, cleanRead.Diagnostics)
	}
	cleanMembers := teamMemberAddProtocolMembers(t, resourceType, cleanRead.NewState)
	if len(cleanMembers) != 1 {
		t.Fatalf("remediated read did not clear only the orphan: %#v", cleanMembers)
	}
	var stableEmail string
	if err := cleanMembers[0]["user_email"].As(&stableEmail); err != nil || stableEmail != "a-stable@example.com" {
		t.Fatalf("remediated read lost stable ownership: email=%q err=%v", stableEmail, err)
	}
	if strings.Contains(string(cleanRead.Private), teamMemberAddOrphanPrivateKey) {
		t.Fatalf("remediated read retained private orphan marker: %q", cleanRead.Private)
	}

	// Exercise the destroy branch from the same remediated checkpoint. With the
	// orphan marker and owned entry cleared, destroy finishes without a futile
	// v1.98 member_delete request.
	cleanupDestroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: nullState, PriorState: cleanRead.NewState,
		ProposedNewState: nullState, PriorPrivate: cleanRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleanupDestroyPlan.Diagnostics) {
		t.Fatalf("plan destroy immediately after cleanup: err=%v diagnostics=%v", err, cleanupDestroyPlan.Diagnostics)
	}
	cleanupDestroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: nullState, PriorState: cleanRead.NewState,
		PlannedState: cleanupDestroyPlan.PlannedState, PlannedPrivate: cleanupDestroyPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleanupDestroyed.Diagnostics) {
		t.Fatalf("destroy immediately after cleanup: err=%v diagnostics=%v", err, cleanupDestroyed.Diagnostics)
	}
	api.mu.Lock()
	deleteCallsAfterCleanupDestroy := api.deleteCalls
	api.mu.Unlock()
	if deleteCallsAfterCleanupDestroy != 1 {
		t.Fatalf("destroy after administrator cleanup sent %d delete calls, want only the stable member", deleteCallsAfterCleanupDestroy)
	}
	// Restore the stable roster entry so the recreate branch starts from the
	// same clean-read checkpoint rather than from the alternative destroy.
	api.mu.Lock()
	api.members = []mockBatchRosterMember{{ID: "resolved-a-stable-example.com", Email: "a-stable@example.com", Role: "user", BudgetID: "member-budget-resolved-a-stable-example.com", Budget: floatPtr(12)}}
	api.mu.Unlock()

	// Branch from the same clean checkpoint to exercise the recreate path.
	updatePlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: config, PriorState: cleanRead.NewState,
		ProposedNewState: proposed, PriorPrivate: cleanRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updatePlan.Diagnostics) {
		t.Fatalf("plan recreate after cleanup: err=%v diagnostics=%v", err, updatePlan.Diagnostics)
	}
	recreated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: config, PriorState: cleanRead.NewState,
		PlannedState: updatePlan.PlannedState, PlannedPrivate: updatePlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(recreated.Diagnostics) || recreated.NewState == nil {
		t.Fatalf("recreate after cleanup: err=%v diagnostics=%v", err, recreated.Diagnostics)
	}
	recreatedMembers := teamMemberAddProtocolMembers(t, resourceType, recreated.NewState)
	if len(recreatedMembers) != 2 {
		t.Fatalf("recreated members=%d, want stable member plus recreation", len(recreatedMembers))
	}
	var recreatedUserID string
	for _, member := range recreatedMembers {
		var email string
		if err := member["user_email"].As(&email); err == nil && email == "member@example.com" {
			_ = member["user_id"].As(&recreatedUserID)
		}
	}
	if recreatedUserID != partialUserID {
		t.Fatalf("recreated canonical user_id=%q, want %q", recreatedUserID, partialUserID)
	}
	noDrift, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_team_member_add", CurrentState: recreated.NewState, Private: recreated.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(noDrift.Diagnostics) || len(teamMemberAddProtocolMembers(t, resourceType, noDrift.NewState)) != 2 {
		t.Fatalf("no-drift read after recreate: err=%v diagnostics=%v", err, noDrift.Diagnostics)
	}

	destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: nullState, PriorState: noDrift.NewState,
		ProposedNewState: nullState, PriorPrivate: noDrift.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
		t.Fatalf("plan destroy: err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
	}
	destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_team_member_add", Config: nullState, PriorState: noDrift.NewState,
		PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) {
		t.Fatalf("destroy after remediation/recreate: err=%v diagnostics=%v", err, destroyed.Diagnostics)
	}
	api.mu.Lock()
	callsAfterDestroy := strings.Join(api.calls[callsBeforeCleanup:], ",")
	remainingMembers, remainingOrphans := len(api.members), len(api.orphanMemberships)
	api.mu.Unlock()
	if !strings.Contains(callsAfterDestroy, "POST /team/member_add") || !strings.Contains(callsAfterDestroy, "POST /team/member_delete") || remainingMembers != 0 || remainingOrphans != 0 {
		t.Fatalf("remediation sequence calls=%q roster=%d orphans=%d", callsAfterDestroy, remainingMembers, remainingOrphans)
	}
}

func TestTeamMemberAddPartialUpdateAndRemovalFailureRecoverState(t *testing.T) {
	t.Parallel()
	schema := teamMemberAddTestSchema(t)

	t.Run("add succeeds before native update failure", func(t *testing.T) {
		api := &mockBatchAPI{
			teamID:     "team-partial-update",
			members:    []mockBatchRosterMember{{ID: "a", Role: "user"}},
			failUpdate: true,
		}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("a", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("a", "admin"), memberByID("b", "user"))
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		if !response.Diagnostics.HasError() {
			t.Fatal("native update failure must be a hard error")
		}
		members := batchStateMembers(t, decodeBatchState(t, response.State))
		if len(members) != 2 || members[0].Role != "user" {
			t.Fatalf("partial update recovered state = %#v", members)
		}
		api.mu.Lock()
		joined := strings.Join(api.calls, ",")
		api.mu.Unlock()
		if strings.Index(joined, "POST /team/member_add") > strings.Index(joined, "POST /team/member_update") {
			t.Fatalf("add did not precede update: %q", joined)
		}
	})

	t.Run("failed planned removal remains in state", func(t *testing.T) {
		api := &mockBatchAPI{
			teamID:       "team-removal-failure",
			members:      []mockBatchRosterMember{{ID: "a", Role: "user"}, {ID: "b", Role: "user"}},
			failDeleteAt: 1,
		}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("a", "user"), memberByID("b", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("b", "user"))
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		if !response.Diagnostics.HasError() {
			t.Fatal("removal failure must be a hard error")
		}
		members := batchStateMembers(t, decodeBatchState(t, response.State))
		if len(members) != 2 {
			t.Fatalf("failed planned removal was committed to state: %#v", members)
		}
	})

	t.Run("roster-removed membership-remains partial delete is retained for manual remediation", func(t *testing.T) {
		api := &mockBatchAPI{
			teamID:                  "team-removal-membership-orphan",
			members:                 []mockBatchRosterMember{{ID: "a", Email: "a@example.com", Role: "user"}, {ID: "b", Role: "user"}},
			failDeleteAfterRosterAt: 1,
		}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByEmail("a@example.com", "user"), memberByID("b", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("b", "user"))
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Summary(), "Membership-Only Partial Delete") {
			t.Fatalf("partial delete diagnostics: %v", response.Diagnostics)
		}
		members := batchStateMembers(t, decodeBatchState(t, response.State))
		if len(members) != 2 {
			t.Fatalf("partial delete ownership state = %#v", members)
		}
		var orphan, retained *batchMember
		for index := range members {
			if members[index].UserID == "a" {
				orphan = &members[index]
			}
			if members[index].UserID == "b" {
				retained = &members[index]
			}
		}
		if orphan == nil || orphan.UserEmail != "a@example.com" || orphan.RoleKnown || retained == nil || !retained.RoleKnown {
			t.Fatalf("partial delete did not retain canonical orphan identity: %#v", members)
		}

		readResponse := &resource.ReadResponse{State: response.State}
		resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: response.State}, readResponse)
		if !readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() {
			t.Fatalf("partial delete read abandoned state: %v", readResponse.Diagnostics)
		}

		api.mu.Lock()
		deleteCallsBeforeDestroy := api.deleteCalls
		api.mu.Unlock()
		deleteResponse := &resource.DeleteResponse{State: readResponse.State}
		resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: readResponse.State}, deleteResponse)
		api.mu.Lock()
		deleteCallsAfterDestroy := api.deleteCalls
		remainingRoster := len(api.members)
		remainingOrphans := len(api.orphanMemberships)
		api.mu.Unlock()
		if !deleteResponse.Diagnostics.HasError() || deleteResponse.State.Raw.IsNull() || deleteCallsAfterDestroy != deleteCallsBeforeDestroy || remainingRoster != 1 || remainingOrphans != 1 {
			t.Fatalf("destroy after partial delete diagnostics=%v delete calls=%d/%d roster=%d orphans=%d", deleteResponse.Diagnostics, deleteCallsBeforeDestroy, deleteCallsAfterDestroy, remainingRoster, remainingOrphans)
		}
	})
}

func TestTeamMemberAddPartialDestroyAndHardFailureRetainState(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID:       "team-partial-destroy",
		members:      []mockBatchRosterMember{{ID: "a", Role: "user"}, {ID: "b", Role: "user"}},
		failDeleteAt: 2,
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("a", "user"), memberByID("b", "user"))
	state := teamMemberAddTestState(t, schema, stateModel)
	response := &resource.DeleteResponse{State: state}
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("partial destroy response: diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
	members := batchStateMembers(t, decodeBatchState(t, response.State))
	if len(members) != 1 || members[0].UserID != "b" {
		t.Fatalf("partial destroy state = %#v", members)
	}
}

func TestTeamMemberAddRetriesAndExactAbsence(t *testing.T) {
	t.Parallel()
	schema := teamMemberAddTestSchema(t)

	t.Run("post-create retries successful-response decode failures", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-decode-retry", invalidJSONReadsAfterAdd: 2}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("decode-retry-user", "user"))
		model.ID = types.StringUnknown()
		plan := teamMemberAddTestPlan(t, schema, model)
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("decode retry create diagnostics: %v", response.Diagnostics)
		}
		api.mu.Lock()
		reads := 0
		for _, call := range api.calls {
			if call == "GET /team/info" {
				reads++
			}
		}
		api.mu.Unlock()
		if reads < 5 {
			t.Fatalf("expected bounded success-decode retries, got %d GETs", reads)
		}
	})

	t.Run("post-create retries transient partial success shapes", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-partial-retry", partialReadsAfterAdd: 2}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("partial-retry-user", "user"))
		model.ID = types.StringUnknown()
		plan := teamMemberAddTestPlan(t, schema, model)
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("partial-shape retry create diagnostics: %v", response.Diagnostics)
		}
		api.mu.Lock()
		reads := 0
		for _, call := range api.calls {
			if call == "GET /team/info" {
				reads++
			}
		}
		api.mu.Unlock()
		if reads < 5 { // preflight, two partials, confirmation, final read
			t.Fatalf("expected bounded partial-shape retries, got %d GETs", reads)
		}
	})

	t.Run("post-create retries stale roster", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-retry", staleReads: 2}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("retry-user", "user"))
		model.ID = types.StringUnknown()
		plan := teamMemberAddTestPlan(t, schema, model)
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("retry create diagnostics: %v", response.Diagnostics)
		}
		api.mu.Lock()
		reads := 0
		for _, call := range api.calls {
			if call == "GET /team/info" {
				reads++
			}
		}
		api.mu.Unlock()
		if reads < 4 {
			t.Fatalf("expected stale read retries, got %d GETs", reads)
		}
	})

	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantNull   bool
		wantErrors bool
	}{
		{name: "exact 404", status: http.StatusNotFound, body: `{"error":"opaque"}`, wantNull: true},
		{name: "500 containing 404", status: http.StatusInternalServerError, body: `{"error":"upstream port 4040 unavailable"}`, wantErrors: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &mockBatchAPI{teamID: "team-absence", readStatus: test.status, readBody: test.body}
			resourceUnderTest, server := newMockBatchResource(t, api)
			defer server.Close()
			stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("a", "user"))
			state := teamMemberAddTestState(t, schema, stateModel)
			response := &resource.ReadResponse{State: state}
			resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, response)
			if response.Diagnostics.HasError() != test.wantErrors || response.State.Raw.IsNull() != test.wantNull {
				t.Fatalf("read diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
			}
			deleteResponse := &resource.DeleteResponse{State: state}
			resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, deleteResponse)
			if deleteResponse.Diagnostics.HasError() != test.wantErrors {
				t.Fatalf("delete diagnostics=%v, want error=%t", deleteResponse.Diagnostics, test.wantErrors)
			}
		})
	}
}

func TestTeamMemberAddPostWriteRetryClassification(t *testing.T) {
	t.Parallel()
	if !isRetryableTeamMemberAddReadError(&safeTransportError{kind: "safe temporary transport", retryCategory: safeTransportRetryTemporary}) {
		t.Fatal("classified temporary transport failures must be retryable after a write")
	}
	if !isRetryableTeamMemberAddReadError(&safeResponseError{statusCode: http.StatusOK, kind: "safe decode", retryable: true}) {
		t.Fatal("successful-response decode failures must be retryable after a write")
	}
	if !isRetryableTeamMemberAddReadError(&teamMemberAddPartialResponseError{detail: "omitted field", retryable: true}) {
		t.Fatal("transient partial shapes must be retryable after a write")
	}
	for name, err := range map[string]error{
		"context cancellation":          context.Canceled,
		"unclassified transport":        &safeTransportError{kind: "safe terminal transport"},
		"TLS transport":                 &safeTransportError{kind: "safe TLS transport", retryCategory: safeTransportRetryNone},
		"oversized successful response": &safeResponseError{statusCode: http.StatusOK, kind: "safety limit"},
		"semantic identity":             &teamMemberAddPartialResponseError{detail: "cross-team identity"},
		"team 404 after preflight":      &APIError{StatusCode: http.StatusNotFound},
		"other permanent client error":  &APIError{StatusCode: http.StatusBadRequest},
	} {
		if isRetryableTeamMemberAddReadError(err) {
			t.Fatalf("%s must not be retried", name)
		}
	}
	for name, err := range map[string]error{
		"408": &APIError{StatusCode: http.StatusRequestTimeout},
		"429": &APIError{StatusCode: http.StatusTooManyRequests},
		"500": &APIError{StatusCode: http.StatusInternalServerError},
	} {
		if !isRetryableTeamMemberAddReadError(err) {
			t.Fatalf("%s transient response must be retryable", name)
		}
	}
	if !errors.Is(&safeTransportError{kind: "canceled", identity: context.Canceled}, context.Canceled) {
		t.Fatal("safe transport cancellation lost context identity")
	}
}

func TestTeamMemberAddWaitStopsOnSemanticPredicateAndTeam404(t *testing.T) {
	t.Parallel()

	t.Run("predicate error", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-predicate-terminal"}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		semanticErr := errors.New("semantic identity mismatch")
		_, err := resourceUnderTest.waitForTeamMemberAddSnapshot(context.Background(), api.teamID, func(*teamMemberAddSnapshot) (bool, error) {
			return false, semanticErr
		})
		if !errors.Is(err, semanticErr) {
			t.Fatalf("predicate error = %v", err)
		}
		api.mu.Lock()
		calls := len(api.calls)
		api.mu.Unlock()
		if calls != 1 {
			t.Fatalf("semantic predicate was retried %d times", calls)
		}
	})

	t.Run("team 404", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-404-terminal", readStatus: http.StatusNotFound, readBody: `{"detail":"absent"}`}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		_, err := resourceUnderTest.waitForTeamMemberAddSnapshot(context.Background(), api.teamID, func(*teamMemberAddSnapshot) (bool, error) {
			return false, nil
		})
		if !IsAPIErrorStatus(err, http.StatusNotFound) {
			t.Fatalf("404 result = %v", err)
		}
		api.mu.Lock()
		calls := len(api.calls)
		api.mu.Unlock()
		if calls != 1 {
			t.Fatalf("team 404 after preflight was retried %d times", calls)
		}
	})
}

func TestTeamMemberAddPartialReadRetainsState(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID:       "team-partial-read",
		members:      []mockBatchRosterMember{{ID: "a", Role: "user"}},
		partialReads: 1,
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	stateModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("a", "user"))
	state := teamMemberAddTestState(t, schema, stateModel)
	response := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("partial read must retain state with error: diagnostics=%v null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
	if got := batchStateMembers(t, decodeBatchState(t, response.State)); len(got) != 1 || got[0].UserID != "a" {
		t.Fatalf("partial read state = %#v", got)
	}
}

func TestTeamMemberAddCompositeImportRejectsMissingOwnedMember(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{teamID: "team-import-missing", members: []mockBatchRosterMember{{ID: "present", Role: "user"}}}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	importID, err := formatTeamMemberAddImportID(api.teamID, []batchMember{
		{UserID: "present", HasUserID: true}, {UserID: "missing", HasUserID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	nullState := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	importResponse := &resource.ImportStateResponse{State: nullState}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: importID}, importResponse)
	readResponse := &resource.ReadResponse{State: importResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: importResponse.State}, readResponse)
	if !readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() {
		t.Fatalf("missing import identity must fail without broad adoption: diagnostics=%v null=%t", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
	}
}

func TestTeamMemberAddCompositeImportUnknownRoleIsNotOwnedOrphan(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID:            "team-import-membership-only",
		orphanMemberships: []mockBatchMembership{{UserID: "imported-user"}},
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	importID, err := formatTeamMemberAddImportID(api.teamID, []batchMember{{UserID: "imported-user", HasUserID: true}})
	if err != nil {
		t.Fatal(err)
	}
	nullState := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	importResponse := &resource.ImportStateResponse{State: nullState}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: importID}, importResponse)

	readResponse := &resource.ReadResponse{State: importResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: importResponse.State}, readResponse)
	if !readResponse.Diagnostics.HasError() || !strings.Contains(readResponse.Diagnostics.Errors()[0].Summary(), "Imported Batch") {
		t.Fatalf("composite membership-only read diagnostics: %v", readResponse.Diagnostics)
	}
	members := batchStateMembers(t, decodeBatchState(t, readResponse.State))
	if len(members) != 1 || members[0].RoleKnown || members[0].UserID != "imported-user" {
		t.Fatalf("composite import ownership changed: %#v", members)
	}

	plannedModel := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("imported-user", "user"))
	plan := teamMemberAddTestPlan(t, schema, plannedModel)
	updateResponse := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, plannedModel)}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: readResponse.State}, updateResponse)
	deleteResponse := &resource.DeleteResponse{State: updateResponse.State}
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: updateResponse.State}, deleteResponse)
	api.mu.Lock()
	addCalls, updateCalls, deleteCalls := api.addCalls, api.updateCalls, api.deleteCalls
	api.mu.Unlock()
	if !updateResponse.Diagnostics.HasError() || !deleteResponse.Diagnostics.HasError() || addCalls != 0 || updateCalls != 0 || deleteCalls != 0 {
		t.Fatalf("composite unresolved lifecycle update=%v delete=%v calls=%d/%d/%d", updateResponse.Diagnostics, deleteResponse.Diagnostics, addCalls, updateCalls, deleteCalls)
	}
}

func TestTeamMemberAddEmailVsIDAliasStopsBeforeSecondMutation(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID:   "team-alias-create",
		idEmails: map[string]string{"user-1": "same@example.com"},
		emailIDs: map[string]string{"same@example.com": "user-1"},
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	model := batchModelWithMembers(t, api.teamID, types.Float64Null(), memberByID("user-1", "user"), memberByEmail("same@example.com", "user"))
	model.ID = types.StringUnknown()
	plan := teamMemberAddTestPlan(t, schema, model)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("email-vs-ID alias must fail")
	}
	api.mu.Lock()
	addCalls := api.addCalls
	api.mu.Unlock()
	if addCalls != 1 {
		t.Fatalf("alias should stop before a second mutation, add calls=%d", addCalls)
	}
	members := batchStateMembers(t, decodeBatchState(t, response.State))
	if len(members) != 1 {
		t.Fatalf("alias recovery state = %#v", members)
	}
}

func TestTeamMemberAddDecodesExactMetadataTeamMemberBudgetID(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		metadata string
		want     string
	}{
		{name: "exact value", metadata: `{"team_member_budget_id":"current-budget-not-team-id"}`, want: "current-budget-not-team-id"},
		{name: "missing key", metadata: `{}`},
		{name: "null metadata", metadata: `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{"team_id":"team-id","team_info":{"team_id":"team-id","metadata":` + test.metadata + `,"members_with_roles":[]},"team_memberships":[]}`)
			snapshot, err := decodeTeamMemberAddSnapshot(raw, "team-id")
			if err != nil || snapshot.TeamBudgetID != test.want {
				t.Fatalf("TeamBudgetID=%q, want %q, err=%v", snapshot.TeamBudgetID, test.want, err)
			}
		})
	}

	malformed := json.RawMessage(`{"team_id":"team-id","team_info":{"team_id":"team-id","metadata":{"team_member_budget_id":7},"members_with_roles":[]},"team_memberships":[]}`)
	_, err := decodeTeamMemberAddSnapshot(malformed, "team-id")
	var partialErr *teamMemberAddPartialResponseError
	if !errors.As(err, &partialErr) || !partialErr.retryable {
		t.Fatalf("malformed metadata must be a bounded transient shape error: %v", err)
	}
}

func TestTeamMemberAddBudgetDivergenceIsSafeError(t *testing.T) {
	t.Parallel()
	api := &mockBatchAPI{
		teamID: "team-budget-divergence",
		members: []mockBatchRosterMember{
			{ID: "a", Role: "user", Budget: floatPtr(1)},
			{ID: "b", Role: "user", Budget: floatPtr(2)},
		},
	}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	schema := teamMemberAddTestSchema(t)
	stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(1), memberByID("a", "user"), memberByID("b", "user"))
	state := teamMemberAddTestState(t, schema, stateModel)
	response := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("divergent shared budgets must retain state with an error: diagnostics=%v", response.Diagnostics)
	}
}

func TestTeamMemberAddBudgetReferenceSafetyAndGrouping(t *testing.T) {
	t.Parallel()
	schema := teamMemberAddTestSchema(t)

	t.Run("metadata current team-member default clones only selected member", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-current-default", teamMemberBudgetID: "actual-current-member-budget"}
		api.members = []mockBatchRosterMember{
			{ID: "owned", Role: "user", BudgetID: api.teamMemberBudgetID, Budget: floatPtr(1)},
			{ID: "unrelated", Role: "user", BudgetID: api.teamMemberBudgetID, Budget: floatPtr(1)},
		}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(1), memberByID("owned", "user"))
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Value(2), memberByID("owned", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("current-default clone diagnostics: %v", response.Diagnostics)
		}
		api.mu.Lock()
		owned, unrelated, calls := api.members[0], api.members[1], api.updateCalls
		api.mu.Unlock()
		if calls != 1 || owned.BudgetID == api.teamMemberBudgetID || owned.Budget == nil || *owned.Budget != 2 || unrelated.BudgetID != api.teamMemberBudgetID || unrelated.Budget == nil || *unrelated.Budget != 1 {
			t.Fatalf("current-default clone owned=%#v unrelated=%#v calls=%d", owned, unrelated, calls)
		}
	})

	t.Run("team ID is never inferred as current default", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-id-is-historical", teamMemberBudgetID: "different-current-budget", members: []mockBatchRosterMember{
			{ID: "owned", Role: "user", BudgetID: "team-id-is-historical", Budget: floatPtr(1)},
			{ID: "unrelated", Role: "user", BudgetID: "team-id-is-historical", Budget: floatPtr(1)},
		}}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(1), memberByID("owned", "user"))
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Value(2), memberByID("owned", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		api.mu.Lock()
		calls := api.updateCalls
		api.mu.Unlock()
		if !response.Diagnostics.HasError() || calls != 0 {
			t.Fatalf("team-ID budget was treated as current default: diagnostics=%v calls=%d", response.Diagnostics, calls)
		}
	})

	t.Run("historical row shared with unrelated membership-only row is refused", func(t *testing.T) {
		api := &mockBatchAPI{
			teamID:            "team-historical-unrelated",
			members:           []mockBatchRosterMember{{ID: "owned", Role: "user", BudgetID: "historical", Budget: floatPtr(1)}},
			orphanMemberships: []mockBatchMembership{{UserID: "unrelated", BudgetID: "historical", Budget: floatPtr(1)}},
		}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(1), memberByID("owned", "user"))
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Value(2), memberByID("owned", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		api.mu.Lock()
		calls := api.updateCalls
		api.mu.Unlock()
		if !response.Diagnostics.HasError() || calls != 0 {
			t.Fatalf("unsafe historical write diagnostics=%v calls=%d", response.Diagnostics, calls)
		}
	})

	t.Run("historical row shared only by retained owned members updates once", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-historical-owned", members: []mockBatchRosterMember{
			{ID: "a", Role: "user", BudgetID: "historical-owned", Budget: floatPtr(1)},
			{ID: "b", Role: "user", BudgetID: "historical-owned", Budget: floatPtr(1)},
		}}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(1), memberByID("a", "user"), memberByID("b", "user"))
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Value(2), memberByID("a", "user"), memberByID("b", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		api.mu.Lock()
		calls, a, b := api.updateCalls, api.members[0], api.members[1]
		api.mu.Unlock()
		if response.Diagnostics.HasError() || calls != 1 || a.Budget == nil || b.Budget == nil || *a.Budget != 2 || *b.Budget != 2 {
			t.Fatalf("owned grouped update diagnostics=%v calls=%d a=%#v b=%#v", response.Diagnostics, calls, a, b)
		}
	})

	t.Run("shared row with removal and budget change is incompatible", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-historical-removal", members: []mockBatchRosterMember{
			{ID: "a", Role: "user", BudgetID: "historical-removal", Budget: floatPtr(1)},
			{ID: "b", Role: "user", BudgetID: "historical-removal", Budget: floatPtr(1)},
		}}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(1), memberByID("a", "user"), memberByID("b", "user"))
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Value(2), memberByID("a", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		api.mu.Lock()
		updates, deletes := api.updateCalls, api.deleteCalls
		api.mu.Unlock()
		if !response.Diagnostics.HasError() || updates != 0 || deletes != 0 {
			t.Fatalf("incompatible shared change diagnostics=%v updates=%d deletes=%d", response.Diagnostics, updates, deletes)
		}
	})

	t.Run("partial grouped writes retain representable ownership state", func(t *testing.T) {
		api := &mockBatchAPI{teamID: "team-budget-partial", failUpdateAt: 2, members: []mockBatchRosterMember{
			{ID: "a", Role: "user", BudgetID: "budget-a", Budget: floatPtr(1)},
			{ID: "b", Role: "user", BudgetID: "budget-b", Budget: floatPtr(1)},
		}}
		resourceUnderTest, server := newMockBatchResource(t, api)
		defer server.Close()
		stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(1), memberByID("a", "user"), memberByID("b", "user"))
		planModel := batchModelWithMembers(t, api.teamID, types.Float64Value(2), memberByID("a", "user"), memberByID("b", "user"))
		state := teamMemberAddTestState(t, schema, stateModel)
		plan := teamMemberAddTestPlan(t, schema, planModel)
		response := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, planModel)}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response)
		recovered := decodeBatchState(t, response.State)
		if !response.Diagnostics.HasError() || response.State.Raw.IsNull() || len(batchStateMembers(t, recovered)) != 2 || recovered.MaxBudgetInTeam.ValueFloat64() != 1 {
			t.Fatalf("partial budget state diagnostics=%v state=%#v", response.Diagnostics, recovered)
		}
		api.mu.Lock()
		a, b := api.members[0], api.members[1]
		api.mu.Unlock()
		if a.Budget == nil || b.Budget == nil || *a.Budget != 2 || *b.Budget != 1 {
			t.Fatalf("partial remote budgets a=%#v b=%#v", a, b)
		}
	})
}

func TestTeamMemberAddBudgetSerializerAndUnmanagedOmission(t *testing.T) {
	t.Parallel()
	schema := teamMemberAddTestSchema(t)

	api := &mockBatchAPI{teamID: "team-budget-serializer", omitBudgetTable: true, members: []mockBatchRosterMember{{ID: "a", Role: "user", BudgetID: "budget-a", Budget: floatPtr(5)}}}
	resourceUnderTest, server := newMockBatchResource(t, api)
	defer server.Close()
	stateModel := batchModelWithMembers(t, api.teamID, types.Float64Value(9), memberByID("a", "user"))
	state := teamMemberAddTestState(t, schema, stateModel)
	omittedResponse := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, omittedResponse)
	omittedState := decodeBatchState(t, omittedResponse.State)
	if !omittedResponse.Diagnostics.HasError() || omittedState.MaxBudgetInTeam.ValueFloat64() != 9 {
		t.Fatalf("omitted budget relation cleared state: diagnostics=%v state=%#v", omittedResponse.Diagnostics, omittedState)
	}

	api.mu.Lock()
	api.omitBudgetTable = false
	api.members[0].BudgetID = ""
	api.members[0].Budget = nil
	api.mu.Unlock()
	nullResponse := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, nullResponse)
	if nullResponse.Diagnostics.HasError() || !decodeBatchState(t, nullResponse.State).MaxBudgetInTeam.IsNull() {
		t.Fatalf("explicit null budget relation was not authoritative: %v", nullResponse.Diagnostics)
	}

	removalAPI := &mockBatchAPI{teamID: "team-budget-omitted-update", members: []mockBatchRosterMember{{ID: "a", Role: "user", BudgetID: "budget-a", Budget: floatPtr(5)}}}
	removalResource, removalServer := newMockBatchResource(t, removalAPI)
	defer removalServer.Close()
	managedModel := batchModelWithMembers(t, removalAPI.teamID, types.Float64Value(5), memberByID("a", "user"))
	omittedModel := batchModelWithMembers(t, removalAPI.teamID, types.Float64Null(), memberByID("a", "user"))
	managedState := teamMemberAddTestState(t, schema, managedModel)
	omittedPlan := teamMemberAddTestPlan(t, schema, omittedModel)
	omittedUpdate := &resource.UpdateResponse{State: teamMemberAddTestState(t, schema, omittedModel)}
	removalResource.Update(context.Background(), resource.UpdateRequest{Plan: omittedPlan, State: managedState}, omittedUpdate)
	removalAPI.mu.Lock()
	updateCalls, remoteBudget := removalAPI.updateCalls, removalAPI.members[0].Budget
	removalAPI.mu.Unlock()
	if omittedUpdate.Diagnostics.HasError() || updateCalls != 0 || !decodeBatchState(t, omittedUpdate.State).MaxBudgetInTeam.IsNull() || remoteBudget == nil || *remoteBudget != 5 {
		t.Fatalf("omitted update managed budget unexpectedly: diagnostics=%v calls=%d remote=%v", omittedUpdate.Diagnostics, updateCalls, remoteBudget)
	}

	unmanagedAPI := &mockBatchAPI{teamID: "team-budget-unmanaged", members: []mockBatchRosterMember{
		{ID: "a", Role: "user", BudgetID: "budget-a", Budget: floatPtr(1)},
		{ID: "b", Role: "user", BudgetID: "budget-b", Budget: floatPtr(2)},
	}}
	unmanagedResource, unmanagedServer := newMockBatchResource(t, unmanagedAPI)
	defer unmanagedServer.Close()
	unmanagedModel := batchModelWithMembers(t, unmanagedAPI.teamID, types.Float64Null(), memberByID("a", "user"), memberByID("b", "user"))
	unmanagedState := teamMemberAddTestState(t, schema, unmanagedModel)
	unmanagedResponse := &resource.ReadResponse{State: unmanagedState}
	unmanagedResource.Read(context.Background(), resource.ReadRequest{State: unmanagedState}, unmanagedResponse)
	if unmanagedResponse.Diagnostics.HasError() || !decodeBatchState(t, unmanagedResponse.State).MaxBudgetInTeam.IsNull() {
		t.Fatalf("omitted budget unexpectedly detected drift: %v", unmanagedResponse.Diagnostics)
	}
}

func Example_formatTeamMemberAddImportID() {
	identity, _ := formatTeamMemberAddImportID("team-1", []batchMember{
		{UserID: "user-1", HasUserID: true},
		{UserEmail: "dev@example.com", HasUserEmail: true},
	})
	fmt.Println(identity)
	// Output: v1.dGVhbS0x.e~ZGV2QGV4YW1wbGUuY29t,i~dXNlci0x
}
