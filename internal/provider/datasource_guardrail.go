package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &GuardrailDataSource{}

func NewGuardrailDataSource() datasource.DataSource {
	return &GuardrailDataSource{}
}

type GuardrailDataSource struct {
	client *Client
}

type GuardrailDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	GuardrailID   types.String `tfsdk:"guardrail_id"`
	GuardrailName types.String `tfsdk:"guardrail_name"`
	Guardrail     types.String `tfsdk:"guardrail"`
	Mode          types.String `tfsdk:"mode"`
	DefaultOn     types.Bool   `tfsdk:"default_on"`
	LitellmParams types.String `tfsdk:"litellm_params"`
	GuardrailInfo types.String `tfsdk:"guardrail_info"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

func (d *GuardrailDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrail"
}

func (d *GuardrailDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM guardrail.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this guardrail.",
				Computed:    true,
			},
			"guardrail_id": schema.StringAttribute{
				Description: "The guardrail ID to look up.",
				Required:    true,
			},
			"guardrail_name": schema.StringAttribute{
				Description: "Human-readable name for the guardrail.",
				Computed:    true,
			},
			"guardrail": schema.StringAttribute{
				Description: "The guardrail integration type.",
				Computed:    true,
			},
			"mode": schema.StringAttribute{
				Description: "When to apply the guardrail (pre_call, post_call, during_call, or JSON array).",
				Computed:    true,
			},
			"default_on": schema.BoolAttribute{
				Description: "Whether the guardrail is enabled by default.",
				Computed:    true,
			},
			"litellm_params": schema.StringAttribute{
				Description: "JSON string containing additional provider-specific parameters.",
				Computed:    true,
			},
			"guardrail_info": schema.StringAttribute{
				Description: "JSON string containing additional metadata.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the guardrail was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the guardrail was last updated.",
				Computed:    true,
			},
		},
	}
}

func (d *GuardrailDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	d.client = client
}

func (d *GuardrailDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data GuardrailDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	guardrailID := data.GuardrailID.ValueString()
	endpoint := fmt.Sprintf("/guardrails/%s/info", guardrailID)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read guardrail: %s", err))
		return
	}

	// Populate the data model
	data.ID = types.StringValue(guardrailID)

	if name, ok := result["guardrail_name"].(string); ok {
		data.GuardrailName = types.StringValue(name)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if updatedAt, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}

	// Handle litellm_params
	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if guardrail, ok := litellmParams["guardrail"].(string); ok {
			data.Guardrail = types.StringValue(guardrail)
		}
		if defaultOn, ok := litellmParams["default_on"].(bool); ok {
			data.DefaultOn = types.BoolValue(defaultOn)
		}

		// Handle mode (can be string or array)
		if mode, ok := litellmParams["mode"].(string); ok {
			data.Mode = types.StringValue(mode)
		} else if modeArray, ok := litellmParams["mode"].([]interface{}); ok {
			if jsonBytes, err := json.Marshal(modeArray); err == nil {
				data.Mode = types.StringValue(string(jsonBytes))
			}
		}

		// Store other litellm_params as JSON
		otherParams := make(map[string]interface{})
		for k, v := range litellmParams {
			if k != "guardrail" && k != "mode" && k != "default_on" {
				otherParams[k] = v
			}
		}
		if len(otherParams) > 0 {
			if jsonBytes, err := json.Marshal(otherParams); err == nil {
				data.LitellmParams = types.StringValue(string(jsonBytes))
			}
		}
	}

	// Handle guardrail_info
	if guardrailInfo, ok := result["guardrail_info"].(map[string]interface{}); ok && len(guardrailInfo) > 0 {
		if jsonBytes, err := json.Marshal(guardrailInfo); err == nil {
			data.GuardrailInfo = types.StringValue(string(jsonBytes))
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
