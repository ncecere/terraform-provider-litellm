package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AgentsListDataSource{}

func NewAgentsListDataSource() datasource.DataSource { return &AgentsListDataSource{} }

type AgentsListDataSource struct{ client *Client }

type AgentsListDataSourceModel struct {
	ID     types.String         `tfsdk:"id"`
	Agents []AgentListItemModel `tfsdk:"agents"`
}

type AgentListItemModel struct {
	AgentID              types.String  `tfsdk:"agent_id"`
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

func (d *AgentsListDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents"
}

func (d *AgentsListDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	itemAttributes := agentDataComputedAttributes()
	itemAttributes["agent_id"] = schema.StringAttribute{Description: "The unique agent ID.", Computed: true}
	resp.Schema = schema.Schema{
		Description: "Fetches lossless, strictly validated projections of all visible LiteLLM Agents (A2A). Role-sanitized omitted fields are null for that item.",
		Attributes: map[string]schema.Attribute{
			"id":     schema.StringAttribute{Description: "Stable data-source identifier.", Computed: true},
			"agents": schema.ListNestedAttribute{Description: "Agents sorted by agent_id.", Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: itemAttributes}},
		},
	}
}

func (d *AgentsListDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func agentListItem(projected AgentDataSourceModel) AgentListItemModel {
	return AgentListItemModel{
		AgentID: projected.ID, AgentName: projected.AgentName, AgentCardParams: projected.AgentCardParams, AgentCardParamsJSON: projected.AgentCardParamsJSON,
		LiteLLMParams: projected.LiteLLMParams, LiteLLMParamsJSON: projected.LiteLLMParamsJSON, ObjectPermissionJSON: projected.ObjectPermissionJSON,
		TPMLimit: projected.TPMLimit, RPMLimit: projected.RPMLimit, SessionTPMLimit: projected.SessionTPMLimit, SessionRPMLimit: projected.SessionRPMLimit,
		StaticHeaders: projected.StaticHeaders, ExtraHeaders: projected.ExtraHeaders, Spend: projected.Spend,
		CreatedAt: projected.CreatedAt, UpdatedAt: projected.UpdatedAt, CreatedBy: projected.CreatedBy, UpdatedBy: projected.UpdatedBy,
	}
}

func (d *AgentsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AgentsListDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := fetchTopLevelListObjects(ctx, d.client, "/v1/agents", "agent item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Unable to list agents authoritatively.")
		return
	}
	agents := make([]AgentListItemModel, 0, len(result))
	for _, item := range result {
		id, ok := item["agent_id"].(string)
		if !ok || id == "" {
			resp.Diagnostics.AddError("Invalid API Response", "/v1/agents returned an agent object without a valid agent_id.")
			return
		}
		projected, err := projectAgentData(item, id)
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", "/v1/agents returned a malformed agent object.")
			return
		}
		agents = append(agents, agentListItem(projected))
	}
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].AgentID.ValueString() < agents[j].AgentID.ValueString() })
	data.ID = types.StringValue("agents-list")
	data.Agents = agents
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
