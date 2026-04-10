package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PoliciesListDataSource{}

func NewPoliciesListDataSource() datasource.DataSource {
	return &PoliciesListDataSource{}
}

type PoliciesListDataSource struct {
	client *Client
}

type PolicyListItemModel struct {
	PolicyID         types.String `tfsdk:"policy_id"`
	PolicyName       types.String `tfsdk:"policy_name"`
	VersionNumber    types.Int64  `tfsdk:"version_number"`
	VersionStatus    types.String `tfsdk:"version_status"`
	ParentVersionID  types.String `tfsdk:"parent_version_id"`
	IsLatest         types.Bool   `tfsdk:"is_latest"`
	Inherit          types.String `tfsdk:"inherit"`
	Description      types.String `tfsdk:"description"`
	GuardrailsAdd    types.List   `tfsdk:"guardrails_add"`
	GuardrailsRemove types.List   `tfsdk:"guardrails_remove"`
	Condition        types.String `tfsdk:"condition"`
	Pipeline         types.String `tfsdk:"pipeline"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	CreatedBy        types.String `tfsdk:"created_by"`
	UpdatedBy        types.String `tfsdk:"updated_by"`
}

type PoliciesListDataSourceModel struct {
	ID            types.String          `tfsdk:"id"`
	VersionStatus types.String          `tfsdk:"version_status"`
	Policies      []PolicyListItemModel `tfsdk:"policies"`
	TotalCount    types.Int64           `tfsdk:"total_count"`
}

func (d *PoliciesListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policies"
}

func (d *PoliciesListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of LiteLLM policies.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"version_status": schema.StringAttribute{
				Description: "Optional filter for version status (draft, published, production).",
				Optional:    true,
			},
			"total_count": schema.Int64Attribute{
				Description: "Total number of returned policies.",
				Computed:    true,
			},
			"policies": schema.ListNestedAttribute{
				Description: "List of policies.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"policy_id":         schema.StringAttribute{Description: "The policy ID.", Computed: true},
						"policy_name":       schema.StringAttribute{Description: "Policy name.", Computed: true},
						"version_number":    schema.Int64Attribute{Description: "Version number.", Computed: true},
						"version_status":    schema.StringAttribute{Description: "Version status.", Computed: true},
						"parent_version_id": schema.StringAttribute{Description: "Parent version ID.", Computed: true},
						"is_latest":         schema.BoolAttribute{Description: "Whether this is latest.", Computed: true},
						"inherit":           schema.StringAttribute{Description: "Parent policy name.", Computed: true},
						"description":       schema.StringAttribute{Description: "Policy description.", Computed: true},
						"guardrails_add":    schema.ListAttribute{Description: "Guardrails to add.", Computed: true, ElementType: types.StringType},
						"guardrails_remove": schema.ListAttribute{Description: "Guardrails to remove.", Computed: true, ElementType: types.StringType},
						"condition":         schema.StringAttribute{Description: "Condition as JSON string.", Computed: true},
						"pipeline":          schema.StringAttribute{Description: "Pipeline as JSON string.", Computed: true},
						"created_at":        schema.StringAttribute{Description: "Creation timestamp.", Computed: true},
						"updated_at":        schema.StringAttribute{Description: "Last update timestamp.", Computed: true},
						"created_by":        schema.StringAttribute{Description: "Creator.", Computed: true},
						"updated_by":        schema.StringAttribute{Description: "Last updater.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *PoliciesListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PoliciesListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PoliciesListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := "/policies/list"
	if !data.VersionStatus.IsNull() && !data.VersionStatus.IsUnknown() && data.VersionStatus.ValueString() != "" {
		endpoint = endpoint + "?version_status=" + url.QueryEscape(data.VersionStatus.ValueString())
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list policies: %s", err))
		return
	}

	data.ID = types.StringValue("policies")

	if v, ok := result["total_count"].(float64); ok {
		data.TotalCount = types.Int64Value(int64(v))
	} else {
		data.TotalCount = types.Int64Value(0)
	}

	data.Policies = make([]PolicyListItemModel, 0)
	policiesData, _ := result["policies"].([]interface{})

	for _, policyRaw := range policiesData {
		policyMap, ok := policyRaw.(map[string]interface{})
		if !ok {
			continue
		}

		item := PolicyListItemModel{
			Inherit:         types.StringNull(),
			Description:     types.StringNull(),
			ParentVersionID: types.StringNull(),
			Condition:       types.StringNull(),
			Pipeline:        types.StringNull(),
			CreatedAt:       types.StringNull(),
			UpdatedAt:       types.StringNull(),
			CreatedBy:       types.StringNull(),
			UpdatedBy:       types.StringNull(),
		}

		if v, ok := policyMap["policy_id"].(string); ok {
			item.PolicyID = types.StringValue(v)
		}
		if v, ok := policyMap["policy_name"].(string); ok {
			item.PolicyName = types.StringValue(v)
		}
		if v, ok := policyMap["version_number"].(float64); ok {
			item.VersionNumber = types.Int64Value(int64(v))
		}
		if v, ok := policyMap["version_status"].(string); ok {
			item.VersionStatus = types.StringValue(v)
		}
		if v, ok := policyMap["parent_version_id"].(string); ok && v != "" {
			item.ParentVersionID = types.StringValue(v)
		}
		if v, ok := policyMap["is_latest"].(bool); ok {
			item.IsLatest = types.BoolValue(v)
		}
		if v, ok := policyMap["inherit"].(string); ok && v != "" {
			item.Inherit = types.StringValue(v)
		}
		if v, ok := policyMap["description"].(string); ok && v != "" {
			item.Description = types.StringValue(v)
		}
		if v, ok := policyMap["created_at"].(string); ok && v != "" {
			item.CreatedAt = types.StringValue(v)
		}
		if v, ok := policyMap["updated_at"].(string); ok && v != "" {
			item.UpdatedAt = types.StringValue(v)
		}
		if v, ok := policyMap["created_by"].(string); ok && v != "" {
			item.CreatedBy = types.StringValue(v)
		}
		if v, ok := policyMap["updated_by"].(string); ok && v != "" {
			item.UpdatedBy = types.StringValue(v)
		}

		if values, ok := policyMap["guardrails_add"].([]interface{}); ok {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			item.GuardrailsAdd, _ = types.ListValue(types.StringType, list)
		} else {
			item.GuardrailsAdd, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if values, ok := policyMap["guardrails_remove"].([]interface{}); ok {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			item.GuardrailsRemove, _ = types.ListValue(types.StringType, list)
		} else {
			item.GuardrailsRemove, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if condition, ok := policyMap["condition"]; ok && condition != nil {
			conditionBytes, err := json.Marshal(condition)
			if err == nil {
				item.Condition = types.StringValue(string(conditionBytes))
			}
		}

		if pipeline, ok := policyMap["pipeline"]; ok && pipeline != nil {
			pipelineBytes, err := json.Marshal(pipeline)
			if err == nil {
				item.Pipeline = types.StringValue(string(pipelineBytes))
			}
		}

		data.Policies = append(data.Policies, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
