package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AgentDataSource{}

func NewAgentDataSource() datasource.DataSource { return &AgentDataSource{} }

type AgentDataSource struct{ client *Client }

type AgentDataSourceModel struct {
	ID                   types.String  `tfsdk:"id"`
	AgentName            types.String  `tfsdk:"agent_name"`
	AgentCardParams      types.Map     `tfsdk:"agent_card_params"`
	AgentCardParamsJSON  types.String  `tfsdk:"agent_card_params_json"`
	LiteLLMParams        types.Map     `tfsdk:"litellm_params"`
	LiteLLMParamsJSON    types.String  `tfsdk:"litellm_params_json"`
	ObjectPermissionJSON types.String  `tfsdk:"object_permission_json"`
	TPMLimit             types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit             types.Int64   `tfsdk:"rpm_limit"`
	SessionTPMLimit      types.Int64   `tfsdk:"session_tpm_limit"`
	SessionRPMLimit      types.Int64   `tfsdk:"session_rpm_limit"`
	StaticHeaders        types.Map     `tfsdk:"static_headers"`
	ExtraHeaders         types.List    `tfsdk:"extra_headers"`
	Spend                types.Float64 `tfsdk:"spend"`
	CreatedAt            types.String  `tfsdk:"created_at"`
	UpdatedAt            types.String  `tfsdk:"updated_at"`
	CreatedBy            types.String  `tfsdk:"created_by"`
	UpdatedBy            types.String  `tfsdk:"updated_by"`
}

func agentDataComputedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"agent_name":             schema.StringAttribute{Description: "The name of the agent.", Computed: true},
		"agent_card_params":      schema.MapAttribute{Description: "Historical flat map(string) compatibility projection; nested card values use deterministic JSON rendering.", Computed: true, Sensitive: true, ElementType: types.StringType},
		"agent_card_params_json": schema.StringAttribute{Description: "Canonical lossless JSON object containing every observable agent-card field.", Computed: true, Sensitive: true},
		"litellm_params":         schema.MapAttribute{Description: "Historical map(string) compatibility projection; heterogeneous API values use deterministic exact JSON rendering while litellm_params_json preserves wire types.", Computed: true, Sensitive: true, ElementType: types.StringType},
		"litellm_params_json":    schema.StringAttribute{Description: "Canonical lossless JSON object containing every observable heterogeneous LiteLLM parameter.", Computed: true, Sensitive: true},
		"object_permission_json": schema.StringAttribute{Description: "Canonical lossless JSON object containing every observable object-permission field.", Computed: true},
		"tpm_limit":              schema.Int64Attribute{Description: "Tokens per minute limit.", Computed: true},
		"rpm_limit":              schema.Int64Attribute{Description: "Requests per minute limit.", Computed: true},
		"session_tpm_limit":      schema.Int64Attribute{Description: "Per-session tokens per minute limit.", Computed: true},
		"session_rpm_limit":      schema.Int64Attribute{Description: "Per-session requests per minute limit.", Computed: true},
		"static_headers":         schema.MapAttribute{Description: "Static headers sent with agent requests.", Computed: true, Sensitive: true, ElementType: types.StringType},
		"extra_headers":          schema.ListAttribute{Description: "Extra header names forwarded from incoming requests.", Computed: true, ElementType: types.StringType},
		"spend":                  schema.Float64Attribute{Description: "Total spend for this agent.", Computed: true},
		"created_at":             schema.StringAttribute{Description: "Timestamp when the agent was created.", Computed: true},
		"updated_at":             schema.StringAttribute{Description: "Timestamp when the agent was last updated.", Computed: true},
		"created_by":             schema.StringAttribute{Description: "User who created the agent.", Computed: true},
		"updated_by":             schema.StringAttribute{Description: "User who last updated the agent.", Computed: true},
	}
}

func (d *AgentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (d *AgentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := agentDataComputedAttributes()
	attributes["id"] = schema.StringAttribute{Description: "The exact agent ID to look up.", Required: true}
	resp.Schema = schema.Schema{Description: "Retrieves a lossless, strictly validated LiteLLM Agent (A2A) projection.", Attributes: attributes}
}

func (d *AgentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func nullAgentDataProjection(id types.String) AgentDataSourceModel {
	return AgentDataSourceModel{
		ID: id, AgentName: types.StringNull(), AgentCardParams: types.MapNull(types.StringType), AgentCardParamsJSON: types.StringNull(),
		LiteLLMParams: types.MapNull(types.StringType), LiteLLMParamsJSON: types.StringNull(), ObjectPermissionJSON: types.StringNull(),
		TPMLimit: types.Int64Null(), RPMLimit: types.Int64Null(), SessionTPMLimit: types.Int64Null(), SessionRPMLimit: types.Int64Null(),
		StaticHeaders: types.MapNull(types.StringType), ExtraHeaders: types.ListNull(types.StringType), Spend: types.Float64Null(),
		CreatedAt: types.StringNull(), UpdatedAt: types.StringNull(), CreatedBy: types.StringNull(), UpdatedBy: types.StringNull(),
	}
}

func projectAgentData(ctx context.Context, item map[string]interface{}, expectedID string) (AgentDataSourceModel, error) {
	data := nullAgentDataProjection(types.StringValue(expectedID))
	if err := validateImportedObjectIdentity(true, "agent", item, "agent_id", expectedID); err != nil {
		return data, err
	}
	if err := requireImportedStringField(true, "agent", item, "agent_name"); err != nil {
		return data, err
	}
	data.ID = types.StringValue(item["agent_id"].(string))
	data.AgentName = types.StringValue(item["agent_name"].(string))

	if raw, present := item["agent_card_params"]; present && raw != nil {
		card, ok := raw.(map[string]interface{})
		if !ok {
			return data, fmt.Errorf("invalid agent_card_params")
		}
		if err := validateAgentCardResponse(ctx, card, false); err != nil {
			return data, err
		}
		legacy, err := agentStringProjection(card, false)
		if err != nil {
			return data, err
		}
		data.AgentCardParams = legacy
		encoded, err := canonicalAgentJSON(card)
		if err != nil {
			return data, err
		}
		data.AgentCardParamsJSON = types.StringValue(encoded)
	}
	if raw, present := item["litellm_params"]; present && raw != nil {
		params, ok := raw.(map[string]interface{})
		if !ok {
			return data, fmt.Errorf("invalid litellm_params")
		}
		legacy, err := agentStringProjection(params, true)
		if err != nil {
			return data, err
		}
		data.LiteLLMParams = legacy
		structured, err := reconcileAgentJSONObject(types.StringNull(), params)
		if err != nil {
			return data, fmt.Errorf("unrecoverable masked litellm_params")
		}
		data.LiteLLMParamsJSON = structured
	}
	if raw, present := item["object_permission"]; present && raw != nil {
		permission, ok := raw.(map[string]interface{})
		if !ok {
			return data, fmt.Errorf("invalid object_permission")
		}
		temporary := emptyKnownAgentResourceModel()
		if err := (&AgentResource{}).readObjectPermissionContext(ctx, permission, &temporary); err != nil {
			return data, err
		}
		encoded, err := canonicalAgentJSON(permission)
		if err != nil {
			return data, err
		}
		data.ObjectPermissionJSON = types.StringValue(encoded)
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit}, {"rpm_limit", &data.RPMLimit}, {"session_tpm_limit", &data.SessionTPMLimit}, {"session_rpm_limit", &data.SessionRPMLimit},
	} {
		if err := updateInt64FromAPI(field.target, item, true, true, field.name); err != nil {
			return data, err
		}
	}
	if err := updateFloat64FromAPI(&data.Spend, item, true, true, "spend"); err != nil {
		return data, err
	}
	staticHeaders, staticPresence, staticDiagnostics := strictAPIStringMap(ctx, item, "static_headers", path.Root("static_headers"), true)
	if staticDiagnostics.HasError() {
		return data, collectionProjectionError(ctx, staticDiagnostics)
	}
	if staticPresence == apiValuePresent {
		data.StaticHeaders = staticHeaders
	}
	extraHeaders, extraPresence, extraDiagnostics := strictAPIStringList(ctx, item, "extra_headers", path.Root("extra_headers"))
	if extraDiagnostics.HasError() {
		return data, collectionProjectionError(ctx, extraDiagnostics)
	}
	if extraPresence == apiValuePresent {
		data.ExtraHeaders = extraHeaders
	}
	for _, field := range []struct {
		name   string
		target *types.String
	}{
		{"created_at", &data.CreatedAt}, {"updated_at", &data.UpdatedAt}, {"created_by", &data.CreatedBy}, {"updated_by", &data.UpdatedBy},
	} {
		if raw, present := item[field.name]; present && raw != nil {
			value, ok := raw.(string)
			if !ok {
				return data, fmt.Errorf("invalid %s", field.name)
			}
			*field.target = types.StringValue(value)
		}
	}
	return data, nil
}

func (d *AgentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config AgentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var result map[string]interface{}
	endpoint := endpointWithPathSegment("/v1/agents/", config.ID.ValueString(), "")
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", "Unable to read the requested agent authoritatively.")
		return
	}
	data, err := projectAgentData(ctx, result, config.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed or identity-mismatched agent.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
