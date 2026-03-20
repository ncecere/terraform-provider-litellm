package provider

import (
	"context"
	"fmt"

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

	var result []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/v1/agents", nil, &result); err != nil {
		// The API may return a top-level array or an object with a key.
		// Try unwrapping if needed.
		var wrapped map[string]interface{}
		if err2 := d.client.DoRequestWithResponse(ctx, "GET", "/v1/agents", nil, &wrapped); err2 == nil {
			if agents, ok := wrapped["agents"].([]interface{}); ok {
				for _, a := range agents {
					if m, ok := a.(map[string]interface{}); ok {
						result = append(result, m)
					}
				}
			}
		}
		if result == nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list agents: %s", err))
			return
		}
	}

	agents := make([]AgentListItemModel, 0, len(result))
	for _, item := range result {
		agent := AgentListItemModel{}
		if v, ok := item["agent_id"].(string); ok {
			agent.AgentID = types.StringValue(v)
		}
		if v, ok := item["agent_name"].(string); ok {
			agent.AgentName = types.StringValue(v)
		}
		if v, ok := item["tpm_limit"].(float64); ok {
			agent.TPMLimit = types.Int64Value(int64(v))
		}
		if v, ok := item["rpm_limit"].(float64); ok {
			agent.RPMLimit = types.Int64Value(int64(v))
		}
		if v, ok := item["session_tpm_limit"].(float64); ok {
			agent.SessionTPMLimit = types.Int64Value(int64(v))
		}
		if v, ok := item["session_rpm_limit"].(float64); ok {
			agent.SessionRPMLimit = types.Int64Value(int64(v))
		}
		if v, ok := item["spend"].(float64); ok {
			agent.Spend = types.Float64Value(v)
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

	data.ID = types.StringValue("agents-list")
	data.Agents = agents

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
