package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AgentDataSource{}

func NewAgentDataSource() datasource.DataSource {
	return &AgentDataSource{}
}

type AgentDataSource struct {
	client *Client
}

type AgentDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	AgentName        types.String `tfsdk:"agent_name"`
	AgentCardParams  types.Map    `tfsdk:"agent_card_params"`
	LiteLLMParams    types.Map    `tfsdk:"litellm_params"`
	TPMLimit         types.Int64  `tfsdk:"tpm_limit"`
	RPMLimit         types.Int64  `tfsdk:"rpm_limit"`
	SessionTPMLimit  types.Int64  `tfsdk:"session_tpm_limit"`
	SessionRPMLimit  types.Int64  `tfsdk:"session_rpm_limit"`
	StaticHeaders    types.Map    `tfsdk:"static_headers"`
	ExtraHeaders     types.List   `tfsdk:"extra_headers"`
	Spend            types.Float64 `tfsdk:"spend"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	CreatedBy        types.String `tfsdk:"created_by"`
	UpdatedBy        types.String `tfsdk:"updated_by"`
}

func (d *AgentDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (d *AgentDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM Agent (A2A).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The agent ID to look up.",
				Required:    true,
			},
			"agent_name": schema.StringAttribute{
				Description: "The name of the agent.",
				Computed:    true,
			},
			"agent_card_params": schema.MapAttribute{
				Description: "The agent card parameters as a flat string map.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"litellm_params": schema.MapAttribute{
				Description: "LiteLLM-specific parameters for the agent.",
				Computed:    true,
				ElementType: types.StringType,
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
			"static_headers": schema.MapAttribute{
				Description: "Static headers sent with agent requests.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"extra_headers": schema.ListAttribute{
				Description: "Extra header names forwarded from incoming requests.",
				Computed:    true,
				ElementType: types.StringType,
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
	}
}

func (d *AgentDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *AgentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AgentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/v1/agents/%s", url.PathEscape(data.ID.ValueString()))

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read agent: %s", err))
		return
	}

	if v, ok := result["agent_id"].(string); ok {
		data.ID = types.StringValue(v)
	}
	if v, ok := result["agent_name"].(string); ok {
		data.AgentName = types.StringValue(v)
	}

	// Flatten agent_card_params to a string map for datasource simplicity
	if cardRaw, ok := result["agent_card_params"].(map[string]interface{}); ok {
		cardMap := map[string]attr.Value{}
		for k, v := range cardRaw {
			cardMap[k] = types.StringValue(fmt.Sprintf("%v", v))
		}
		data.AgentCardParams, _ = types.MapValue(types.StringType, cardMap)
	} else {
		data.AgentCardParams = types.MapNull(types.StringType)
	}

	// LiteLLM params
	if params, ok := result["litellm_params"].(map[string]interface{}); ok && len(params) > 0 {
		paramMap := map[string]attr.Value{}
		for k, v := range params {
			paramMap[k] = types.StringValue(fmt.Sprintf("%v", v))
		}
		data.LiteLLMParams, _ = types.MapValue(types.StringType, paramMap)
	} else {
		data.LiteLLMParams = types.MapNull(types.StringType)
	}

	// Rate limits
	if v, ok := result["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(v))
	}
	if v, ok := result["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(v))
	}
	if v, ok := result["session_tpm_limit"].(float64); ok {
		data.SessionTPMLimit = types.Int64Value(int64(v))
	}
	if v, ok := result["session_rpm_limit"].(float64); ok {
		data.SessionRPMLimit = types.Int64Value(int64(v))
	}

	// Spend
	if v, ok := result["spend"].(float64); ok {
		data.Spend = types.Float64Value(v)
	}

	// Static headers
	if headers, ok := result["static_headers"].(map[string]interface{}); ok && len(headers) > 0 {
		headerMap := map[string]attr.Value{}
		for k, v := range headers {
			headerMap[k] = types.StringValue(fmt.Sprintf("%v", v))
		}
		data.StaticHeaders, _ = types.MapValue(types.StringType, headerMap)
	} else {
		data.StaticHeaders = types.MapNull(types.StringType)
	}

	// Extra headers
	if headers, ok := result["extra_headers"].([]interface{}); ok && len(headers) > 0 {
		vals := make([]attr.Value, 0, len(headers))
		for _, h := range headers {
			if s, ok := h.(string); ok {
				vals = append(vals, types.StringValue(s))
			}
		}
		data.ExtraHeaders, _ = types.ListValue(types.StringType, vals)
	} else {
		data.ExtraHeaders = types.ListNull(types.StringType)
	}

	// Computed timestamps
	if v, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(v)
	}
	if v, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(v)
	}
	if v, ok := result["created_by"].(string); ok {
		data.CreatedBy = types.StringValue(v)
	}
	if v, ok := result["updated_by"].(string); ok {
		data.UpdatedBy = types.StringValue(v)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
