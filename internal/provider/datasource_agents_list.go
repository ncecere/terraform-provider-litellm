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

func NewAgentsListDataSource() datasource.DataSource {
	return &AgentsListDataSource{}
}

type AgentsListDataSource struct {
	client *Client
}

type AgentsListDataSourceModel struct {
	ID     types.String         `tfsdk:"id"`
	Agents []AgentListItemModel `tfsdk:"agents"`
}

type AgentListItemModel struct {
	AgentID         types.String  `tfsdk:"agent_id"`
	AgentName       types.String  `tfsdk:"agent_name"`
	TPMLimit        types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit        types.Int64   `tfsdk:"rpm_limit"`
	SessionTPMLimit types.Int64   `tfsdk:"session_tpm_limit"`
	SessionRPMLimit types.Int64   `tfsdk:"session_rpm_limit"`
	Spend           types.Float64 `tfsdk:"spend"`
	CreatedAt       types.String  `tfsdk:"created_at"`
	UpdatedAt       types.String  `tfsdk:"updated_at"`
	CreatedBy       types.String  `tfsdk:"created_by"`
	UpdatedBy       types.String  `tfsdk:"updated_by"`
}

func (d *AgentsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents"
}

func (d *AgentsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM Agents (A2A).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"agents": schema.ListNestedAttribute{
				Description: "List of agents.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"agent_id": schema.StringAttribute{
							Description: "The unique agent ID.",
							Computed:    true,
						},
						"agent_name": schema.StringAttribute{
							Description: "The name of the agent.",
							Computed:    true,
						},
						"tpm_limit": schema.Int64Attribute{
							Description: "Tokens per minute limit.",
							Computed:    true,
						},
						"rpm_limit": schema.Int64Attribute{
							Description: "Requests per minute limit.",
							Computed:    true,
						},
						"session_tpm_limit": schema.Int64Attribute{
							Description: "Per-session tokens per minute limit.",
							Computed:    true,
						},
						"session_rpm_limit": schema.Int64Attribute{
							Description: "Per-session requests per minute limit.",
							Computed:    true,
						},
						"spend": schema.Float64Attribute{
							Description: "Total spend for this agent.",
							Computed:    true,
						},
						"created_at": schema.StringAttribute{
							Description: "Timestamp when the agent was created.",
							Computed:    true,
						},
						"updated_at": schema.StringAttribute{
							Description: "Timestamp when the agent was last updated.",
							Computed:    true,
						},
						"created_by": schema.StringAttribute{
							Description: "User who created the agent.",
							Computed:    true,
						},
						"updated_by": schema.StringAttribute{
							Description: "User who last updated the agent.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *AgentsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func (d *AgentsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AgentsListDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := fetchTopLevelListObjects(ctx, d.client, "/v1/agents", "agent item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list agents: %s", err))
		return
	}

	agents := make([]AgentListItemModel, 0, len(result))
	for _, item := range result {
		agent := AgentListItemModel{}
		if v, ok := item["agent_id"].(string); ok && v != "" {
			agent.AgentID = types.StringValue(v)
		} else {
			resp.Diagnostics.AddError("Invalid API Response", "/v1/agents returned an agent object without agent_id")
			return
		}
		if v, ok := item["agent_name"].(string); ok {
			agent.AgentName = types.StringValue(v)
		}
		for _, field := range []struct {
			name   string
			target *types.Int64
		}{
			{"tpm_limit", &agent.TPMLimit},
			{"rpm_limit", &agent.RPMLimit},
			{"session_tpm_limit", &agent.SessionTPMLimit},
			{"session_rpm_limit", &agent.SessionRPMLimit},
		} {
			if err := updateInt64FromAPI(field.target, item, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		if err := updateFloat64FromAPI(&agent.Spend, item, true, true, "spend"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if v, ok := item["created_at"].(string); ok {
			agent.CreatedAt = types.StringValue(v)
		}
		if v, ok := item["updated_at"].(string); ok {
			agent.UpdatedAt = types.StringValue(v)
		}
		if v, ok := item["created_by"].(string); ok {
			agent.CreatedBy = types.StringValue(v)
		}
		if v, ok := item["updated_by"].(string); ok {
			agent.UpdatedBy = types.StringValue(v)
		}
		agents = append(agents, agent)
	}
	sort.SliceStable(agents, func(i, j int) bool {
		return agents[i].AgentID.ValueString() < agents[j].AgentID.ValueString()
	})

	data.ID = types.StringValue("agents-list")
	data.Agents = agents

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
