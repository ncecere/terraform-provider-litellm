package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &GuardrailsListDataSource{}

func NewGuardrailsListDataSource() datasource.DataSource {
	return &GuardrailsListDataSource{}
}

type GuardrailsListDataSource struct {
	client *Client
}

type GuardrailsListDataSourceModel struct {
	ID         types.String             `tfsdk:"id"`
	Guardrails []GuardrailListItemModel `tfsdk:"guardrails"`
}

type GuardrailListItemModel struct {
	GuardrailID   types.String `tfsdk:"guardrail_id"`
	GuardrailName types.String `tfsdk:"guardrail_name"`
	Guardrail     types.String `tfsdk:"guardrail"`
	Mode          types.String `tfsdk:"mode"`
	DefaultOn     types.Bool   `tfsdk:"default_on"`
	LitellmParams types.String `tfsdk:"litellm_params"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

func (d *GuardrailsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrails"
}

func (d *GuardrailsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM guardrails.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"guardrails": schema.ListNestedAttribute{
				Description: "List of guardrails.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"guardrail_id": schema.StringAttribute{
							Description: "The guardrail ID.",
							Computed:    true,
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
							Description: "When to apply the guardrail.",
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
						"created_at": schema.StringAttribute{
							Description: "Timestamp when the guardrail was created.",
							Computed:    true,
						},
						"updated_at": schema.StringAttribute{
							Description: "Timestamp when the guardrail was last updated.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *GuardrailsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *GuardrailsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data GuardrailsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var results []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/guardrails/list", nil, &results); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list guardrails: %s", err))
		return
	}

	guardrails := make([]GuardrailListItemModel, 0, len(results))
	for _, result := range results {
		guardrail := GuardrailListItemModel{}

		if guardrailID, ok := result["guardrail_id"].(string); ok {
			guardrail.GuardrailID = types.StringValue(guardrailID)
		}
		if name, ok := result["guardrail_name"].(string); ok {
			guardrail.GuardrailName = types.StringValue(name)
		}
		if createdAt, ok := result["created_at"].(string); ok {
			guardrail.CreatedAt = types.StringValue(createdAt)
		}
		if updatedAt, ok := result["updated_at"].(string); ok {
			guardrail.UpdatedAt = types.StringValue(updatedAt)
		}

		// Handle litellm_params
		if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
			if g, ok := litellmParams["guardrail"].(string); ok {
				guardrail.Guardrail = types.StringValue(g)
			}
			if defaultOn, ok := litellmParams["default_on"].(bool); ok {
				guardrail.DefaultOn = types.BoolValue(defaultOn)
			}

			// Handle mode
			if mode, ok := litellmParams["mode"].(string); ok {
				guardrail.Mode = types.StringValue(mode)
			} else if modeArray, ok := litellmParams["mode"].([]interface{}); ok {
				if jsonBytes, err := json.Marshal(modeArray); err == nil {
					guardrail.Mode = types.StringValue(string(jsonBytes))
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
					guardrail.LitellmParams = types.StringValue(string(jsonBytes))
				}
			}
		}

		guardrails = append(guardrails, guardrail)
	}

	data.ID = types.StringValue("guardrails")
	data.Guardrails = guardrails

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
