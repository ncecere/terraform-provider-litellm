package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PolicyDataSource{}

func NewPolicyDataSource() datasource.DataSource {
	return &PolicyDataSource{}
}

type PolicyDataSource struct {
	client *Client
}

type PolicyDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	PolicyID         types.String `tfsdk:"policy_id"`
	PolicyName       types.String `tfsdk:"policy_name"`
	Inherit          types.String `tfsdk:"inherit"`
	Description      types.String `tfsdk:"description"`
	GuardrailsAdd    types.List   `tfsdk:"guardrails_add"`
	GuardrailsRemove types.List   `tfsdk:"guardrails_remove"`
	Condition        types.Object `tfsdk:"condition"`
	Pipeline         types.String `tfsdk:"pipeline"`

	VersionNumber   types.Int64  `tfsdk:"version_number"`
	VersionStatus   types.String `tfsdk:"version_status"`
	ParentVersionID types.String `tfsdk:"parent_version_id"`
	IsLatest        types.Bool   `tfsdk:"is_latest"`
	PublishedAt     types.String `tfsdk:"published_at"`
	ProductionAt    types.String `tfsdk:"production_at"`
	CreatedAt       types.String `tfsdk:"created_at"`
	UpdatedAt       types.String `tfsdk:"updated_at"`
	CreatedBy       types.String `tfsdk:"created_by"`
	UpdatedBy       types.String `tfsdk:"updated_by"`
}

func (d *PolicyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}

func (d *PolicyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM policy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The policy ID.",
				Computed:    true,
			},
			"policy_id": schema.StringAttribute{
				Description: "The policy ID to look up.",
				Required:    true,
			},
			"policy_name": schema.StringAttribute{
				Description: "Policy name.",
				Computed:    true,
			},
			"inherit": schema.StringAttribute{
				Description: "Name of parent policy.",
				Computed:    true,
			},
			"description": schema.StringAttribute{
				Description: "Human-readable policy description.",
				Computed:    true,
			},
			"guardrails_add": schema.ListAttribute{
				Description: "Guardrails to add.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"guardrails_remove": schema.ListAttribute{
				Description: "Guardrails to remove from inherited set.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"condition": schema.SingleNestedAttribute{
				Description: "Condition for when this policy applies.",
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"model": schema.StringAttribute{
						Description: "Model name pattern (exact match or regex).",
						Computed:    true,
					},
				},
			},
			"pipeline": schema.StringAttribute{
				Description: "JSON string defining optional guardrail pipeline.",
				Computed:    true,
			},

			"version_number": schema.Int64Attribute{
				Description: "Version number.",
				Computed:    true,
			},
			"version_status": schema.StringAttribute{
				Description: "Version status (draft, published, production).",
				Computed:    true,
			},
			"parent_version_id": schema.StringAttribute{
				Description: "Policy ID this version was cloned from.",
				Computed:    true,
			},
			"is_latest": schema.BoolAttribute{
				Description: "Whether this is the latest version.",
				Computed:    true,
			},
			"published_at": schema.StringAttribute{
				Description: "Timestamp when this version was published.",
				Computed:    true,
			},
			"production_at": schema.StringAttribute{
				Description: "Timestamp when this version was promoted to production.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the policy was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the policy was last updated.",
				Computed:    true,
			},
			"created_by": schema.StringAttribute{
				Description: "Who created the policy.",
				Computed:    true,
			},
			"updated_by": schema.StringAttribute{
				Description: "Who last updated the policy.",
				Computed:    true,
			},
		},
	}
}

func (d *PolicyDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PolicyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PolicyDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/policies/%s", data.PolicyID.ValueString())

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read policy: %s", err))
		return
	}

	if v, ok := result["policy_id"].(string); ok && v != "" {
		data.PolicyID = types.StringValue(v)
		data.ID = types.StringValue(v)
	} else {
		data.ID = data.PolicyID
	}
	if v, ok := result["policy_name"].(string); ok {
		data.PolicyName = types.StringValue(v)
	}
	if v, ok := result["inherit"].(string); ok && v != "" {
		data.Inherit = types.StringValue(v)
	} else {
		data.Inherit = types.StringNull()
	}
	if v, ok := result["description"].(string); ok && v != "" {
		data.Description = types.StringValue(v)
	} else {
		data.Description = types.StringNull()
	}

	if v, ok := result["version_number"].(float64); ok {
		data.VersionNumber = types.Int64Value(int64(v))
	} else {
		data.VersionNumber = types.Int64Null()
	}
	if v, ok := result["version_status"].(string); ok && v != "" {
		data.VersionStatus = types.StringValue(v)
	} else {
		data.VersionStatus = types.StringNull()
	}
	if v, ok := result["parent_version_id"].(string); ok && v != "" {
		data.ParentVersionID = types.StringValue(v)
	} else {
		data.ParentVersionID = types.StringNull()
	}
	if v, ok := result["is_latest"].(bool); ok {
		data.IsLatest = types.BoolValue(v)
	} else {
		data.IsLatest = types.BoolNull()
	}
	if v, ok := result["published_at"].(string); ok && v != "" {
		data.PublishedAt = types.StringValue(v)
	} else {
		data.PublishedAt = types.StringNull()
	}
	if v, ok := result["production_at"].(string); ok && v != "" {
		data.ProductionAt = types.StringValue(v)
	} else {
		data.ProductionAt = types.StringNull()
	}
	if v, ok := result["created_at"].(string); ok && v != "" {
		data.CreatedAt = types.StringValue(v)
	} else {
		data.CreatedAt = types.StringNull()
	}
	if v, ok := result["updated_at"].(string); ok && v != "" {
		data.UpdatedAt = types.StringValue(v)
	} else {
		data.UpdatedAt = types.StringNull()
	}
	if v, ok := result["created_by"].(string); ok && v != "" {
		data.CreatedBy = types.StringValue(v)
	} else {
		data.CreatedBy = types.StringNull()
	}
	if v, ok := result["updated_by"].(string); ok && v != "" {
		data.UpdatedBy = types.StringValue(v)
	} else {
		data.UpdatedBy = types.StringNull()
	}

	if values, ok := result["guardrails_add"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(values))
		for _, value := range values {
			if strValue, ok := value.(string); ok {
				list = append(list, types.StringValue(strValue))
			}
		}
		data.GuardrailsAdd, _ = types.ListValue(types.StringType, list)
	} else {
		data.GuardrailsAdd, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	if values, ok := result["guardrails_remove"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(values))
		for _, value := range values {
			if strValue, ok := value.(string); ok {
				list = append(list, types.StringValue(strValue))
			}
		}
		data.GuardrailsRemove, _ = types.ListValue(types.StringType, list)
	} else {
		data.GuardrailsRemove, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	if condition, ok := result["condition"].(map[string]interface{}); ok && len(condition) > 0 {
		conditionModel := ""
		if v, ok := condition["model"].(string); ok {
			conditionModel = v
		}
		obj, err := policyConditionObject(conditionModel)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to map condition: %s", err))
			return
		}
		data.Condition = obj
	} else {
		data.Condition = policyConditionNullObject()
	}

	if pipeline, ok := result["pipeline"]; ok && pipeline != nil {
		pipelineBytes, err := json.Marshal(pipeline)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to encode pipeline: %s", err))
			return
		}
		data.Pipeline = types.StringValue(string(pipelineBytes))
	} else {
		data.Pipeline = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
