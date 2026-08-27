package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var _ resource.Resource = &MCPServerResource{}
var _ resource.ResourceWithImportState = &MCPServerResource{}
var _ resource.ResourceWithUpgradeState = &MCPServerResource{}
var _ resource.ResourceWithValidateConfig = &MCPServerResource{}
var _ resource.ResourceWithModifyPlan = &MCPServerResource{}

// Wire-contract values verified against LiteLLM commit
// d8f71d7bdbd7c9873d98293f83d64c6db72847e6: litellm/types/mcp.py,
// litellm/constants.py, and NewMCPServerRequest/UpdateMCPServerRequest in
// litellm/proxy/_types.py.
var mcpAuthTypesV198 = []string{
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
}

var mcpTransportsV198 = []string{"http", "sse", "stdio"}

var mcpStdioAllowedCommandsV198 = map[string]struct{}{
	"deno":    {},
	"docker":  {},
	"node":    {},
	"npx":     {},
	"python":  {},
	"python3": {},
	"uvx":     {},
}

type mcpSafeEnumValidator struct {
	allowed     map[string]struct{}
	description string
}

var _ validator.String = mcpSafeEnumValidator{}

func newMCPSafeEnumValidator(values []string, description string) mcpSafeEnumValidator {
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}
	return mcpSafeEnumValidator{allowed: allowed, description: description}
}

func (v mcpSafeEnumValidator) Description(context.Context) string {
	return v.description
}

func (v mcpSafeEnumValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v mcpSafeEnumValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, ok := v.allowed[req.ConfigValue.ValueString()]; !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MCP Configuration", v.description)
	}
}

func NewMCPServerResource() resource.Resource {
	return &MCPServerResource{}
}

type MCPServerResource struct {
	client *Client
}

type MCPServerCostInfoModel struct {
	DefaultCostPerQuery    types.Float64 `tfsdk:"default_cost_per_query"`
	ToolNameToCostPerQuery types.Map     `tfsdk:"tool_name_to_cost_per_query"`
}

type MCPInfoModel struct {
	ServerName        types.String            `tfsdk:"server_name"`
	Description       types.String            `tfsdk:"description"`
	LogoURL           types.String            `tfsdk:"logo_url"`
	MCPServerCostInfo *MCPServerCostInfoModel `tfsdk:"mcp_server_cost_info"`
}

type MCPServerResourceModel struct {
	ID                         types.String  `tfsdk:"id"`
	ServerID                   types.String  `tfsdk:"server_id"`
	ServerName                 types.String  `tfsdk:"server_name"`
	Alias                      types.String  `tfsdk:"alias"`
	Description                types.String  `tfsdk:"description"`
	URL                        types.String  `tfsdk:"url"`
	SpecPath                   types.String  `tfsdk:"spec_path"`
	Transport                  types.String  `tfsdk:"transport"`
	SpecVersion                types.String  `tfsdk:"spec_version"`
	AuthType                   types.String  `tfsdk:"auth_type"`
	MCPAccessGroups            types.List    `tfsdk:"mcp_access_groups"`
	Command                    types.String  `tfsdk:"command"`
	Args                       types.List    `tfsdk:"args"`
	Env                        types.Map     `tfsdk:"env"`
	MCPInfo                    *MCPInfoModel `tfsdk:"mcp_info"`
	MCPInfoJSON                types.String  `tfsdk:"mcp_info_json"`
	MCPInfoOverridesJSON       types.String  `tfsdk:"mcp_info_overrides_json"`
	MCPInfoClearPaths          types.List    `tfsdk:"mcp_info_clear_paths"`
	MCPInfoOwnershipGeneration types.Int64   `tfsdk:"mcp_info_ownership_generation"`
	FieldOwnershipGeneration   types.Int64   `tfsdk:"field_ownership_generation"`
	// New fields for expanded API support
	Credentials       types.Map    `tfsdk:"credentials"`
	AllowedTools      types.List   `tfsdk:"allowed_tools"`
	ExtraHeaders      types.List   `tfsdk:"extra_headers"`
	StaticHeaders     types.Map    `tfsdk:"static_headers"`
	AuthorizationURL  types.String `tfsdk:"authorization_url"`
	TokenURL          types.String `tfsdk:"token_url"`
	RegistrationURL   types.String `tfsdk:"registration_url"`
	AllowAllKeys      types.Bool   `tfsdk:"allow_all_keys"`
	SkipURLValidation types.Bool   `tfsdk:"skip_url_validation"`
	// Computed fields
	CreatedAt types.String `tfsdk:"created_at"`
	CreatedBy types.String `tfsdk:"created_by"`
}

func (r *MCPServerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_server"
}

func (r *MCPServerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM MCP (Model Context Protocol) server.",
		Version:     3,
		Attributes: map[string]schema.Attribute{
			"mcp_info_json": schema.StringAttribute{
				Description: "Sensitive complete MCP info JSON object. The root must be a non-null object; {} explicitly owns and clears the whole document. Authoritative reads expose the complete object without dropping unknown members or exact JSON numbers.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
			},
			"mcp_info_overrides_json": schema.StringAttribute{
				Description: "Sensitive recursive selective MCP info JSON object. Nested null is data and a nested empty object atomically replaces that member.",
				Optional:    true,
				Sensitive:   true,
			},
			"mcp_info_clear_paths": schema.ListAttribute{
				Description: "Sensitive list of canonical RFC 6901 object-member pointers to clear. Root and array traversal are not supported.",
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"mcp_info_ownership_generation": schema.Int64Attribute{
				Description: "Internal non-sensitive generation of MCP info ownership intent.",
				Computed:    true,
			},
			"field_ownership_generation": schema.Int64Attribute{
				Description: "Internal non-sensitive generation of presence-aware MCP field ownership intent.",
				Computed:    true,
			},
			"id": schema.StringAttribute{
				Description: "The unique identifier for this MCP server (same as server_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"server_id": schema.StringAttribute{
				Description: "Unique identifier for the MCP server.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"server_name": schema.StringAttribute{
				Description: "Name of the MCP server.",
				Required:    true,
			},
			"alias": schema.StringAttribute{
				Description: "Alias for the MCP server.",
				Optional:    true,
			},
			"description": schema.StringAttribute{
				Description: "Description of the MCP server.",
				Optional:    true,
			},
			"url": schema.StringAttribute{
				Description: "URL of the MCP server. HTTP and SSE transports require url or spec_path; stdio does not require a URL.",
				Optional:    true,
			},
			"spec_path": schema.StringAttribute{
				Description: "Path or URL of an OpenAPI specification. For HTTP and SSE transports this can be used instead of url.",
				Optional:    true,
			},
			"transport": schema.StringAttribute{
				Description: "Transport type for the MCP server (http, sse, stdio).",
				Required:    true,
				Validators: []validator.String{
					newMCPSafeEnumValidator(mcpTransportsV198, "Transport must be one of the values accepted by LiteLLM v1.98."),
				},
			},
			"spec_version": schema.StringAttribute{
				Description:        "Deprecated compatibility attribute. LiteLLM v1.98 does not accept or return this field.",
				DeprecationMessage: "spec_version is retained only for state and HCL compatibility and is not sent to LiteLLM. Remove it from configuration.",
				Optional:           true,
				Computed:           true,
				Default:            stringdefault.StaticString("2024-11-05"),
			},
			"auth_type": schema.StringAttribute{
				Description: "Authentication type accepted by the LiteLLM v1.98 MCP server request contract.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("none"),
				Validators: []validator.String{
					newMCPSafeEnumValidator(mcpAuthTypesV198, "Authentication type must be one of the values accepted by LiteLLM v1.98."),
				},
			},
			"mcp_access_groups": schema.ListAttribute{
				Description: "List of access groups for the MCP server.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"command": schema.StringAttribute{
				Description: "Command to run for stdio transport.",
				Optional:    true,
			},
			"args": schema.ListAttribute{
				Description: "Arguments for the command (stdio transport).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"env": schema.MapAttribute{
				Description: "Environment variables for the command (stdio transport).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"credentials": schema.MapAttribute{
				Description: "Credentials map for the MCP server authentication.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"allowed_tools": schema.ListAttribute{
				Description: "List of allowed tool names for this MCP server.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"extra_headers": schema.ListAttribute{
				Description: "Extra header names to forward to the MCP server.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"static_headers": schema.MapAttribute{
				Description: "Static headers to always include with requests.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"authorization_url": schema.StringAttribute{
				Description: "OAuth authorization URL for the MCP server.",
				Optional:    true,
			},
			"token_url": schema.StringAttribute{
				Description: "OAuth token URL for the MCP server.",
				Optional:    true,
			},
			"registration_url": schema.StringAttribute{
				Description: "OAuth registration URL for the MCP server.",
				Optional:    true,
			},
			"allow_all_keys": schema.BoolAttribute{
				Description: "Whether to allow all API keys to access this MCP server.",
				Optional:    true,
			},
			"skip_url_validation": schema.BoolAttribute{
				Description:        "Deprecated compatibility attribute. LiteLLM v1.98 does not accept this field; new or changed true values are unsafe, while unchanged historical state remains plannable.",
				DeprecationMessage: "skip_url_validation is retained only for state and HCL compatibility and is not sent to LiteLLM. Remove it from configuration.",
				Optional:           true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the server was created.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_by": schema.StringAttribute{
				Description: "User who created the server.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"mcp_info": schema.SingleNestedBlock{
				Description: "MCP server information and configuration.",
				Attributes: map[string]schema.Attribute{
					"server_name": schema.StringAttribute{
						Description: "Server name in MCP info.",
						Optional:    true,
					},
					"description": schema.StringAttribute{
						Description: "Description in MCP info.",
						Optional:    true,
					},
					"logo_url": schema.StringAttribute{
						Description: "Logo URL for the MCP server.",
						Optional:    true,
					},
				},
				Blocks: map[string]schema.Block{
					"mcp_server_cost_info": schema.SingleNestedBlock{
						Description: "Cost information for MCP server tools.",
						Attributes: map[string]schema.Attribute{
							"default_cost_per_query": schema.Float64Attribute{
								Description: "Default cost per query.",
								Optional:    true,
							},
							"tool_name_to_cost_per_query": schema.MapAttribute{
								Description: "Map of tool names to their cost per query.",
								Optional:    true,
								Computed:    true,
								ElementType: types.Float64Type,
								Validators:  []validator.Map{mapvalidator.NoNullValues()},
							},
						},
					},
				},
			},
		},
	}
}

func (r *MCPServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data MCPServerResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for name, value := range map[string]types.String{"url": data.URL, "spec_path": data.SpecPath} {
		if !value.IsNull() && !value.IsUnknown() && value.ValueString() == "" {
			resp.Diagnostics.AddAttributeError(
				path.Root(name),
				"Invalid MCP Endpoint Configuration",
				"Configured MCP endpoint fields must be non-empty. Omit the field to clear or release it.",
			)
		}
	}
	validateMCPInfoJSONConfig(data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() || data.Transport.IsNull() || data.Transport.IsUnknown() {
		return
	}
	switch data.Transport.ValueString() {
	case "http", "sse":
		if data.URL.IsUnknown() || data.SpecPath.IsUnknown() {
			return
		}
		if !mcpKnownNonEmptyString(data.URL) && !mcpKnownNonEmptyString(data.SpecPath) {
			resp.Diagnostics.AddAttributeError(
				path.Root("url"),
				"Invalid MCP Transport Configuration",
				"HTTP and SSE configurations require at least one non-empty endpoint field: url or spec_path.",
			)
		}
	case "stdio":
		if !data.Command.IsUnknown() && !mcpKnownNonEmptyString(data.Command) {
			resp.Diagnostics.AddAttributeError(
				path.Root("command"),
				"Invalid MCP Stdio Configuration",
				"A non-empty command is required for stdio transport.",
			)
		}
		if !data.Args.IsUnknown() && (data.Args.IsNull() || len(data.Args.Elements()) == 0) {
			resp.Diagnostics.AddAttributeError(
				path.Root("args"),
				"Invalid MCP Stdio Configuration",
				"At least one command argument is required for stdio transport.",
			)
		}
		if mcpKnownNonEmptyString(data.Command) {
			if _, ok := mcpStdioAllowedCommandsV198[mcpStdioCommandBaseV198(data.Command.ValueString())]; !ok {
				resp.Diagnostics.AddAttributeError(
					path.Root("command"),
					"Invalid MCP Stdio Configuration",
					"The command executable is not in LiteLLM v1.98's built-in stdio allowlist: deno, docker, node, npx, python, python3, uvx.",
				)
			}
		}
	}
}

func (r *MCPServerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	prior, privateDiags := readMCPInfoProvenance(ctx, req.Private)
	resp.Diagnostics.Append(privateDiags...)
	priorFields, fieldPrivateDiags := readMCPFieldOwnership(ctx, req.Private)
	resp.Diagnostics.Append(fieldPrivateDiags...)
	if resp.Diagnostics.HasError() {
		// Private corruption must not turn a planned update or destroy into an
		// ownership-losing state transition.
		resp.Private = req.Private
		if !req.State.Raw.IsNull() {
			resp.Plan.Raw = req.State.Raw
		}
		return
	}

	// Destroy remains possible for historical phantom values only when their
	// private ownership grammar is valid.
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan, config MCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state MCPServerResourceModel
	hasState := !req.State.Raw.IsNull()
	if hasState {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	// Re-run ownership validation from Config at resource plan time. This is
	// intentionally not based on ProposedNewState, where Optional+Computed
	// values can contain prior state.
	validateMCPInfoJSONConfig(config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	unsupportedSpecVersion := !plan.SpecVersion.IsNull() && !plan.SpecVersion.IsUnknown() && plan.SpecVersion.ValueString() != "2024-11-05"
	unchangedSpecVersion := hasState && !state.SpecVersion.IsUnknown() && state.SpecVersion.Equal(plan.SpecVersion)
	if unsupportedSpecVersion && !unchangedSpecVersion {
		resp.Diagnostics.AddAttributeError(
			path.Root("spec_version"),
			"Unsupported Deprecated MCP Configuration",
			"LiteLLM v1.98 does not accept this compatibility field. A historical non-default value may remain unchanged, but new or changed non-default values are unsafe.",
		)
	}

	unsupportedSkipValidation := !plan.SkipURLValidation.IsNull() && !plan.SkipURLValidation.IsUnknown() && plan.SkipURLValidation.ValueBool()
	unchangedSkipValidation := hasState && !state.SkipURLValidation.IsUnknown() && state.SkipURLValidation.Equal(plan.SkipURLValidation)
	if unsupportedSkipValidation && !unchangedSkipValidation {
		resp.Diagnostics.AddAttributeError(
			path.Root("skip_url_validation"),
			"Unsupported Deprecated MCP Configuration",
			"LiteLLM v1.98 does not accept this compatibility field. A historical true value may remain unchanged, but a new or changed true value is unsafe.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	candidate, ownershipDiags := deriveMCPInfoJSONPlanProvenance(ctx, prior, config, state)
	resp.Diagnostics.Append(ownershipDiags...)
	candidateFields := deriveMCPFieldPlanOwnership(priorFields, config)
	if resp.Diagnostics.HasError() {
		return
	}
	effectiveJSON, setEffectiveJSON, effectiveDiags := planEffectiveMCPInfoJSON(ctx, hasState, state, config)
	resp.Diagnostics.Append(effectiveDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if setEffectiveJSON {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("mcp_info_json"), effectiveJSON)...)
	}
	plannedGeneration := types.Int64Value(candidate.Generation)
	if hasState && state.MCPInfoOwnershipGeneration.IsNull() && mcpInfoOwnershipEqual(prior, candidate) {
		// Direct protocol callers may bypass state upgrading. Preserve their
		// historical typed null on a true ownership no-op.
		plannedGeneration = types.Int64Null()
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("mcp_info_ownership_generation"), plannedGeneration)...)
	plannedFieldGeneration := mcpFieldGenerationValue(candidateFields)
	if hasState && state.FieldOwnershipGeneration.IsNull() && mcpFieldSetsEqual(priorFields.Owned, candidateFields.Owned) && len(candidateFields.Removals) == 0 {
		plannedFieldGeneration = types.Int64Null()
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("field_ownership_generation"), plannedFieldGeneration)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Optional+Computed fields distinguish omission from an explicit empty value
	// through Config plus private provenance. Omitted unowned values retain an
	// import/API projection; removing a committed-owned value plans a typed null.
	for _, item := range []struct {
		name       string
		fieldPath  string
		configured attr.Value
		priorValue attr.Value
		nullValue  attr.Value
	}{
		{name: "mcp_access_groups", fieldPath: mcpFieldAccessGroupsPath, configured: config.MCPAccessGroups, priorValue: state.MCPAccessGroups, nullValue: types.ListNull(types.StringType)},
		{name: "args", fieldPath: mcpFieldArgsPath, configured: config.Args, priorValue: state.Args, nullValue: types.ListNull(types.StringType)},
		{name: "env", fieldPath: mcpFieldEnvPath, configured: config.Env, priorValue: state.Env, nullValue: types.MapNull(types.StringType)},
		{name: "credentials", fieldPath: mcpFieldCredentialsPath, configured: config.Credentials, priorValue: state.Credentials, nullValue: types.MapNull(types.StringType)},
		{name: "allowed_tools", fieldPath: mcpFieldAllowedToolsPath, configured: config.AllowedTools, priorValue: state.AllowedTools, nullValue: types.ListNull(types.StringType)},
		{name: "extra_headers", fieldPath: mcpFieldExtraHeadersPath, configured: config.ExtraHeaders, priorValue: state.ExtraHeaders, nullValue: types.ListNull(types.StringType)},
		{name: "static_headers", fieldPath: mcpFieldStaticHeadersPath, configured: config.StaticHeaders, priorValue: state.StaticHeaders, nullValue: types.MapNull(types.StringType)},
	} {
		if !hasState || !item.configured.IsNull() {
			continue
		}
		if candidateFields.Removals[item.fieldPath] {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(item.name), item.nullValue)...)
		} else if !priorFields.Owned[item.fieldPath] && !item.priorValue.IsUnknown() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(item.name), item.priorValue)...)
		}
	}

	// Preserve each imported API-owned cost leaf while that exact HCL leaf is
	// omitted. A configured parent, string sibling, or other cost sibling must
	// neither erase the projection nor receive ownership implicitly.
	planInfoChanged := false
	configuredLeaves := mcpInfoConfiguredLeafStates(config)
	preserveDefault := hasState && candidate.API[mcpInfoDefaultCostLeaf] && configuredLeaves[mcpInfoDefaultCostLeaf] == 0
	preserveTools := hasState && candidate.API[mcpInfoToolCostsLeaf] && configuredLeaves[mcpInfoToolCostsLeaf] == 0
	if (preserveDefault || preserveTools) && state.MCPInfo != nil && state.MCPInfo.MCPServerCostInfo != nil {
		if plan.MCPInfo == nil {
			plan.MCPInfo = &MCPInfoModel{
				ServerName:  types.StringNull(),
				Description: types.StringNull(),
				LogoURL:     types.StringNull(),
			}
			planInfoChanged = true
		}
		if plan.MCPInfo.MCPServerCostInfo == nil {
			plan.MCPInfo.MCPServerCostInfo = &MCPServerCostInfoModel{
				DefaultCostPerQuery:    types.Float64Null(),
				ToolNameToCostPerQuery: types.MapNull(types.Float64Type),
			}
			planInfoChanged = true
		}
		if preserveDefault && !plan.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.Equal(state.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery) {
			plan.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery = state.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery
			planInfoChanged = true
		}
		if preserveTools && !plan.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.Equal(state.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery) {
			plan.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery = state.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery
			planInfoChanged = true
		}
	}
	if planInfoChanged {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("mcp_info"), plan.MCPInfo)...)
		// Setting one nested attribute materializes the framework's internally
		// unknown Optional+Computed siblings. Restore only omitted siblings from
		// prior state so the cost-shell preservation remains a true no-op.
		for _, item := range []struct {
			name       string
			configured attr.Value
			planned    attr.Value
			prior      attr.Value
		}{
			{name: "mcp_access_groups", configured: config.MCPAccessGroups, planned: plan.MCPAccessGroups, prior: state.MCPAccessGroups},
			{name: "args", configured: config.Args, planned: plan.Args, prior: state.Args},
			{name: "env", configured: config.Env, planned: plan.Env, prior: state.Env},
			{name: "credentials", configured: config.Credentials, planned: plan.Credentials, prior: state.Credentials},
			{name: "allowed_tools", configured: config.AllowedTools, planned: plan.AllowedTools, prior: state.AllowedTools},
			{name: "extra_headers", configured: config.ExtraHeaders, planned: plan.ExtraHeaders, prior: state.ExtraHeaders},
			{name: "static_headers", configured: config.StaticHeaders, planned: plan.StaticHeaders, prior: state.StaticHeaders},
		} {
			if item.configured.IsNull() && item.planned.IsUnknown() && !item.prior.IsUnknown() {
				resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(item.name), item.prior)...)
			}
		}
	}
	if resp.Private != nil && !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(writePendingMCPInfoProvenance(ctx, resp.Private, candidate)...)
		resp.Diagnostics.Append(writePendingMCPFieldOwnership(ctx, resp.Private, candidateFields)...)
	}

	// The public generation, rather than identity churn, forces Apply for
	// ownership-only changes such as equal-value takeover.
}

func validateMCPServerOptionalResponseFields(result map[string]interface{}, stringFields, boolFields, stringListFields, stringMapFields []string) error {
	for _, field := range stringFields {
		if value, present := result[field]; present && value != nil {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional string field")
			}
		}
	}
	for _, field := range boolFields {
		if value, present := result[field]; present && value != nil {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional boolean field")
			}
		}
	}
	for _, field := range stringListFields {
		value, present := result[field]
		if !present || value == nil {
			continue
		}
		items, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("MCP server response contains a malformed optional string-list field")
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional string-list field")
			}
		}
	}
	for _, field := range stringMapFields {
		value, present := result[field]
		if !present || value == nil {
			continue
		}
		items, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("MCP server response contains a malformed optional string-map field")
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional string-map field")
			}
		}
	}
	return nil
}

func mcpKnownNonEmptyString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() != ""
}

// Python's os.path.basename on the LiteLLM runtime is deliberately not the
// same as path.Base: it does not clean the path, and a trailing slash yields an
// empty basename. Matching that behavior keeps plan validation wire-exact.
func mcpStdioCommandBaseV198(command string) string {
	if slash := strings.LastIndex(command, "/"); slash >= 0 {
		return command[slash+1:]
	}
	return command
}

func (r *MCPServerResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *MCPServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data, config MCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	priorFields := emptyMCPFieldOwnership()
	plannedOwnership, ownershipDiags := deriveMCPInfoJSONPlanProvenance(ctx, emptyMCPInfoProvenance(), config, MCPServerResourceModel{})
	resp.Diagnostics.Append(ownershipDiags...)
	plannedFields := deriveMCPFieldPlanOwnership(priorFields, config)
	if resp.Diagnostics.HasError() {
		return
	}
	resolved, err := resolveMCPInfoCreateDocument(ctx, config)
	if err != nil {
		resp.Diagnostics.AddError("Invalid MCP Info Configuration", "The complete MCP info document could not be resolved safely; no create was attempted.")
		return
	}
	mcpReq, err := r.buildMCPServerCreateRequest(ctx, &data, &config, resolved.Document, resolved.Present)
	if err != nil {
		resp.Diagnostics.AddError("Invalid MCP Request", err.Error())
		return
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/v1/mcp/server", mcpReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create MCP server: %s", err))
		return
	}
	serverID, ok := result["server_id"].(string)
	if !ok || serverID == "" {
		resp.Diagnostics.AddError("Invalid Create Response", "LiteLLM accepted the MCP server create but did not return a usable identity.")
		return
	}
	data.ServerID = types.StringValue(serverID)
	data.ID = types.StringValue(serverID)
	// CreateRequest does not carry PlannedPrivate. Re-establish the pending v2
	// bundle as soon as the POST confirms identity, then replace it with the
	// committed bundle only after successful direct readback.
	if resp.Private != nil {
		resp.Diagnostics.Append(writePendingMCPInfoProvenance(ctx, resp.Private, plannedOwnership)...)
		resp.Diagnostics.Append(writePendingMCPFieldOwnership(ctx, resp.Private, plannedFields)...)
		if resp.Diagnostics.HasError() {
			partial := partialMCPServerState(serverID)
			resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
			return
		}
	}
	planned := data
	_, _, readback, err := r.readMCPServerWithAllProvenanceDirect(ctx, &data, plannedOwnership, committedMCPFieldOwnership(plannedFields), false)
	if err != nil {
		partial := partialMCPServerState(serverID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.AddError("MCP Server Readback Not Confirmed", "LiteLLM accepted the create, but direct authoritative readback failed. Only the confirmed identity was retained for recovery.")
		return
	}
	observed, presence, err := mcpInfoDocumentFromResponse(readback)
	if err != nil || (resolved.Present && presence != apiValuePresent) {
		partial := partialMCPServerState(serverID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.AddError("MCP Server Readback Not Confirmed", "LiteLLM accepted the create, but direct readback did not return a valid MCP info object. Only the confirmed identity was retained for recovery.")
		return
	}
	if mcpOwnedEndpointReadbackMismatch(&planned, &data, nil) || verifyMCPFieldCreateReadback(ctx, config, readback, plannedFields) != nil {
		partial := partialMCPServerState(serverID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.AddError("Inconsistent MCP Endpoint Readback", "LiteLLM accepted the create but did not persist the requested endpoint or transport. Only the confirmed identity was retained for recovery.")
		return
	}
	if resolved.Present {
		if err := verifyMCPInfoReadback(map[string]interface{}{}, resolved.Document, observed, plannedOwnership); err != nil {
			partial := partialMCPServerState(serverID)
			resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
			resp.Diagnostics.AddError("Inconsistent MCP Info Readback", "LiteLLM accepted the create but did not confirm the requested MCP info document. Only the confirmed identity was retained for recovery.")
			return
		}
	}
	resolveUnknownMCPServerState(&data, nil)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(writeMCPInfoProvenance(ctx, resp.Private, plannedOwnership)...)
		resp.Diagnostics.Append(writeMCPInfoPrivateDocumentAuthoritative(ctx, resp.Private, presence == apiValuePresent)...)
		resp.Diagnostics.Append(writeMCPFieldOwnership(ctx, resp.Private, committedMCPFieldOwnership(plannedFields))...)
	}
}

func (r *MCPServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data MCPServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	fieldImportedMarker, fieldImportDiags := req.Private.GetKey(ctx, mcpFieldImportedPrivateKey)
	resp.Diagnostics.Append(fieldImportDiags...)
	if fieldImportedMarker != nil && string(fieldImportedMarker) != "true" {
		mcpFieldPrivateError(&resp.Diagnostics)
	}
	ownership, ownershipDiags := readMCPInfoProvenance(ctx, req.Private)
	resp.Diagnostics.Append(ownershipDiags...)
	fieldOwnership, fieldOwnershipDiags := readMCPFieldOwnership(ctx, req.Private)
	resp.Diagnostics.Append(fieldOwnershipDiags...)
	hasPendingOwnership, pendingOwnershipDiags := mcpInfoPrivateHasPending(ctx, req.Private)
	resp.Diagnostics.Append(pendingOwnershipDiags...)
	if resp.Diagnostics.HasError() {
		resp.State = req.State
		resp.Private = req.Private
		return
	}
	imported := string(importedMarker) == "true"
	fieldImported := string(fieldImportedMarker) == "true"
	_, adoptedAPI, result, err := r.readMCPServerWithAllProvenanceDirect(ctx, &data, ownership, fieldOwnership, imported || fieldImported)
	fieldSingular := err == nil
	fieldReadFailure := ClassifyHTTPFailure(err)
	if err != nil && !IsAPIErrorStatus(err, 404) && !(fieldReadFailure.Kind == HTTPFailureContractOrLocal && !fieldReadFailure.RequestDispatched) {
		// Preserve the historical #116/#213 collection fallback for identity and
		// MCP-info compatibility, but never use it as authority for #212 fields.
		priorFields := data
		_, adoptedAPI, result, err = r.readMCPServerWithAllProvenanceResult(ctx, &data, ownership, emptyMCPFieldOwnership(), imported)
		data.Alias, data.Description, data.Command = priorFields.Alias, priorFields.Description, priorFields.Command
		data.AuthorizationURL, data.TokenURL, data.RegistrationURL = priorFields.AuthorizationURL, priorFields.TokenURL, priorFields.RegistrationURL
		data.MCPAccessGroups, data.Args, data.Env = priorFields.MCPAccessGroups, priorFields.Args, priorFields.Env
		data.AllowedTools, data.ExtraHeaders, data.StaticHeaders = priorFields.AllowedTools, priorFields.ExtraHeaders, priorFields.StaticHeaders
		data.Credentials, data.AllowAllKeys = priorFields.Credentials, priorFields.AllowAllKeys
		resolveUnknownMCPServerState(&data, nil)
	}
	if err != nil {
		if IsAPIErrorStatus(err, 404) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Client Error", "Unable to read MCP server because LiteLLM did not return an authoritative response. Prior public and private state was retained.")
		return
	}
	_, presence, err := mcpInfoDocumentFromResponse(result)
	if err != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed MCP info root. Prior public and private state was retained.")
		return
	}
	if (imported || fieldImported) && (data.SpecVersion.IsNull() || data.SpecVersion.IsUnknown()) {
		// spec_version is provider-only compatibility state. LiteLLM v1.98 does
		// not return it, so imports must adopt the unchanged schema default.
		data.SpecVersion = types.StringValue("2024-11-05")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() || resp.Private == nil {
		return
	}
	if presence == apiValuePresent {
		// This marker records document hydration only. It neither commits nor
		// discards pending ownership from ModifyPlan.
		resp.Diagnostics.Append(writeMCPInfoPrivateDocumentAuthoritative(ctx, resp.Private, true)...)
	}
	if imported && !hasPendingOwnership && presence == apiValuePresent {
		ownership.API = adoptedAPI
		ownership.Versioned = true
		resp.Diagnostics.Append(writeMCPInfoProvenance(ctx, resp.Private, ownership)...)
		resp.Diagnostics.Append(writeMCPInfoPrivateDocumentAuthoritative(ctx, resp.Private, true)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
	// Field ownership starts empty on import. Its marker is independent from
	// #213 and clears only after this identity-valid singular read.
	if fieldImported && fieldSingular && !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(writeMCPFieldOwnership(ctx, resp.Private, fieldOwnership)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, mcpFieldImportedPrivateKey, nil)...)
	}
}

func (r *MCPServerResource) putMCPServer(ctx context.Context, request map[string]interface{}, result *map[string]interface{}) error {
	return r.client.DoRequestWithResponse(ctx, "PUT", "/v1/mcp/server", request, result)
}

func (r *MCPServerResource) updateLegacyIssue213(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, config, state MCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	committed, privateDiags := readMCPInfoProvenance(ctx, req.Private)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		resp.State = req.State
		resp.Private = req.Private
		return
	}
	plannedOwnership, pendingDiags := readPendingMCPInfoProvenance(ctx, req.Private, deriveMCPInfoPlanProvenance(committed, config, state))
	resp.Diagnostics.Append(pendingDiags...)
	if resp.Diagnostics.HasError() {
		resp.State = req.State
		resp.Private = req.Private
		return
	}
	data.ID = state.ID
	data.ServerID = state.ServerID

	// Mutation authority is only the direct singular endpoint. Hydration and
	// exact identity validation happen before request construction or PUT.
	hydrated := state
	_, _, hydrationResult, err := r.readMCPServerWithProvenanceDirect(ctx, &hydrated, plannedOwnership, false)
	if err != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("MCP Info Hydration Failed", "The direct MCP server endpoint did not return an authoritative, identity-matching response. No update was attempted and prior state was retained.")
		return
	}
	base, presence, err := mcpInfoDocumentFromResponse(hydrationResult)
	if err != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("MCP Info Hydration Failed", "The direct MCP server endpoint returned a malformed MCP info root. No update was attempted and prior state was retained.")
		return
	}
	if presence != apiValuePresent {
		authoritative, markerDiags := mcpInfoPrivateDocumentAuthoritative(ctx, req.Private)
		resp.Diagnostics.Append(markerDiags...)
		if resp.Diagnostics.HasError() || !authoritative || state.MCPInfoJSON.IsNull() || state.MCPInfoJSON.IsUnknown() {
			resp.State = req.State
			resp.Private = req.Private
			resp.Diagnostics.AddError("Authoritative MCP Info Required", "LiteLLM masked mcp_info and no previously authoritative complete document is available. No update was attempted and prior state was retained.")
			return
		}
		base, err = parseMCPInfoJSONObject(state.MCPInfoJSON.ValueString())
		if err != nil {
			resp.State = req.State
			resp.Private = req.Private
			resp.Diagnostics.AddError("Authoritative MCP Info Required", "The previously hydrated complete MCP info document is malformed. No update was attempted and prior state was retained.")
			return
		}
	}
	resolved, err := resolveMCPInfoUpdateDocument(ctx, base, config)
	if err != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Invalid MCP Info Configuration", "The complete MCP info update could not be resolved safely. No update was attempted and prior state was retained.")
		return
	}
	mcpReq, err := r.buildMCPServerRequest(ctx, &data, resolved.Document, true)
	if err != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Invalid MCP Request", err.Error())
		return
	}
	mcpReq["server_id"] = data.ServerID.ValueString()
	if !state.URL.IsNull() && !state.URL.IsUnknown() && data.URL.IsNull() {
		mcpReq["url"] = nil
	}
	if !state.SpecPath.IsNull() && !state.SpecPath.IsUnknown() && data.SpecPath.IsNull() {
		mcpReq["spec_path"] = nil
	}
	priorReq, err := r.buildMCPServerRequest(ctx, &state, nil, false)
	if err != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Invalid Prior MCP State", "The prior MCP server request projection is malformed. No update was attempted.")
		return
	}
	otherMutation := !mcpInfoJSONValuesEqual(mcpInfoRequestWithoutDocument(priorReq), mcpInfoRequestWithoutDocument(mcpReq))
	mcpMutation := !mcpInfoJSONValuesEqual(base, resolved.Document)

	planned := data
	var readback map[string]interface{}
	if otherMutation || mcpMutation {
		var updateResult map[string]interface{}
		if err := r.putMCPServer(ctx, mcpReq, &updateResult); err != nil {
			resp.State = req.State
			resp.Private = req.Private
			resp.Diagnostics.AddError("Client Error", "LiteLLM did not confirm the MCP server update. Prior public and private state was retained.")
			return
		}
		if len(updateResult) > 0 {
			if err := validateMCPServerResponse(updateResult, data.ServerID.ValueString()); err != nil {
				resp.State = req.State
				resp.Private = req.Private
				resp.Diagnostics.AddError("Invalid Update Response", "LiteLLM accepted the MCP server update but returned a malformed required response shape. Prior public and private state was retained.")
				return
			}
		}
		if mcpEndpointWasCleared(state.URL, planned.URL) {
			data.URL = types.StringUnknown()
		}
		if mcpEndpointWasCleared(state.SpecPath, planned.SpecPath) {
			data.SpecPath = types.StringUnknown()
		}
		_, _, readback, err = r.readMCPServerWithProvenanceDirect(ctx, &data, plannedOwnership, false)
	} else {
		// Equal-value takeover still requires the authoritative GET above, but it
		// commits provenance without a needless PUT.
		readback = hydrationResult
		err = r.readMCPServerResultProjection(ctx, &data, readback, plannedOwnership, emptyMCPFieldOwnership(), false, mcpInfoLeafSet{}, cloneMCPInfoLeafSet(plannedOwnership.API))
	}
	if err != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Read Error", "Authoritative direct readback failed. Prior public and private state was retained.")
		return
	}
	observed, observedPresence, err := mcpInfoDocumentFromResponse(readback)
	if err != nil || observedPresence != apiValuePresent {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Inconsistent MCP Info Readback", "The direct endpoint did not return a complete authoritative MCP info object. Prior public and private state was retained.")
		return
	}
	if mcpOwnedEndpointReadbackMismatch(&planned, &data, &state) || verifyMCPInfoReadback(base, resolved.Document, observed, plannedOwnership) != nil {
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Inconsistent MCP Info Readback", "LiteLLM did not confirm every owned, cleared, and preserved MCP info value. Prior public and private state was retained.")
		return
	}
	resolveUnknownMCPServerState(&data, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(writeMCPInfoProvenance(ctx, resp.Private, plannedOwnership)...)
		resp.Diagnostics.Append(writeMCPInfoPrivateDocumentAuthoritative(ctx, resp.Private, true)...)
	}
}

func (r *MCPServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data MCPServerResourceModel

	_, privateDiags := readMCPInfoProvenance(ctx, req.Private)
	resp.Diagnostics.Append(privateDiags...)
	_, fieldPrivateDiags := readMCPFieldOwnership(ctx, req.Private)
	resp.Diagnostics.Append(fieldPrivateDiags...)
	if resp.Diagnostics.HasError() {
		resp.State = req.State
		resp.Private = req.Private
		return
	}
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}

	endpoint := mcpServerEndpoint(serverID)
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsAPIErrorStatus(err, 404) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete MCP server: %s", err))
			return
		}
	}
}

func (r *MCPServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, mcpFieldImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(writeMCPInfoProvenance(ctx, resp.Private, emptyMCPInfoProvenance())...)
		resp.Diagnostics.Append(writeMCPFieldOwnership(ctx, resp.Private, emptyMCPFieldOwnership())...)
	}
}

// UpgradeState handles v0, v1, and v2 directly so Terraform 1.1 never needs
// to understand an intermediate schema. Existing values, flags, types, and
// blocks remain byte-for-byte compatible; only computed lifecycle controls are
// initialized.
func (r *MCPServerResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	upgrade := func(convertExtraHeaders, addMCPInfoControls bool) resource.StateUpgrader {
		return resource.StateUpgrader{PriorSchema: nil, StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
			if req.RawState == nil {
				resp.Diagnostics.AddError("Unable to Upgrade State", "RawState is nil. This is a bug in the provider.")
				return
			}
			var priorState map[string]json.RawMessage
			if err := json.Unmarshal(req.RawState.JSON, &priorState); err != nil {
				resp.Diagnostics.AddError("Unable to Upgrade State", fmt.Sprintf("Failed to unmarshal prior state JSON: %s", err))
				return
			}
			if convertExtraHeaders {
				if raw, ok := priorState["extra_headers"]; ok && string(raw) != "null" {
					var oldMap map[string]string
					if err := json.Unmarshal(raw, &oldMap); err != nil {
						resp.Diagnostics.AddError("Unable to Upgrade State", "Failed to decode legacy extra_headers.")
						return
					}
					headers := make([]string, 0, len(oldMap))
					for header := range oldMap {
						headers = append(headers, header)
					}
					sort.Strings(headers)
					converted, err := json.Marshal(headers)
					if err != nil {
						resp.Diagnostics.AddError("Unable to Upgrade State", "Failed to encode upgraded extra_headers.")
						return
					}
					priorState["extra_headers"] = converted
				}
			}
			if addMCPInfoControls {
				priorState["mcp_info_json"] = json.RawMessage("null")
				priorState["mcp_info_overrides_json"] = json.RawMessage("null")
				priorState["mcp_info_clear_paths"] = json.RawMessage("null")
				priorState["mcp_info_ownership_generation"] = json.RawMessage("0")
			}
			priorState["field_ownership_generation"] = json.RawMessage("0")
			upgradedJSON, err := json.Marshal(priorState)
			if err != nil {
				resp.Diagnostics.AddError("Unable to Upgrade State", "Failed to marshal upgraded state.")
				return
			}
			resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgradedJSON}
		}}
	}
	return map[int64]resource.StateUpgrader{0: upgrade(true, true), 1: upgrade(false, true), 2: upgrade(false, false)}
}

func (r *MCPServerResource) buildMCPServerRequest(ctx context.Context, data *MCPServerResourceModel, resolvedMCPInfo map[string]interface{}, mcpInfoPresent bool) (map[string]interface{}, error) {
	mcpReq := map[string]interface{}{
		"server_name": data.ServerName.ValueString(),
		"transport":   data.Transport.ValueString(),
		"auth_type":   data.AuthType.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string.
	if mcpKnownNonEmptyString(data.URL) {
		mcpReq["url"] = data.URL.ValueString()
	}
	if mcpKnownNonEmptyString(data.SpecPath) {
		mcpReq["spec_path"] = data.SpecPath.ValueString()
	}
	if !data.Alias.IsNull() && !data.Alias.IsUnknown() && data.Alias.ValueString() != "" {
		mcpReq["alias"] = data.Alias.ValueString()
	}
	if !data.Description.IsNull() && !data.Description.IsUnknown() && data.Description.ValueString() != "" {
		mcpReq["description"] = data.Description.ValueString()
	}
	if !data.Command.IsNull() && !data.Command.IsUnknown() && data.Command.ValueString() != "" {
		mcpReq["command"] = data.Command.ValueString()
	}
	if !data.AuthorizationURL.IsNull() && !data.AuthorizationURL.IsUnknown() && data.AuthorizationURL.ValueString() != "" {
		mcpReq["authorization_url"] = data.AuthorizationURL.ValueString()
	}
	if !data.TokenURL.IsNull() && !data.TokenURL.IsUnknown() && data.TokenURL.ValueString() != "" {
		mcpReq["token_url"] = data.TokenURL.ValueString()
	}
	if !data.RegistrationURL.IsNull() && !data.RegistrationURL.IsUnknown() && data.RegistrationURL.ValueString() != "" {
		mcpReq["registration_url"] = data.RegistrationURL.ValueString()
	}

	// Boolean fields - check IsNull and IsUnknown
	if !data.AllowAllKeys.IsNull() && !data.AllowAllKeys.IsUnknown() {
		mcpReq["allow_all_keys"] = data.AllowAllKeys.ValueBool()
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.MCPAccessGroups.IsNull() && !data.MCPAccessGroups.IsUnknown() {
		var groups []string
		data.MCPAccessGroups.ElementsAs(ctx, &groups, false)
		if len(groups) > 0 {
			mcpReq["mcp_access_groups"] = groups
		}
	}

	if !data.Args.IsNull() && !data.Args.IsUnknown() {
		var args []string
		data.Args.ElementsAs(ctx, &args, false)
		if len(args) > 0 {
			mcpReq["args"] = args
		}
	}

	if !data.AllowedTools.IsNull() && !data.AllowedTools.IsUnknown() {
		var allowedTools []string
		data.AllowedTools.ElementsAs(ctx, &allowedTools, false)
		if len(allowedTools) > 0 {
			mcpReq["allowed_tools"] = allowedTools
		}
	}

	// Map fields - check IsNull, IsUnknown, and len > 0
	if !data.Env.IsNull() && !data.Env.IsUnknown() {
		var env map[string]string
		data.Env.ElementsAs(ctx, &env, false)
		if len(env) > 0 {
			mcpReq["env"] = env
		}
	}

	if !data.Credentials.IsNull() && !data.Credentials.IsUnknown() {
		var credentials map[string]string
		data.Credentials.ElementsAs(ctx, &credentials, false)
		if len(credentials) > 0 {
			mcpReq["credentials"] = credentials
		}
	}

	if !data.ExtraHeaders.IsNull() && !data.ExtraHeaders.IsUnknown() {
		var extraHeaders []string
		data.ExtraHeaders.ElementsAs(ctx, &extraHeaders, false)
		if len(extraHeaders) > 0 {
			mcpReq["extra_headers"] = extraHeaders
		}
	}

	if !data.StaticHeaders.IsNull() && !data.StaticHeaders.IsUnknown() {
		var staticHeaders map[string]string
		data.StaticHeaders.ElementsAs(ctx, &staticHeaders, false)
		if len(staticHeaders) > 0 {
			mcpReq["static_headers"] = staticHeaders
		}
	}

	if mcpInfoPresent {
		if resolvedMCPInfo == nil {
			return nil, fmt.Errorf("resolved mcp_info must be a complete object")
		}
		if err := validateMCPInfoJSONValue(resolvedMCPInfo); err != nil {
			return nil, fmt.Errorf("resolved mcp_info is invalid")
		}
		mcpReq["mcp_info"] = cloneMCPInfoJSONObject(resolvedMCPInfo)
	}

	return mcpReq, nil
}

func mcpEndpointWasCleared(previous, planned types.String) bool {
	return !previous.IsNull() && !previous.IsUnknown() && planned.IsNull()
}

func mcpOwnedEndpointReadbackMismatch(planned, observed, previous *MCPServerResourceModel) bool {
	plannedFields := []types.String{planned.URL, planned.SpecPath, planned.Transport}
	observedFields := []types.String{observed.URL, observed.SpecPath, observed.Transport}
	previousFields := []types.String{types.StringNull(), types.StringNull(), types.StringNull()}
	if previous != nil {
		previousFields = []types.String{previous.URL, previous.SpecPath, previous.Transport}
	}
	for index := range plannedFields {
		if !plannedFields[index].IsNull() && !plannedFields[index].IsUnknown() {
			if !plannedFields[index].Equal(observedFields[index]) {
				return true
			}
			continue
		}
		if mcpEndpointWasCleared(previousFields[index], plannedFields[index]) && !observedFields[index].IsNull() {
			return true
		}
	}
	return false
}

func mcpInfoReadbackMismatch(planned, observed MCPServerResourceModel, ownership mcpInfoProvenance, confirmed mcpInfoLeafSet) bool {
	for leaf := range ownership.Terraform {
		if !confirmed[leaf] {
			return true
		}
		switch leaf {
		case mcpInfoServerNameLeaf:
			if planned.MCPInfo == nil || observed.MCPInfo == nil || !planned.MCPInfo.ServerName.Equal(observed.MCPInfo.ServerName) {
				return true
			}
		case mcpInfoDescriptionLeaf:
			if planned.MCPInfo == nil || observed.MCPInfo == nil || !planned.MCPInfo.Description.Equal(observed.MCPInfo.Description) {
				return true
			}
		case mcpInfoLogoURLLeaf:
			if planned.MCPInfo == nil || observed.MCPInfo == nil || !planned.MCPInfo.LogoURL.Equal(observed.MCPInfo.LogoURL) {
				return true
			}
		case mcpInfoDefaultCostLeaf:
			if planned.MCPInfo == nil || observed.MCPInfo == nil || planned.MCPInfo.MCPServerCostInfo == nil || observed.MCPInfo.MCPServerCostInfo == nil || !planned.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.Equal(observed.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery) {
				return true
			}
		case mcpInfoToolCostsLeaf:
			if planned.MCPInfo == nil || observed.MCPInfo == nil || planned.MCPInfo.MCPServerCostInfo == nil || observed.MCPInfo.MCPServerCostInfo == nil || !planned.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.Equal(observed.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery) {
				return true
			}
		}
	}
	return false
}

func partialMCPServerState(serverID string) MCPServerResourceModel {
	return MCPServerResourceModel{
		ID:                         types.StringValue(serverID),
		ServerID:                   types.StringValue(serverID),
		MCPAccessGroups:            types.ListNull(types.StringType),
		Args:                       types.ListNull(types.StringType),
		Env:                        types.MapNull(types.StringType),
		Credentials:                types.MapNull(types.StringType),
		AllowedTools:               types.ListNull(types.StringType),
		ExtraHeaders:               types.ListNull(types.StringType),
		StaticHeaders:              types.MapNull(types.StringType),
		MCPInfoJSON:                types.StringNull(),
		MCPInfoOverridesJSON:       types.StringNull(),
		MCPInfoClearPaths:          types.ListNull(types.StringType),
		MCPInfoOwnershipGeneration: types.Int64Value(0),
		FieldOwnershipGeneration:   types.Int64Value(0),
	}
}

func resolveUnknownMCPServerState(data *MCPServerResourceModel, previous *MCPServerResourceModel) {
	var prior MCPServerResourceModel
	if previous != nil {
		prior = *previous
	}

	resolveString := func(current, fallback types.String) types.String {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.StringNull()
	}
	resolveBool := func(current, fallback types.Bool) types.Bool {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.BoolNull()
	}
	resolveList := func(current, fallback types.List) types.List {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.ListNull(types.StringType)
	}
	resolveStringMap := func(current, fallback types.Map) types.Map {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.MapNull(types.StringType)
	}

	data.ID = resolveString(data.ID, prior.ID)
	data.ServerID = resolveString(data.ServerID, prior.ServerID)
	data.ServerName = resolveString(data.ServerName, prior.ServerName)
	data.Alias = resolveString(data.Alias, prior.Alias)
	data.Description = resolveString(data.Description, prior.Description)
	data.URL = resolveString(data.URL, prior.URL)
	data.SpecPath = resolveString(data.SpecPath, prior.SpecPath)
	data.Transport = resolveString(data.Transport, prior.Transport)
	data.SpecVersion = resolveString(data.SpecVersion, prior.SpecVersion)
	data.AuthType = resolveString(data.AuthType, prior.AuthType)
	data.Command = resolveString(data.Command, prior.Command)
	data.AuthorizationURL = resolveString(data.AuthorizationURL, prior.AuthorizationURL)
	data.TokenURL = resolveString(data.TokenURL, prior.TokenURL)
	data.RegistrationURL = resolveString(data.RegistrationURL, prior.RegistrationURL)
	data.CreatedAt = resolveString(data.CreatedAt, prior.CreatedAt)
	data.CreatedBy = resolveString(data.CreatedBy, prior.CreatedBy)
	data.AllowAllKeys = resolveBool(data.AllowAllKeys, prior.AllowAllKeys)
	data.SkipURLValidation = resolveBool(data.SkipURLValidation, prior.SkipURLValidation)
	data.MCPAccessGroups = resolveList(data.MCPAccessGroups, prior.MCPAccessGroups)
	data.Args = resolveList(data.Args, prior.Args)
	data.AllowedTools = resolveList(data.AllowedTools, prior.AllowedTools)
	data.ExtraHeaders = resolveList(data.ExtraHeaders, prior.ExtraHeaders)
	data.Env = resolveStringMap(data.Env, prior.Env)
	data.Credentials = resolveStringMap(data.Credentials, prior.Credentials)
	data.StaticHeaders = resolveStringMap(data.StaticHeaders, prior.StaticHeaders)
	data.MCPInfoJSON = resolveString(data.MCPInfoJSON, prior.MCPInfoJSON)
	data.MCPInfoOverridesJSON = resolveString(data.MCPInfoOverridesJSON, prior.MCPInfoOverridesJSON)
	data.MCPInfoClearPaths = resolveList(data.MCPInfoClearPaths, prior.MCPInfoClearPaths)
	if data.MCPInfoOwnershipGeneration.IsUnknown() {
		if previous != nil && !prior.MCPInfoOwnershipGeneration.IsUnknown() {
			data.MCPInfoOwnershipGeneration = prior.MCPInfoOwnershipGeneration
		} else {
			data.MCPInfoOwnershipGeneration = types.Int64Value(0)
		}
	}
	if data.FieldOwnershipGeneration.IsUnknown() {
		if previous != nil && !prior.FieldOwnershipGeneration.IsUnknown() {
			data.FieldOwnershipGeneration = prior.FieldOwnershipGeneration
		} else {
			data.FieldOwnershipGeneration = types.Int64Value(0)
		}
	}

	if data.MCPInfo != nil {
		var priorInfo MCPInfoModel
		if prior.MCPInfo != nil {
			priorInfo = *prior.MCPInfo
		}
		data.MCPInfo.ServerName = resolveString(data.MCPInfo.ServerName, priorInfo.ServerName)
		data.MCPInfo.Description = resolveString(data.MCPInfo.Description, priorInfo.Description)
		data.MCPInfo.LogoURL = resolveString(data.MCPInfo.LogoURL, priorInfo.LogoURL)
		if data.MCPInfo.MCPServerCostInfo != nil {
			var priorCost MCPServerCostInfoModel
			hasPriorCost := priorInfo.MCPServerCostInfo != nil
			if hasPriorCost {
				priorCost = *priorInfo.MCPServerCostInfo
			}
			if data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsUnknown() {
				if hasPriorCost && !priorCost.DefaultCostPerQuery.IsUnknown() {
					data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery = priorCost.DefaultCostPerQuery
				} else {
					data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery = types.Float64Null()
				}
			}
			if data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
				if hasPriorCost && !priorCost.ToolNameToCostPerQuery.IsUnknown() {
					data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery = priorCost.ToolNameToCostPerQuery
				} else {
					data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery = types.MapNull(types.Float64Type)
				}
			}
		}
	}
}

func mcpServerEndpoint(serverID string) string {
	return endpointWithPathSegment("/v1/mcp/server/", serverID, "")
}

func (r *MCPServerResource) getMCPServerDirect(ctx context.Context, serverID string) (map[string]interface{}, error) {
	endpoint := mcpServerEndpoint(serverID)
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *MCPServerResource) getMCPServer(ctx context.Context, serverID string) (map[string]interface{}, error) {
	endpoint := mcpServerEndpoint(serverID)
	var result map[string]interface{}
	individualErr := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result)
	if failure := ClassifyHTTPFailure(individualErr); individualErr == nil || IsAPIErrorStatus(individualErr, 404) || (failure.Kind == HTTPFailureContractOrLocal && !failure.RequestDispatched) {
		return result, individualErr
	}

	// Older LiteLLM versions can return 500 for the individual endpoint while
	// the collection endpoint still returns the successfully created server.
	delay := 250 * time.Millisecond
	for attempt := 0; attempt < 5; attempt++ {
		var servers []map[string]interface{}
		if err := r.client.DoRequestWithResponse(ctx, "GET", "/v1/mcp/server", nil, &servers); err == nil {
			for _, server := range servers {
				if id, ok := server["server_id"].(string); ok && id == serverID {
					return server, nil
				}
			}
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
	return nil, individualErr
}

func validateMCPServerResponse(result map[string]interface{}, expectedServerID string) error {
	serverID, ok := result["server_id"].(string)
	if !ok || serverID == "" {
		return fmt.Errorf("MCP server response is missing its required identity")
	}
	if expectedServerID == "" || serverID != expectedServerID {
		return fmt.Errorf("MCP server response identity does not match the requested identity")
	}

	transport, ok := result["transport"].(string)
	if !ok {
		return fmt.Errorf("MCP server response is missing its required transport")
	}
	validTransport := false
	for _, allowed := range mcpTransportsV198 {
		if transport == allowed {
			validTransport = true
			break
		}
	}
	if !validTransport {
		return fmt.Errorf("MCP server response contains an invalid transport")
	}
	return validateMCPServerOptionalResponseFields(
		result,
		[]string{"server_name", "url", "spec_path", "alias", "description", "command", "authorization_url", "token_url", "registration_url", "auth_type", "created_at", "created_by"},
		[]string{"allow_all_keys"},
		[]string{"mcp_access_groups", "args", "allowed_tools", "extra_headers"},
		[]string{"env", "static_headers", "credentials"},
	)
}

func (r *MCPServerResource) readMCPServer(ctx context.Context, data *MCPServerResourceModel) error {
	_, _, err := r.readMCPServerWithProvenance(ctx, data, mcpInfoProvenance{Terraform: mcpInfoLeafSet{}, API: mcpInfoLeafSet{}}, false)
	return err
}

// readMCPServerWithNumericOwnership remains as a narrow compatibility helper
// for tests of first-import numeric decoding. Production lifecycle paths always
// pass versioned private provenance to readMCPServerWithProvenance.
func (r *MCPServerResource) readMCPServerWithNumericOwnership(ctx context.Context, data *MCPServerResourceModel, imported bool) error {
	_, _, err := r.readMCPServerWithProvenance(ctx, data, mcpInfoProvenance{Terraform: mcpInfoLeafSet{}, API: mcpInfoLeafSet{}}, imported)
	return err
}

func (r *MCPServerResource) readMCPServerWithProvenance(ctx context.Context, data *MCPServerResourceModel, ownership mcpInfoProvenance, imported bool) (mcpInfoLeafSet, mcpInfoLeafSet, error) {
	return r.readMCPServerWithAllProvenance(ctx, data, ownership, emptyMCPFieldOwnership(), imported)
}

func (r *MCPServerResource) readMCPServerWithAllProvenance(ctx context.Context, data *MCPServerResourceModel, ownership mcpInfoProvenance, fieldOwnership mcpFieldOwnership, imported bool) (mcpInfoLeafSet, mcpInfoLeafSet, error) {
	confirmed := mcpInfoLeafSet{}
	adoptedAPI := cloneMCPInfoLeafSet(ownership.API)
	err := r.readMCPServerProjection(ctx, data, ownership, fieldOwnership, imported, confirmed, adoptedAPI)
	return confirmed, adoptedAPI, err
}

func (r *MCPServerResource) readMCPServerWithProvenanceResult(ctx context.Context, data *MCPServerResourceModel, ownership mcpInfoProvenance, imported bool) (mcpInfoLeafSet, mcpInfoLeafSet, map[string]interface{}, error) {
	return r.readMCPServerWithAllProvenanceResult(ctx, data, ownership, emptyMCPFieldOwnership(), imported)
}

func (r *MCPServerResource) readMCPServerWithAllProvenanceResult(ctx context.Context, data *MCPServerResourceModel, ownership mcpInfoProvenance, fieldOwnership mcpFieldOwnership, imported bool) (mcpInfoLeafSet, mcpInfoLeafSet, map[string]interface{}, error) {
	confirmed := mcpInfoLeafSet{}
	adoptedAPI := cloneMCPInfoLeafSet(ownership.API)
	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}
	result, err := r.getMCPServer(ctx, serverID)
	if err == nil {
		err = r.readMCPServerResultProjection(ctx, data, result, ownership, fieldOwnership, imported, confirmed, adoptedAPI)
	}
	return confirmed, adoptedAPI, result, err
}

func (r *MCPServerResource) readMCPServerWithProvenanceDirect(ctx context.Context, data *MCPServerResourceModel, ownership mcpInfoProvenance, imported bool) (mcpInfoLeafSet, mcpInfoLeafSet, map[string]interface{}, error) {
	return r.readMCPServerWithAllProvenanceDirect(ctx, data, ownership, emptyMCPFieldOwnership(), imported)
}

func (r *MCPServerResource) readMCPServerWithAllProvenanceDirect(ctx context.Context, data *MCPServerResourceModel, ownership mcpInfoProvenance, fieldOwnership mcpFieldOwnership, imported bool) (mcpInfoLeafSet, mcpInfoLeafSet, map[string]interface{}, error) {
	confirmed := mcpInfoLeafSet{}
	adoptedAPI := cloneMCPInfoLeafSet(ownership.API)
	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}
	result, err := r.getMCPServerDirect(ctx, serverID)
	if err == nil {
		err = r.readMCPServerResultProjection(ctx, data, result, ownership, fieldOwnership, imported, confirmed, adoptedAPI)
	}
	return confirmed, adoptedAPI, result, err
}

func (r *MCPServerResource) readMCPServerProjection(ctx context.Context, data *MCPServerResourceModel, ownership mcpInfoProvenance, fieldOwnership mcpFieldOwnership, imported bool, confirmed, adoptedAPI mcpInfoLeafSet) error {
	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}
	result, err := r.getMCPServer(ctx, serverID)
	if err != nil {
		return err
	}
	return r.readMCPServerResultProjection(ctx, data, result, ownership, fieldOwnership, imported, confirmed, adoptedAPI)
}

func (r *MCPServerResource) readMCPServerResultProjection(_ context.Context, data *MCPServerResourceModel, result map[string]interface{}, ownership mcpInfoProvenance, fieldOwnership mcpFieldOwnership, imported bool, confirmed, adoptedAPI mcpInfoLeafSet) error {
	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}
	if err := validateMCPServerResponse(result, serverID); err != nil {
		return err
	}

	// Update fields from response
	if serverID, ok := result["server_id"].(string); ok {
		data.ServerID = types.StringValue(serverID)
		data.ID = types.StringValue(serverID)
	}
	if serverName, ok := result["server_name"].(string); ok {
		data.ServerName = types.StringValue(serverName)
	}
	projectString := func(fieldPath, name string, current *types.String) {
		projected := fieldOwnership.Owned[fieldPath] || (!current.IsNull() && !current.IsUnknown())
		if !projected {
			return
		}
		raw, present := result[name]
		if !present {
			return
		}
		if raw == nil {
			*current = types.StringNull()
			return
		}
		*current = types.StringValue(raw.(string))
	}
	projectString(mcpFieldAliasPath, "alias", &data.Alias)
	projectString(mcpFieldDescriptionPath, "description", &data.Description)
	urlOwned := imported || !data.URL.IsNull()
	if urlOwned {
		if remoteURL, ok := result["url"].(string); ok {
			data.URL = types.StringValue(remoteURL)
		} else {
			data.URL = types.StringNull()
		}
	}
	specPathOwned := imported || !data.SpecPath.IsNull()
	if specPathOwned {
		if specPath, ok := result["spec_path"].(string); ok {
			data.SpecPath = types.StringValue(specPath)
		} else {
			data.SpecPath = types.StringNull()
		}
	}
	if transport, ok := result["transport"].(string); ok {
		data.Transport = types.StringValue(transport)
	}
	if authType, ok := result["auth_type"].(string); ok {
		data.AuthType = types.StringValue(authType)
	}
	if imported && data.Command.IsNull() {
		if command, ok := result["command"].(string); ok {
			data.Command = types.StringValue(command)
		}
	} else {
		projectString(mcpFieldCommandPath, "command", &data.Command)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if createdBy, ok := result["created_by"].(string); ok {
		data.CreatedBy = types.StringValue(createdBy)
	}
	projectList := func(fieldPath, name string, current *types.List) {
		raw, present := result[name]
		items, visible := raw.([]interface{})
		projected := fieldOwnership.Owned[fieldPath] || imported || !current.IsNull()
		if !present || raw == nil || !visible || len(items) == 0 || !projected {
			if current.IsUnknown() {
				*current, _ = types.ListValue(types.StringType, []attr.Value{})
			}
			return
		}
		values := make([]attr.Value, len(items))
		for index, item := range items {
			values[index] = types.StringValue(item.(string))
		}
		*current, _ = types.ListValue(types.StringType, values)
	}
	projectMap := func(fieldPath, name string, current *types.Map) {
		raw, present := result[name]
		items, visible := raw.(map[string]interface{})
		projected := fieldOwnership.Owned[fieldPath] || imported || !current.IsNull()
		if !present || raw == nil || !visible || len(items) == 0 || !projected {
			if current.IsUnknown() {
				*current, _ = types.MapValue(types.StringType, map[string]attr.Value{})
			}
			return
		}
		values := make(map[string]attr.Value, len(items))
		for name, item := range items {
			values[name] = types.StringValue(item.(string))
		}
		*current, _ = types.MapValue(types.StringType, values)
	}
	projectList(mcpFieldAccessGroupsPath, "mcp_access_groups", &data.MCPAccessGroups)
	projectList(mcpFieldArgsPath, "args", &data.Args)
	projectMap(mcpFieldEnvPath, "env", &data.Env)
	projectList(mcpFieldAllowedToolsPath, "allowed_tools", &data.AllowedTools)
	projectList(mcpFieldExtraHeadersPath, "extra_headers", &data.ExtraHeaders)
	projectMap(mcpFieldStaticHeadersPath, "static_headers", &data.StaticHeaders)

	// Credential values are never authoritative in the management response.
	// Keep configured sensitive values through redaction and keep imports null.
	if data.Credentials.IsUnknown() {
		data.Credentials = types.MapNull(types.StringType)
	}

	projectString(mcpFieldAuthorizationURLPath, "authorization_url", &data.AuthorizationURL)
	projectString(mcpFieldTokenURLPath, "token_url", &data.TokenURL)
	projectString(mcpFieldRegistrationURLPath, "registration_url", &data.RegistrationURL)
	if fieldOwnership.Owned[mcpFieldAllowAllKeysPath] || (!data.AllowAllKeys.IsNull() && !data.AllowAllKeys.IsUnknown()) {
		if allowAllKeys, present := result["allow_all_keys"].(bool); present {
			data.AllowAllKeys = types.BoolValue(allowAllKeys)
		}
	}

	// A present object is the sole authoritative complete MCP-info snapshot.
	// Absence or null is role masking and leaves every public projection intact.
	mcpInfoRaw, mcpInfoPresence, err := mcpInfoDocumentFromResponse(result)
	if err != nil {
		return fmt.Errorf("invalid MCP server response: mcp_info must be a JSON object or null")
	}
	if mcpInfoPresence != apiValuePresent {
		return nil
	}
	if err := setCompleteMCPInfoJSONState(data, mcpInfoRaw); err != nil {
		return fmt.Errorf("invalid MCP server response: mcp_info could not be represented")
	}

	costRaw, _ := mcpInfoRaw["mcp_server_cost_info"].(map[string]interface{})
	if imported && costRaw != nil {
		if raw, present := costRaw["default_cost_per_query"]; present {
			if _, compatible := mcpInfoCompatibleFloat(raw); compatible {
				adoptedAPI[mcpInfoDefaultCostLeaf] = true
			}
		}
		if raw, present := costRaw["tool_name_to_cost_per_query"]; present {
			if _, compatible := mcpInfoCompatibleFloatMap(raw); compatible {
				adoptedAPI[mcpInfoToolCostsLeaf] = true
			}
		}
	}
	leafOwned := func(leaf string) bool {
		return ownership.Terraform[leaf] || ownership.API[leaf] || adoptedAPI[leaf]
	}
	anyOwned := false
	for _, leaf := range mcpInfoAllLeaves {
		anyOwned = anyOwned || leafOwned(leaf)
	}
	if !anyOwned {
		data.MCPInfo = nil
		return nil
	}
	priorInfo := data.MCPInfo
	info := &MCPInfoModel{ServerName: types.StringNull(), Description: types.StringNull(), LogoURL: types.StringNull()}
	if priorInfo != nil {
		info.ServerName = priorInfo.ServerName
		info.Description = priorInfo.Description
		info.LogoURL = priorInfo.LogoURL
	}
	for _, field := range []struct {
		name string
		leaf string
		set  func(types.String)
	}{
		{name: "server_name", leaf: mcpInfoServerNameLeaf, set: func(value types.String) { info.ServerName = value }},
		{name: "description", leaf: mcpInfoDescriptionLeaf, set: func(value types.String) { info.Description = value }},
		{name: "logo_url", leaf: mcpInfoLogoURLLeaf, set: func(value types.String) { info.LogoURL = value }},
	} {
		if !leafOwned(field.leaf) {
			continue
		}
		confirmed[field.leaf] = true
		raw, present := mcpInfoRaw[field.name]
		if !present {
			continue
		}
		if raw == nil {
			field.set(types.StringNull())
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("invalid MCP server response: an owned fixed MCP info string has an incompatible type")
		}
		field.set(types.StringValue(value))
	}

	if leafOwned(mcpInfoDefaultCostLeaf) || leafOwned(mcpInfoToolCostsLeaf) {
		costs := &MCPServerCostInfoModel{DefaultCostPerQuery: types.Float64Null(), ToolNameToCostPerQuery: types.MapNull(types.Float64Type)}
		if priorInfo != nil && priorInfo.MCPServerCostInfo != nil {
			costs.DefaultCostPerQuery = priorInfo.MCPServerCostInfo.DefaultCostPerQuery
			costs.ToolNameToCostPerQuery = priorInfo.MCPServerCostInfo.ToolNameToCostPerQuery
		}
		if leafOwned(mcpInfoDefaultCostLeaf) {
			confirmed[mcpInfoDefaultCostLeaf] = true
			if raw, present := costRaw["default_cost_per_query"]; present && raw == nil {
				costs.DefaultCostPerQuery = types.Float64Null()
			} else if present {
				value, ok := mcpInfoCompatibleFloat(raw)
				if !ok {
					return fmt.Errorf("invalid MCP server response: an owned fixed MCP info cost has an incompatible type")
				}
				costs.DefaultCostPerQuery = types.Float64Value(value)
			}
		}
		if leafOwned(mcpInfoToolCostsLeaf) {
			confirmed[mcpInfoToolCostsLeaf] = true
			if raw, present := costRaw["tool_name_to_cost_per_query"]; present && raw == nil {
				costs.ToolNameToCostPerQuery = types.MapNull(types.Float64Type)
			} else if present {
				values, ok := mcpInfoCompatibleFloatMap(raw)
				if !ok {
					return fmt.Errorf("invalid MCP server response: an owned fixed MCP info cost map has an incompatible type")
				}
				elements := make(map[string]attr.Value, len(values))
				for name, value := range values {
					elements[name] = types.Float64Value(value)
				}
				costs.ToolNameToCostPerQuery, _ = types.MapValue(types.Float64Type, elements)
			}
		}
		info.MCPServerCostInfo = costs
	}
	data.MCPInfo = info
	return nil
}
