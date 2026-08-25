package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func organizationMemberTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&OrganizationMemberResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func organizationMemberTestState(t *testing.T, schema resourceschema.Schema, data OrganizationMemberResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test state: %v", diagnostics)
	}
	return state
}

func organizationMemberTestPlan(t *testing.T, schema resourceschema.Schema, data OrganizationMemberResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	return plan
}

func organizationMemberTestClient(server *httptest.Server) *Client {
	return &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
}

func organizationMemberProtocolDynamicValue(t *testing.T, schemaType tftypes.Type, value tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	dynamic, err := tfprotov6.NewDynamicValue(schemaType, value)
	if err != nil {
		t.Fatalf("build protocol dynamic value: %v", err)
	}
	return &dynamic
}

func organizationMemberProtocolObject(schemaType tftypes.Type, id, budget interface{}) tftypes.Value {
	return tftypes.NewValue(schemaType, map[string]tftypes.Value{
		"id":                         tftypes.NewValue(tftypes.String, id),
		"organization_id":            tftypes.NewValue(tftypes.String, "org-1"),
		"user_id":                    tftypes.NewValue(tftypes.String, "user-1"),
		"user_email":                 tftypes.NewValue(tftypes.String, nil),
		"role":                       tftypes.NewValue(tftypes.String, "internal_user"),
		"max_budget_in_organization": tftypes.NewValue(tftypes.Number, budget),
	})
}

func organizationMemberJSON(organizationID, userID, email, role, budgetID string, maxBudget *float64) map[string]interface{} {
	member := map[string]interface{}{
		"organization_id": organizationID,
		"user_id":         userID,
		"user_email":      email,
		"user_role":       role,
	}
	if budgetID == "" {
		member["budget_id"] = nil
		member["litellm_budget_table"] = nil
	} else {
		member["budget_id"] = budgetID
		member["litellm_budget_table"] = map[string]interface{}{
			"budget_id":  budgetID,
			"max_budget": maxBudget,
		}
	}
	return member
}

func TestOrganizationMemberRolesMatchV198Exactly(t *testing.T) {
	t.Parallel()

	want := []string{"org_admin", "internal_user", "internal_user_viewer"}
	if !reflect.DeepEqual(organizationMemberRoles, want) {
		t.Fatalf("organizationMemberRoles = %#v, want %#v", organizationMemberRoles, want)
	}
	for _, role := range want {
		if !isOrganizationMemberRole(role) {
			t.Errorf("expected %q to be accepted", role)
		}
	}
	for _, role := range []string{"proxy_admin", "proxy_admin_viewer", "admin", "user", ""} {
		if isOrganizationMemberRole(role) {
			t.Errorf("did not expect %q to be accepted", role)
		}
	}
}

func TestOrganizationMemberBudgetRemovalPlansReplacementThroughProtocol(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := organizationMemberTestSchema(t)
	schemaType := schema.Type().TerraformType(ctx)
	server := providerserver.NewProtocol6(New("test")())()
	if _, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{}); err != nil {
		t.Fatalf("get provider schema: %v", err)
	}

	tests := []struct {
		name           string
		stateBudget    interface{}
		configBudget   interface{}
		proposedBudget interface{}
		wantReplace    bool
	}{
		{name: "known configured value removed", stateBudget: float64(50), configBudget: nil, proposedBudget: nil, wantReplace: true},
		{name: "known value changed", stateBudget: float64(50), configBudget: float64(75), proposedBudget: float64(75)},
		{name: "known value unchanged", stateBudget: float64(50), configBudget: float64(50), proposedBudget: float64(50)},
		{name: "import-compatible null remains null", stateBudget: nil, configBudget: nil, proposedBudget: nil},
		{name: "unknown prior remains compatible", stateBudget: tftypes.UnknownValue, configBudget: nil, proposedBudget: nil},
		{name: "unknown configuration remains compatible", stateBudget: float64(50), configBudget: tftypes.UnknownValue, proposedBudget: tftypes.UnknownValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := organizationMemberProtocolObject(schemaType, nil, test.configBudget)
			prior := organizationMemberProtocolObject(schemaType, "org-1:user-1", test.stateBudget)
			proposed := organizationMemberProtocolObject(schemaType, "org-1:user-1", test.proposedBudget)
			response, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_organization_member",
				Config:           organizationMemberProtocolDynamicValue(t, schemaType, config),
				PriorState:       organizationMemberProtocolDynamicValue(t, schemaType, prior),
				ProposedNewState: organizationMemberProtocolDynamicValue(t, schemaType, proposed),
			})
			if err != nil {
				t.Fatalf("plan resource change: %v", err)
			}
			if len(response.Diagnostics) != 0 {
				t.Fatalf("planning diagnostics: %#v", response.Diagnostics)
			}
			if got := len(response.RequiresReplace) > 0; got != test.wantReplace {
				t.Fatalf("RequiresReplace=%v, want replacement %t", response.RequiresReplace, test.wantReplace)
			}
			if test.wantReplace {
				wantPath := tftypes.NewAttributePath().WithAttributeName("max_budget_in_organization")
				if len(response.RequiresReplace) != 1 || !response.RequiresReplace[0].Equal(wantPath) {
					t.Fatalf("replacement paths=%v, want %s", response.RequiresReplace, wantPath)
				}
			}
		})
	}

	t.Run("ordinary create does not replace", func(t *testing.T) {
		config := organizationMemberProtocolObject(schemaType, nil, float64(50))
		proposed := organizationMemberProtocolObject(schemaType, tftypes.UnknownValue, float64(50))
		nullPrior := tftypes.NewValue(schemaType, nil)
		response, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
			TypeName:         "litellm_organization_member",
			Config:           organizationMemberProtocolDynamicValue(t, schemaType, config),
			PriorState:       organizationMemberProtocolDynamicValue(t, schemaType, nullPrior),
			ProposedNewState: organizationMemberProtocolDynamicValue(t, schemaType, proposed),
		})
		if err != nil {
			t.Fatalf("plan create: %v", err)
		}
		if len(response.Diagnostics) != 0 || len(response.RequiresReplace) != 0 {
			t.Fatalf("create diagnostics=%#v replacement=%v", response.Diagnostics, response.RequiresReplace)
		}
	})
}

func TestOrganizationMemberMutationPayloads(t *testing.T) {
	t.Parallel()

	data := &OrganizationMemberResourceModel{
		OrganizationID:          types.StringValue("org-1"),
		UserID:                  types.StringValue("user-1"),
		UserEmail:               types.StringValue("user@example.com"),
		Role:                    types.StringValue("org_admin"),
		MaxBudgetInOrganization: types.Float64Value(125),
	}
	add, err := buildOrganizationMemberAddRequest(data)
	if err != nil {
		t.Fatalf("build add request: %v", err)
	}
	wantAdd := map[string]interface{}{
		"organization_id": "org-1",
		"member": map[string]interface{}{
			"user_id":    "user-1",
			"user_email": "user@example.com",
			"role":       "org_admin",
		},
	}
	if !reflect.DeepEqual(add, wantAdd) {
		t.Fatalf("add request = %#v, want %#v", add, wantAdd)
	}
	if _, exists := add["max_budget_in_organization"]; exists {
		t.Fatal("member_add must omit the budget field that LiteLLM v1.98 ignores")
	}

	update, err := buildOrganizationMemberUpdateRequest(data)
	if err != nil {
		t.Fatalf("build update request: %v", err)
	}
	wantUpdate := map[string]interface{}{
		"organization_id":            "org-1",
		"user_id":                    "user-1",
		"role":                       "org_admin",
		"max_budget_in_organization": float64(125),
	}
	if !reflect.DeepEqual(update, wantUpdate) {
		t.Fatalf("flat update request = %#v, want %#v", update, wantUpdate)
	}
	if _, nested := update["member"]; nested {
		t.Fatal("member_update must not contain the old nested member payload")
	}
	if _, sentEmail := update["user_email"]; sentEmail {
		t.Fatal("user_id must take precedence when both identities are known")
	}

	deleteRequest, err := buildOrganizationMemberDeleteRequest(data)
	if err != nil {
		t.Fatalf("build delete request: %v", err)
	}
	if !reflect.DeepEqual(deleteRequest, map[string]interface{}{"organization_id": "org-1", "user_id": "user-1"}) {
		t.Fatalf("delete request = %#v", deleteRequest)
	}

	emailOnly := *data
	emailOnly.UserID = types.StringNull()
	update, err = buildOrganizationMemberUpdateRequest(&emailOnly)
	if err != nil {
		t.Fatalf("build email update request: %v", err)
	}
	if update["user_email"] != "user@example.com" || update["user_id"] != nil {
		t.Fatalf("email update identity = %#v", update)
	}
	deleteRequest, err = buildOrganizationMemberDeleteRequest(&emailOnly)
	if err != nil || deleteRequest["user_email"] != "user@example.com" {
		t.Fatalf("email delete request = %#v, err=%v", deleteRequest, err)
	}

	missing := emailOnly
	missing.UserEmail = types.StringUnknown()
	if _, err := buildOrganizationMemberAddRequest(&missing); err == nil {
		t.Fatal("expected unknown/empty identity to be rejected")
	}
}

func TestApplyOrganizationMemberResponseUsesAuthoritativeRoleAndNestedBudget(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"organization_id":"org-1",
		"user_id":"user-1",
		"user_email":"remote@example.com",
		"role":"internal_user",
		"user_role":"org_admin",
		"budget_id":"budget-1",
		"max_budget_in_organization":999,
		"litellm_budget_table":{"budget_id":"budget-1","max_budget":42}
	}`)
	var member organizationMemberAPIModel
	if err := json.Unmarshal(raw, &member); err != nil {
		t.Fatalf("decode member fixture: %v", err)
	}
	data := OrganizationMemberResourceModel{
		OrganizationID: types.StringValue("org-1"),
		UserEmail:      types.StringValue("configured@example.com"),
	}
	if err := applyOrganizationMemberResponse(&data, member); err != nil {
		t.Fatalf("apply member: %v", err)
	}
	if data.Role.ValueString() != "org_admin" {
		t.Fatalf("role = %q, want authoritative user_role org_admin", data.Role.ValueString())
	}
	if data.MaxBudgetInOrganization.ValueFloat64() != 42 {
		t.Fatalf("budget = %v, want nested max_budget 42", data.MaxBudgetInOrganization)
	}
	if data.UserEmail.ValueString() != "configured@example.com" {
		t.Fatalf("configured email was overwritten: %v", data.UserEmail)
	}
	if data.ID.ValueString() != "org-1:user-1" || data.UserID.ValueString() != "user-1" {
		t.Fatalf("canonical identity not applied: id=%v user_id=%v", data.ID, data.UserID)
	}
}

func TestApplyOrganizationMemberResponseRejectsMalformedLoadedContracts(t *testing.T) {
	t.Parallel()

	role := "internal_user"
	budgetID := "budget-1"
	otherBudgetID := "other"
	maxBudget := float64(5)
	tests := map[string]organizationMemberAPIModel{
		"missing user_role": {
			UserID: "user-1", OrganizationID: "org-1",
		},
		"unsupported user_role": {
			UserID: "user-1", OrganizationID: "org-1", UserRole: stringPointer("proxy_admin"),
		},
		"mismatched nested budget id": {
			UserID: "user-1", OrganizationID: "org-1", UserRole: &role, BudgetID: &budgetID,
			LiteLLMBudgetTable: &organizationMemberBudgetAPIModel{BudgetID: &otherBudgetID, MaxBudget: &maxBudget},
		},
		"orphan nested budget id": {
			UserID: "user-1", OrganizationID: "org-1", UserRole: &role,
			LiteLLMBudgetTable: &organizationMemberBudgetAPIModel{BudgetID: &budgetID, MaxBudget: &maxBudget},
		},
	}
	for name, member := range tests {
		name, member := name, member
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := OrganizationMemberResourceModel{OrganizationID: types.StringValue("org-1")}
			if err := applyOrganizationMemberResponse(&data, member); err == nil {
				t.Fatal("expected malformed contract to fail")
			}
		})
	}
}

func TestApplyOrganizationMemberResponsePreservesBudgetWhenNestedRelationIsUnavailable(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"omitted": `{"organization_id":"org-1","user_id":"user-1","user_role":"internal_user","budget_id":"budget-1"}`,
		"null":    `{"organization_id":"org-1","user_id":"user-1","user_role":"internal_user","budget_id":"budget-1","litellm_budget_table":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := OrganizationMemberResourceModel{
				OrganizationID:          types.StringValue("org-1"),
				MaxBudgetInOrganization: types.Float64Value(37),
			}
			var member organizationMemberAPIModel
			if err := json.Unmarshal([]byte(raw), &member); err != nil {
				t.Fatalf("decode member fixture: %v", err)
			}
			err := applyOrganizationMemberResponse(&data, member)
			if err != nil {
				t.Fatalf("unavailable nested relation: %v", err)
			}
			if data.MaxBudgetInOrganization.IsNull() || data.MaxBudgetInOrganization.ValueFloat64() != 37 {
				t.Fatalf("preserved budget = %v, want 37", data.MaxBudgetInOrganization)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

func TestDecodeOrganizationMemberAddResponseValidatesEnvelopeIdentityAndMembership(t *testing.T) {
	t.Parallel()

	data := OrganizationMemberResourceModel{
		OrganizationID: types.StringValue("org-1"),
		UserID:         types.StringUnknown(),
		UserEmail:      types.StringValue("user@example.com"),
		Role:           types.StringValue("internal_user"),
	}
	valid := []byte(`{
		"organization_id":"org-1",
		"updated_users":[{"user_id":"resolved-user","user_email":"user@example.com"}],
		"updated_organization_memberships":[{
			"organization_id":"org-1","user_id":"resolved-user","user_role":"internal_user",
			"budget_id":null,"litellm_budget_table":null
		}]
	}`)
	var validResponse organizationMemberAddAPIResponse
	if err := json.Unmarshal(valid, &validResponse); err != nil {
		t.Fatalf("decode add fixture: %v", err)
	}
	member, err := validateOrganizationMemberAddResponse(validResponse, &data)
	if err != nil || member.UserID != "resolved-user" {
		t.Fatalf("valid add response member=%#v err=%v", member, err)
	}

	malformed := [][]byte{
		[]byte(`{"organization_id":"org-2","updated_users":[],"updated_organization_memberships":[]}`),
		[]byte(`{"organization_id":"org-1","updated_users":[{"user_id":"other","user_email":"user@example.com"}],"updated_organization_memberships":[{"organization_id":"org-1","user_id":"resolved-user","user_role":"internal_user","litellm_budget_table":null}]}`),
		[]byte(`{"organization_id":"org-1","updated_users":[{"user_id":"resolved-user","user_email":"other@example.com"}],"updated_organization_memberships":[{"organization_id":"org-1","user_id":"resolved-user","user_role":"internal_user","litellm_budget_table":null}]}`),
		[]byte(`{"organization_id":"org-1","updated_users":[{"user_id":"resolved-user","user_email":"user@example.com"}],"updated_organization_memberships":[{"organization_id":"org-1","user_id":"resolved-user","litellm_budget_table":null}]}`),
	}
	for index, raw := range malformed {
		var response organizationMemberAddAPIResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatalf("decode malformed fixture %d: %v", index, err)
		}
		if _, err := validateOrganizationMemberAddResponse(response, &data); err == nil {
			t.Errorf("malformed add response %d was accepted: %s", index, raw)
		}
	}
}

func TestOrganizationAdminReadUsesPrimaryMembershipProofWithoutBudgetInfo(t *testing.T) {
	t.Parallel()

	const organizationID = "org/group #1&ops"
	for name, auxiliaryStatus := range map[string]int{
		"org-admin authorization denial": http.StatusForbidden,
		"auxiliary not found":            http.StatusNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			budgetInfoCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
					if got := request.URL.Query().Get("organization_id"); got != organizationID {
						t.Errorf("organization query = %q, want %q", got, organizationID)
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{
						"organization_id": organizationID,
						"members": []interface{}{
							map[string]interface{}{
								"organization_id":      organizationID,
								"user_id":              "user-1",
								"user_email":           "remote@example.com",
								"user_role":            "org_admin",
								"budget_id":            "budget-1",
								"litellm_budget_table": nil,
							},
						},
					})
				case request.Method == http.MethodPost && request.URL.Path == "/budget/info":
					// Forbidden is the valid v1.98 result for org-admin credentials;
					// a 404 is also never membership evidence. This resource has no
					// reason to call either auxiliary route.
					budgetInfoCalls++
					http.Error(writer, `{"error":"budget info unavailable"}`, auxiliaryStatus)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			schema := organizationMemberTestSchema(t)
			state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
				ID:                      types.StringValue(organizationID + ":user-1"),
				OrganizationID:          types.StringValue(organizationID),
				UserID:                  types.StringValue("user-1"),
				UserEmail:               types.StringValue("configured@example.com"),
				Role:                    types.StringValue("internal_user"),
				MaxBudgetInOrganization: types.Float64Value(88),
			})
			response := &resource.ReadResponse{State: state}
			(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Read(
				context.Background(), resource.ReadRequest{State: state}, response,
			)
			if response.Diagnostics.HasError() || response.State.Raw.IsNull() {
				t.Fatalf("refresh diagnostics=%v state_null=%t", response.Diagnostics, response.State.Raw.IsNull())
			}
			var refreshed OrganizationMemberResourceModel
			if diagnostics := response.State.Get(context.Background(), &refreshed); diagnostics.HasError() {
				t.Fatalf("decode refreshed state: %v", diagnostics)
			}
			if refreshed.Role.ValueString() != "org_admin" || refreshed.MaxBudgetInOrganization.ValueFloat64() != 88 {
				t.Fatalf("primary membership read role=%v budget=%v", refreshed.Role, refreshed.MaxBudgetInOrganization)
			}
			if refreshed.UserEmail.ValueString() != "configured@example.com" {
				t.Fatalf("read unexpectedly began managing user email: %v", refreshed.UserEmail)
			}
			if budgetInfoCalls != 0 {
				t.Fatalf("member refresh called unavailable /budget/info %d times", budgetInfoCalls)
			}
		})
	}
}

func TestReadOrganizationMemberEmailIdentityHydratesCanonicalUserID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"organization_id": "org-1",
			"members": []interface{}{
				organizationMemberJSON("org-1", "other", "target@example.com", "internal_user", "", nil),
			},
		})
	}))
	defer server.Close()

	data := OrganizationMemberResourceModel{
		OrganizationID: types.StringValue("org-1"),
		UserID:         types.StringNull(),
		UserEmail:      types.StringValue("target@example.com"),
	}
	exists, err := (&OrganizationMemberResource{client: organizationMemberTestClient(server)}).readOrganizationMember(context.Background(), &data)
	if err != nil || !exists {
		t.Fatalf("read by email exists=%v err=%v", exists, err)
	}
	if data.UserID.ValueString() != "other" || data.ID.ValueString() != "org-1:other" {
		t.Fatalf("email identity was not canonicalized: user_id=%v id=%v", data.UserID, data.ID)
	}
}

func TestOrganizationMemberCreateRetainsEmailFallbackCanonicalIdentity(t *testing.T) {
	t.Parallel()

	created := false
	var gets int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			gets++
			members := []interface{}{}
			if created {
				members = append(members, organizationMemberJSON("org-1", "existing-email-id", "member@example.com", "internal_user", "", nil))
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": "org-1", "members": members})
		case request.Method == http.MethodPost && request.URL.Path == "/organization/member_add":
			created = true
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"organization_id":                  "org-1",
				"updated_users":                    []interface{}{map[string]interface{}{"user_id": "existing-email-id", "user_email": "member@example.com"}},
				"updated_organization_memberships": []interface{}{organizationMemberJSON("org-1", "existing-email-id", "member@example.com", "internal_user", "", nil)},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	plan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID:                      types.StringUnknown(),
		OrganizationID:          types.StringValue("org-1"),
		UserID:                  types.StringValue("requested-new-id"),
		UserEmail:               types.StringValue("member@example.com"),
		Role:                    types.StringValue("internal_user"),
		MaxBudgetInOrganization: types.Float64Null(),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Create(
		context.Background(), resource.CreateRequest{Plan: plan}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected canonical user_id mismatch diagnostic")
	}
	var retained OrganizationMemberResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatalf("decode retained state: %v", diagnostics)
	}
	if retained.UserID.ValueString() != "existing-email-id" || retained.ID.ValueString() != "org-1:existing-email-id" {
		t.Fatalf("email-fallback membership was not retained canonically: %#v", retained)
	}
	if gets != 4 {
		t.Fatalf("organization info reads = %d, want primary+email preflight and recovery", gets)
	}
}

func TestMatchOrganizationMemberIdentityPrecedence(t *testing.T) {
	t.Parallel()

	if !matchOrganizationMember("user-1", "a@example.com", "user-1", "other@example.com") {
		t.Fatal("expected user_id to match when both targets are provided")
	}
	if matchOrganizationMember("user-2", "a@example.com", "user-1", "a@example.com") {
		t.Fatal("email must not override a mismatched canonical user_id")
	}
	if !matchOrganizationMember("user-1", "a@example.com", "", "a@example.com") {
		t.Fatal("expected email-only identity to match")
	}
	if matchOrganizationMember("user-1", "a@example.com", "", "") {
		t.Fatal("empty target identity must not match")
	}
}

func TestOrganizationMemberLifecyclePersistsBudgetThroughFlatUpdate(t *testing.T) {
	// The handler is intentionally stateful and this test is not parallel.
	const organizationID = "org-1"
	const userID = "resolved-user"
	const email = "member@example.com"
	memberExists := false
	role := ""
	var budget *float64
	budgetID := ""
	var calls []string
	var patchBodies []map[string]interface{}

	currentMember := func() map[string]interface{} {
		return organizationMemberJSON(organizationID, userID, email, role, budgetID, budget)
	}
	organizationInfoMember := func() map[string]interface{} {
		member := currentMember()
		// Exact v1.98 organization/info proves membership but does not load
		// litellm_budget_table. The update response above is the last nested
		// read-back available to an organization admin.
		if budgetID != "" {
			member["litellm_budget_table"] = nil
		}
		return member
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			members := []interface{}{}
			if memberExists {
				members = append(members, organizationInfoMember())
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": organizationID, "members": members})
		case request.Method == http.MethodPost && request.URL.Path == "/organization/member_add":
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode add: %v", err)
			}
			if _, sent := body["max_budget_in_organization"]; sent {
				t.Error("member_add sent ignored max_budget_in_organization")
			}
			member, _ := body["member"].(map[string]interface{})
			if body["organization_id"] != organizationID || member["user_email"] != email || member["role"] != "internal_user" {
				t.Errorf("add body = %#v", body)
			}
			for key := range body {
				if key != "organization_id" && key != "member" {
					t.Errorf("unexpected add key %q", key)
				}
			}
			memberExists = true
			role = member["role"].(string)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"organization_id":                  organizationID,
				"updated_users":                    []interface{}{map[string]interface{}{"user_id": userID, "user_email": email}},
				"updated_organization_memberships": []interface{}{currentMember()},
			})
		case request.Method == http.MethodPatch && request.URL.Path == "/organization/member_update":
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode patch: %v", err)
			}
			patchBodies = append(patchBodies, body)
			if _, nested := body["member"]; nested {
				t.Error("member_update used nested member payload")
			}
			if body["organization_id"] != organizationID || body["user_id"] != userID {
				t.Errorf("update identity = %#v", body)
			}
			if _, sentEmail := body["user_email"]; sentEmail {
				t.Error("canonical update unexpectedly sent user_email")
			}
			role = body["role"].(string)
			value := body["max_budget_in_organization"].(float64)
			budget = &value
			budgetID = "member-budget"
			_ = json.NewEncoder(writer).Encode(currentMember())
		case request.Method == http.MethodDelete && request.URL.Path == "/organization/member_delete":
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			if !reflect.DeepEqual(body, map[string]interface{}{"organization_id": organizationID, "user_id": userID}) {
				t.Errorf("delete body = %#v", body)
			}
			memberExists = false
			_ = json.NewEncoder(writer).Encode(currentMember())
		default:
			http.Error(writer, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	createPlan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID:                      types.StringUnknown(),
		OrganizationID:          types.StringValue(organizationID),
		UserID:                  types.StringUnknown(),
		UserEmail:               types.StringValue(email),
		Role:                    types.StringValue("internal_user"),
		MaxBudgetInOrganization: types.Float64Value(50),
	})
	memberResource := &OrganizationMemberResource{client: organizationMemberTestClient(server)}
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: createPlan.Raw, Schema: schema}}
	memberResource.Create(context.Background(), resource.CreateRequest{Plan: createPlan}, createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	var created OrganizationMemberResourceModel
	if diagnostics := createResponse.State.Get(context.Background(), &created); diagnostics.HasError() {
		t.Fatalf("decode create state: %v", diagnostics)
	}
	if created.ID.ValueString() != organizationID+":"+userID || created.UserID.ValueString() != userID || created.MaxBudgetInOrganization.ValueFloat64() != 50 {
		t.Fatalf("created state = %#v", created)
	}

	updatePlan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID:                      created.ID,
		OrganizationID:          created.OrganizationID,
		UserID:                  created.UserID,
		UserEmail:               created.UserEmail,
		Role:                    types.StringValue("org_admin"),
		MaxBudgetInOrganization: types.Float64Value(75),
	})
	updateResponse := &resource.UpdateResponse{State: createResponse.State}
	memberResource.Update(context.Background(), resource.UpdateRequest{Plan: updatePlan, State: createResponse.State}, updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}
	var updated OrganizationMemberResourceModel
	if diagnostics := updateResponse.State.Get(context.Background(), &updated); diagnostics.HasError() {
		t.Fatalf("decode update state: %v", diagnostics)
	}
	if updated.Role.ValueString() != "org_admin" || updated.MaxBudgetInOrganization.ValueFloat64() != 75 {
		t.Fatalf("updated state = %#v", updated)
	}

	deleteResponse := &resource.DeleteResponse{}
	memberResource.Delete(context.Background(), resource.DeleteRequest{State: updateResponse.State}, deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	if memberExists {
		t.Fatal("membership still exists after delete")
	}
	if len(patchBodies) != 2 || patchBodies[0]["max_budget_in_organization"] != float64(50) || patchBodies[1]["max_budget_in_organization"] != float64(75) {
		t.Fatalf("budget persistence patches = %#v", patchBodies)
	}
	wantCalls := "GET /organization/info,POST /organization/member_add,PATCH /organization/member_update,GET /organization/info,PATCH /organization/member_update,GET /organization/info,DELETE /organization/member_delete"
	if got := strings.Join(calls, ","); got != wantCalls {
		t.Fatalf("calls = %q, want %q", got, wantCalls)
	}
	for _, call := range calls {
		if strings.Contains(call, "/user/") || strings.Contains(call, "/budget/") {
			t.Fatalf("organization membership lifecycle made unsupported auxiliary API call: %s", call)
		}
	}
}

func TestOrganizationMemberBudgetClearFailsWithoutMutationAndKeepsState(t *testing.T) {
	t.Parallel()

	mutated := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutated = true
		http.Error(writer, "unexpected mutation", http.StatusInternalServerError)
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
		ID:                      types.StringValue("org-1:user-1"),
		OrganizationID:          types.StringValue("org-1"),
		UserID:                  types.StringValue("user-1"),
		UserEmail:               types.StringNull(),
		Role:                    types.StringValue("internal_user"),
		MaxBudgetInOrganization: types.Float64Value(50),
	})
	plan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID:                      types.StringValue("org-1:user-1"),
		OrganizationID:          types.StringValue("org-1"),
		UserID:                  types.StringValue("user-1"),
		UserEmail:               types.StringNull(),
		Role:                    types.StringValue("internal_user"),
		MaxBudgetInOrganization: types.Float64Null(),
	})
	response := &resource.UpdateResponse{State: state}
	(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Update(
		context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected unsupported in-place clear to fail")
	}
	if mutated {
		t.Fatal("budget clear sent a mutation that LiteLLM would ignore")
	}
	var retained OrganizationMemberResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatalf("decode retained state: %v", diagnostics)
	}
	if retained.MaxBudgetInOrganization.ValueFloat64() != 50 {
		t.Fatalf("clear failure state = %v, want prior budget 50", retained.MaxBudgetInOrganization)
	}
}

func TestOrganizationMemberCreateMalformedSuccessRetainsRecoveredState(t *testing.T) {
	t.Parallel()

	created := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/organization/info":
			members := []interface{}{}
			if created {
				members = append(members, organizationMemberJSON("org-1", "resolved-user", "member@example.com", "org_admin", "", nil))
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": "org-1", "members": members})
		case "/organization/member_add":
			created = true
			_, _ = writer.Write([]byte(`{"accepted":`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	plan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID: types.StringUnknown(), OrganizationID: types.StringValue("org-1"), UserID: types.StringUnknown(),
		UserEmail: types.StringValue("member@example.com"), Role: types.StringValue("org_admin"),
		MaxBudgetInOrganization: types.Float64Null(),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Create(
		context.Background(), resource.CreateRequest{Plan: plan}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected malformed successful add response diagnostic")
	}
	if response.State.Raw.IsNull() {
		t.Fatal("provider-owned membership was dropped from state")
	}
	var retained OrganizationMemberResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatalf("decode retained state: %v", diagnostics)
	}
	if retained.ID.ValueString() != "org-1:resolved-user" || retained.UserID.ValueString() != "resolved-user" || retained.Role.ValueString() != "org_admin" {
		t.Fatalf("recovered partial create state = %#v", retained)
	}
}

func TestOrganizationMemberCreateBudgetFollowupFailureRetainsCreatedState(t *testing.T) {
	t.Parallel()

	created := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			members := []interface{}{}
			if created {
				members = append(members, organizationMemberJSON("org-1", "user-1", "member@example.com", "internal_user", "", nil))
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": "org-1", "members": members})
		case request.Method == http.MethodPost && request.URL.Path == "/organization/member_add":
			created = true
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"organization_id":                  "org-1",
				"updated_users":                    []interface{}{map[string]interface{}{"user_id": "user-1", "user_email": "member@example.com"}},
				"updated_organization_memberships": []interface{}{organizationMemberJSON("org-1", "user-1", "member@example.com", "internal_user", "", nil)},
			})
		case request.Method == http.MethodPatch && request.URL.Path == "/organization/member_update":
			http.Error(writer, `{"error":"budget write failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	plan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID: types.StringUnknown(), OrganizationID: types.StringValue("org-1"), UserID: types.StringUnknown(),
		UserEmail: types.StringValue("member@example.com"), Role: types.StringValue("internal_user"),
		MaxBudgetInOrganization: types.Float64Value(50),
	})
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Create(
		context.Background(), resource.CreateRequest{Plan: plan}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected failed budget follow-up diagnostic")
	}
	var retained OrganizationMemberResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatalf("decode retained state: %v", diagnostics)
	}
	if retained.ID.ValueString() != "org-1:user-1" || !retained.MaxBudgetInOrganization.IsNull() {
		t.Fatalf("partial budget state = %#v, want created membership with no confirmed budget", retained)
	}
}

func TestOrganizationMemberUpdateMismatchRetainsConfirmedMutationBudgetWhenReadOmitsIt(t *testing.T) {
	t.Parallel()

	const confirmedBudget = float64(42)
	patchCalls := 0
	readCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPatch && request.URL.Path == "/organization/member_update":
			patchCalls++
			_ = json.NewEncoder(writer).Encode(organizationMemberJSON("org-1", "user-1", "member@example.com", "org_admin", "budget-1", float64Pointer(confirmedBudget)))
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			readCalls++
			member := organizationMemberJSON("org-1", "user-1", "member@example.com", "org_admin", "budget-1", float64Pointer(confirmedBudget))
			member["litellm_budget_table"] = nil
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"organization_id": "org-1",
				"members":         []interface{}{member},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
		ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
		UserEmail: types.StringNull(), Role: types.StringValue("internal_user"),
		MaxBudgetInOrganization: types.Float64Null(),
	})
	plan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
		UserEmail: types.StringNull(), Role: types.StringValue("org_admin"),
		MaxBudgetInOrganization: types.Float64Value(50),
	})
	response := &resource.UpdateResponse{State: state}
	(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Update(
		context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected mismatch between requested budget 50 and confirmed budget 42")
	}
	var retained OrganizationMemberResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatalf("decode retained state: %v", diagnostics)
	}
	if retained.MaxBudgetInOrganization.IsNull() || retained.MaxBudgetInOrganization.ValueFloat64() != confirmedBudget {
		t.Fatalf("retained budget = %v, want authoritative mutation budget 42", retained.MaxBudgetInOrganization)
	}
	if retained.Role.ValueString() != "org_admin" {
		t.Fatalf("retained role = %v, want authoritative mutation role", retained.Role)
	}
	if patchCalls != 1 || readCalls != 1 {
		t.Fatalf("patch calls=%d read calls=%d, want one mutation and one reconciliation", patchCalls, readCalls)
	}
}

func float64Pointer(value float64) *float64 { return &value }

func TestOrganizationMemberMutationDiagnosticsOmitBodiesAndBoundOversizedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		bodyLength int64
		wantStatus int
	}{
		{
			name:       "error response echo",
			statusCode: http.StatusBadRequest,
			body:       `{"detail":"plain-response-secret at https://internal.invalid/organization/member_update?token=query-secret"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "oversized error response",
			statusCode: http.StatusBadGateway,
			body:       "oversized-error-secret",
			bodyLength: maxErrorResponseBody + 1,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "oversized successful response",
			statusCode: http.StatusOK,
			body:       "oversized-success-secret",
			bodyLength: maxSuccessResponseBody + 1,
			wantStatus: http.StatusOK,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.Method {
				case http.MethodPatch:
					if test.bodyLength > 0 {
						writer.Header().Set("Content-Length", strconv.FormatInt(test.bodyLength, 10))
					}
					writer.WriteHeader(test.statusCode)
					_, _ = writer.Write([]byte(test.body))
				case http.MethodGet:
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{
						"organization_id": "org-1",
						"members": []interface{}{
							organizationMemberJSON("org-1", "user-1", "member@example.com", "internal_user", "", nil),
						},
					})
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			schema := organizationMemberTestSchema(t)
			state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
				ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
				UserEmail: types.StringValue("member@example.com"), Role: types.StringValue("internal_user"),
				MaxBudgetInOrganization: types.Float64Null(),
			})
			plan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
				ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
				UserEmail: types.StringValue("member@example.com"), Role: types.StringValue("org_admin"),
				MaxBudgetInOrganization: types.Float64Null(),
			})
			response := &resource.UpdateResponse{State: state}
			(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Update(
				context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response,
			)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected mutation response diagnostic")
			}
			diagnosticText := ""
			for _, diagnostic := range response.Diagnostics.Errors() {
				diagnosticText += diagnostic.Summary() + " " + diagnostic.Detail()
			}
			if !strings.Contains(diagnosticText, "HTTP "+strconv.Itoa(test.wantStatus)) {
				t.Fatalf("diagnostic = %q, want safe HTTP status", diagnosticText)
			}
			for _, forbidden := range []string{
				"plain-response-secret", "query-secret", "internal.invalid", "oversized-error-secret",
				"oversized-success-secret", server.URL, "/organization/member_update?",
			} {
				if strings.Contains(diagnosticText, forbidden) {
					t.Fatalf("diagnostic exposed %q: %q", forbidden, diagnosticText)
				}
			}
		})
	}
}

func TestOrganizationMemberUpdateMalformedSuccessReconcilesRemoteState(t *testing.T) {
	t.Parallel()

	role := "internal_user"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPatch:
			role = "org_admin"
			_, _ = writer.Write([]byte(`{}`))
		case request.Method == http.MethodGet:
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"organization_id": "org-1",
				"members":         []interface{}{organizationMemberJSON("org-1", "user-1", "member@example.com", role, "", nil)},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
		ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
		UserEmail: types.StringValue("member@example.com"), Role: types.StringValue("internal_user"),
		MaxBudgetInOrganization: types.Float64Null(),
	})
	plan := organizationMemberTestPlan(t, schema, OrganizationMemberResourceModel{
		ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
		UserEmail: types.StringValue("member@example.com"), Role: types.StringValue("org_admin"),
		MaxBudgetInOrganization: types.Float64Null(),
	})
	response := &resource.UpdateResponse{State: state}
	(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Update(
		context.Background(), resource.UpdateRequest{Plan: plan, State: state}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected malformed successful update response diagnostic")
	}
	var reconciled OrganizationMemberResourceModel
	if diagnostics := response.State.Get(context.Background(), &reconciled); diagnostics.HasError() {
		t.Fatalf("decode reconciled state: %v", diagnostics)
	}
	if reconciled.Role.ValueString() != "org_admin" {
		t.Fatalf("partial update state = %#v, want authoritative remote role", reconciled)
	}
}

func TestOrganizationMemberReadRemovesMissingMembership(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": "org-1", "members": []interface{}{}})
	}))
	defer server.Close()

	schema := organizationMemberTestSchema(t)
	state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
		ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
		UserEmail: types.StringNull(), Role: types.StringValue("internal_user"), MaxBudgetInOrganization: types.Float64Null(),
	})
	response := &resource.ReadResponse{State: state}
	(&OrganizationMemberResource{client: organizationMemberTestClient(server)}).Read(
		context.Background(), resource.ReadRequest{State: state}, response,
	)
	if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
		t.Fatalf("missing member read diagnostics=%v state_null=%t", response.Diagnostics, response.State.Raw.IsNull())
	}
}

func TestOrganizationMemberUsesExactNotFoundStatus(t *testing.T) {
	t.Parallel()

	t.Run("misleading 500 retains state and errors", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"member not found after lookup on port 4040"}`))
		}))
		defer server.Close()

		schema := organizationMemberTestSchema(t)
		state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
			ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
			UserEmail: types.StringNull(), Role: types.StringValue("internal_user"), MaxBudgetInOrganization: types.Float64Null(),
		})
		memberResource := &OrganizationMemberResource{client: organizationMemberTestClient(server)}
		readResponse := &resource.ReadResponse{State: state}
		memberResource.Read(context.Background(), resource.ReadRequest{State: state}, readResponse)
		if !readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() {
			t.Fatalf("500 read diagnostics=%v state_null=%t", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
		}
		deleteResponse := &resource.DeleteResponse{}
		memberResource.Delete(context.Background(), resource.DeleteRequest{State: state}, deleteResponse)
		if !deleteResponse.Diagnostics.HasError() {
			t.Fatal("500 containing not-found text was treated as successful delete")
		}
	})

	t.Run("exact 404 is absence", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Error(writer, "missing", http.StatusNotFound)
		}))
		defer server.Close()

		schema := organizationMemberTestSchema(t)
		state := organizationMemberTestState(t, schema, OrganizationMemberResourceModel{
			ID: types.StringValue("org-1:user-1"), OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"),
			UserEmail: types.StringNull(), Role: types.StringValue("internal_user"), MaxBudgetInOrganization: types.Float64Null(),
		})
		memberResource := &OrganizationMemberResource{client: organizationMemberTestClient(server)}
		readResponse := &resource.ReadResponse{State: state}
		memberResource.Read(context.Background(), resource.ReadRequest{State: state}, readResponse)
		if readResponse.Diagnostics.HasError() || !readResponse.State.Raw.IsNull() {
			t.Fatalf("404 read diagnostics=%v state_null=%t", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
		}
		deleteResponse := &resource.DeleteResponse{}
		memberResource.Delete(context.Background(), resource.DeleteRequest{State: state}, deleteResponse)
		if deleteResponse.Diagnostics.HasError() {
			t.Fatalf("404 delete diagnostics: %v", deleteResponse.Diagnostics)
		}
	})
}

func TestOrganizationMemberImportCanonicalIdentity(t *testing.T) {
	t.Parallel()

	memberResource := &OrganizationMemberResource{}
	var schemaResponse resource.SchemaResponse
	memberResource.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)

	state, err := nullStateFor(schemaResponse.Schema)
	if err != nil {
		t.Fatalf("build import state: %v", err)
	}
	response := &resource.ImportStateResponse{State: state}
	memberResource.ImportState(context.Background(), resource.ImportStateRequest{ID: "org-1:user@example.com"}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var imported OrganizationMemberResourceModel
	if diagnostics := response.State.Get(context.Background(), &imported); diagnostics.HasError() {
		t.Fatalf("decode import state: %v", diagnostics)
	}
	if imported.ID.ValueString() != "org-1:user@example.com" || imported.OrganizationID.ValueString() != "org-1" || imported.UserID.ValueString() != "user@example.com" {
		t.Fatalf("imported identity = %#v", imported)
	}

	for _, importID := range []string{"", "org-only", ":user-1", "org-1:"} {
		state, err := nullStateFor(schemaResponse.Schema)
		if err != nil {
			t.Fatalf("build malformed import state: %v", err)
		}
		response := &resource.ImportStateResponse{State: state}
		memberResource.ImportState(context.Background(), resource.ImportStateRequest{ID: importID}, response)
		if !response.Diagnostics.HasError() {
			t.Errorf("malformed import %q was accepted", importID)
		}
	}
}
