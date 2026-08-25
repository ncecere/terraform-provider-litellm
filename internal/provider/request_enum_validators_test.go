package provider

import (
	"context"
	"testing"

	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type requestEnumValidatorCase struct {
	name       string
	validators []validator.String
	accepted   []string
	rejected   []string
}

func resourceStringValidators(
	t *testing.T,
	resourceUnderTest frameworkresource.Resource,
	attributeName string,
) []validator.String {
	t.Helper()

	var response frameworkresource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), frameworkresource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes[attributeName].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("attribute %q schema type = %T, want schema.StringAttribute", attributeName, response.Schema.Attributes[attributeName])
	}
	if len(attribute.Validators) == 0 {
		t.Fatalf("attribute %q has no string validators", attributeName)
	}
	return attribute.Validators
}

func dataSourceStringValidators(
	t *testing.T,
	dataSourceUnderTest frameworkdatasource.DataSource,
	attributeName string,
) []validator.String {
	t.Helper()

	var response frameworkdatasource.SchemaResponse
	dataSourceUnderTest.Schema(context.Background(), frameworkdatasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes[attributeName].(datasourceschema.StringAttribute)
	if !ok {
		t.Fatalf("attribute %q schema type = %T, want schema.StringAttribute", attributeName, response.Schema.Attributes[attributeName])
	}
	if len(attribute.Validators) == 0 {
		t.Fatalf("attribute %q has no string validators", attributeName)
	}
	return attribute.Validators
}

func teamMemberAddRoleValidators(t *testing.T) []validator.String {
	t.Helper()

	var response frameworkresource.SchemaResponse
	(&TeamMemberAddResource{}).Schema(context.Background(), frameworkresource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	memberBlock, ok := response.Schema.Blocks["member"].(resourceschema.SetNestedBlock)
	if !ok {
		t.Fatalf("member block schema type = %T, want schema.SetNestedBlock", response.Schema.Blocks["member"])
	}
	role, ok := memberBlock.NestedObject.Attributes["role"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("member.role schema type = %T, want schema.StringAttribute", memberBlock.NestedObject.Attributes["role"])
	}
	return role.Validators
}

func validateStringValue(ctx context.Context, validators []validator.String, value types.String) bool {
	request := validator.StringRequest{
		Path:        path.Root("value"),
		ConfigValue: value,
	}
	var response validator.StringResponse
	for _, stringValidator := range validators {
		stringValidator.ValidateString(ctx, request, &response)
	}
	return response.Diagnostics.HasError()
}

func TestRequestEnumValidatorsMatchLiteLLMV198Contracts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	keyLimitAccepted := []string{"guaranteed_throughput", "best_effort_throughput", "dynamic"}
	teamLimitAccepted := []string{"guaranteed_throughput", "best_effort_throughput"}
	teamMemberRoleAccepted := []string{"admin", "user"}
	fallbackTypeAccepted := []string{"general", "context_window", "content_policy"}

	tests := []requestEnumValidatorCase{
		{
			name:       "key tpm_limit_type",
			validators: resourceStringValidators(t, &KeyResource{}, "tpm_limit_type"),
			accepted:   keyLimitAccepted,
			rejected:   []string{"key", "team", "Dynamic", "", " dynamic"},
		},
		{
			name:       "key rpm_limit_type",
			validators: resourceStringValidators(t, &KeyResource{}, "rpm_limit_type"),
			accepted:   keyLimitAccepted,
			rejected:   []string{"key", "team", "BEST_EFFORT_THROUGHPUT", "", "dynamic "},
		},
		{
			name:       "team tpm_limit_type",
			validators: resourceStringValidators(t, &TeamResource{}, "tpm_limit_type"),
			accepted:   teamLimitAccepted,
			rejected:   []string{"dynamic", "key", "team", "Guaranteed_Throughput", ""},
		},
		{
			name:       "team rpm_limit_type",
			validators: resourceStringValidators(t, &TeamResource{}, "rpm_limit_type"),
			accepted:   teamLimitAccepted,
			rejected:   []string{"dynamic", "key", "team", "best_effort_throughput ", ""},
		},
		{
			name:       "user user_role",
			validators: resourceStringValidators(t, &UserResource{}, "user_role"),
			accepted:   []string{"proxy_admin", "proxy_admin_viewer", "internal_user", "internal_user_viewer"},
			rejected:   []string{"team", "customer", "org_admin", "INTERNAL_USER", ""},
		},
		{
			name:       "organization member role",
			validators: resourceStringValidators(t, &OrganizationMemberResource{}, "role"),
			accepted:   []string{"org_admin", "internal_user", "internal_user_viewer"},
			rejected:   []string{"proxy_admin", "proxy_admin_viewer", "team", "customer", "ORG_ADMIN", ""},
		},
		{
			name:       "team member role",
			validators: resourceStringValidators(t, &TeamMemberResource{}, "role"),
			accepted:   teamMemberRoleAccepted,
			rejected:   []string{"member", "Admin", "", "user "},
		},
		{
			name:       "team member add role",
			validators: teamMemberAddRoleValidators(t),
			accepted:   teamMemberRoleAccepted,
			rejected:   []string{"member", "USER", "", " admin"},
		},
		{
			name:       "MCP transport",
			validators: resourceStringValidators(t, &MCPServerResource{}, "transport"),
			accepted:   []string{"http", "sse", "stdio"},
			rejected:   []string{"websocket", "HTTP", "", "sse "},
		},
		{
			name:       "MCP auth_type",
			validators: resourceStringValidators(t, &MCPServerResource{}, "auth_type"),
			accepted: []string{
				"none",
				"api_key",
				"bearer_token",
				"basic",
				"authorization",
				"oauth2",
				"aws_sigv4",
				"token",
				"oauth2_token_exchange",
				"oauth2_id_jag",
				"true_passthrough",
				"oauth_delegate",
			},
			rejected: []string{"bearer", "oauth", "AWS_SIGV4", "", "none "},
		},
		{
			name:       "fallback resource fallback_type",
			validators: resourceStringValidators(t, &FallbackResource{}, "fallback_type"),
			accepted:   fallbackTypeAccepted,
			rejected:   []string{"content_window", "General", "", "general "},
		},
		{
			name:       "fallback data source fallback_type",
			validators: dataSourceStringValidators(t, &FallbackDataSource{}, "fallback_type"),
			accepted:   fallbackTypeAccepted,
			rejected:   []string{"content_window", "GENERAL", "", " context_window"},
		},
		{
			name:       "prompt prompt_type",
			validators: resourceStringValidators(t, &PromptResource{}, "prompt_type"),
			accepted:   []string{"config", "db"},
			rejected:   []string{"database", "DB", "", "config "},
		},
		{
			name:       "model tier",
			validators: resourceStringValidators(t, &ModelResource{}, "tier"),
			accepted:   []string{"free", "paid"},
			rejected:   []string{"enterprise", "PAID", "", "free "},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, value := range test.accepted {
				if validateStringValue(ctx, test.validators, types.StringValue(value)) {
					t.Errorf("accepted value %q produced a validation error", value)
				}
			}
			for _, value := range test.rejected {
				if !validateStringValue(ctx, test.validators, types.StringValue(value)) {
					t.Errorf("rejected value %q did not produce a validation error", value)
				}
			}
			for name, value := range map[string]types.String{
				"null":    types.StringNull(),
				"unknown": types.StringUnknown(),
			} {
				if validateStringValue(ctx, test.validators, value) {
					t.Errorf("%s value produced a validation error", name)
				}
			}
		})
	}
}

func TestBudgetDurationValidatorsAcceptPositiveMonthCounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, test := range []struct {
		name       string
		validators []validator.String
	}{
		{
			name:       "team default member budget",
			validators: resourceStringValidators(t, &TeamResource{}, "team_member_budget_duration"),
		},
		{
			name:       "team member budget",
			validators: resourceStringValidators(t, &TeamMemberResource{}, "budget_duration"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range []string{"1mo", "2mo", "12mo", "monthly", "30d", "24h"} {
				if validateStringValue(ctx, test.validators, types.StringValue(value)) {
					t.Errorf("valid duration %q produced a validation error", value)
				}
			}
			for _, value := range []string{"0mo", "mo", "-2mo", "2month", "2MO", ""} {
				if !validateStringValue(ctx, test.validators, types.StringValue(value)) {
					t.Errorf("invalid duration %q did not produce a validation error", value)
				}
			}
			for name, value := range map[string]types.String{
				"null":    types.StringNull(),
				"unknown": types.StringUnknown(),
			} {
				if validateStringValue(ctx, test.validators, value) {
					t.Errorf("%s value produced a validation error", name)
				}
			}
		})
	}
}

func TestLimitAndRoleRequestBuildersPreserveValidatedLiterals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	keyRequest, err := (&KeyResource{}).buildKeyRequest(ctx, &KeyResourceModel{
		TPMLimitType: types.StringValue("dynamic"),
		RPMLimitType: types.StringValue("guaranteed_throughput"),
	})
	if err != nil {
		t.Fatalf("build key request: %v", err)
	}
	if got := keyRequest["tpm_limit_type"]; got != "dynamic" {
		t.Errorf("key tpm_limit_type = %#v, want dynamic", got)
	}
	if got := keyRequest["rpm_limit_type"]; got != "guaranteed_throughput" {
		t.Errorf("key rpm_limit_type = %#v, want guaranteed_throughput", got)
	}

	teamRequest := (&TeamResource{}).buildTeamRequest(ctx, &TeamResourceModel{
		TeamAlias:    types.StringValue("contract-team"),
		TPMLimitType: types.StringValue("best_effort_throughput"),
		RPMLimitType: types.StringValue("guaranteed_throughput"),
	}, "team-contract")
	if got := teamRequest["tpm_limit_type"]; got != "best_effort_throughput" {
		t.Errorf("team tpm_limit_type = %#v, want best_effort_throughput", got)
	}
	if got := teamRequest["rpm_limit_type"]; got != "guaranteed_throughput" {
		t.Errorf("team rpm_limit_type = %#v, want guaranteed_throughput", got)
	}

	userRequest := (&UserResource{}).buildUserRequest(ctx, &UserResourceModel{
		UserRole: types.StringValue("internal_user_viewer"),
	})
	if got := userRequest["user_role"]; got != "internal_user_viewer" {
		t.Errorf("user_role = %#v, want internal_user_viewer", got)
	}
}
