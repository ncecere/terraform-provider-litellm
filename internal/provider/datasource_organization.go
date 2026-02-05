package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &OrganizationDataSource{}

func NewOrganizationDataSource() datasource.DataSource {
	return &OrganizationDataSource{}
}

type OrganizationDataSource struct {
	client *Client
}

type OrganizationDataSourceModel struct {
	ID                types.String  `tfsdk:"id"`
	OrganizationID    types.String  `tfsdk:"organization_id"`
	OrganizationAlias types.String  `tfsdk:"organization_alias"`
	Models            types.List    `tfsdk:"models"`
	BudgetID          types.String  `tfsdk:"budget_id"`
	MaxBudget         types.Float64 `tfsdk:"max_budget"`
	TPMLimit          types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit          types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration    types.String  `tfsdk:"budget_duration"`
	Metadata          types.Map     `tfsdk:"metadata"`
	Blocked           types.Bool    `tfsdk:"blocked"`
	Tags              types.List    `tfsdk:"tags"`
	Spend             types.Float64 `tfsdk:"spend"`
	CreatedAt         types.String  `tfsdk:"created_at"`
	UpdatedAt         types.String  `tfsdk:"updated_at"`
}

func (d *OrganizationDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (d *OrganizationDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM organization.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this organization.",
				Computed:    true,
			},
			"organization_id": schema.StringAttribute{
				Description: "The organization ID to look up.",
				Required:    true,
			},
			"organization_alias": schema.StringAttribute{
				Description: "The name/alias of the organization.",
				Computed:    true,
			},
			"models": schema.ListAttribute{
				Description: "The models the organization has access to.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"budget_id": schema.StringAttribute{
				Description: "The ID for a budget for the organization.",
				Computed:    true,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Max budget for the organization.",
				Computed:    true,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Max TPM limit for the organization.",
				Computed:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Max RPM limit for the organization.",
				Computed:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Frequency of resetting org budget.",
				Computed:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the organization.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Flag indicating if the org is blocked.",
				Computed:    true,
			},
			"tags": schema.ListAttribute{
				Description: "Tags for the organization.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"spend": schema.Float64Attribute{
				Description: "Amount spent by this organization.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the organization was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the organization was last updated.",
				Computed:    true,
			},
		},
	}
}

func (d *OrganizationDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OrganizationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OrganizationDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.OrganizationID.ValueString()
	endpoint := fmt.Sprintf("/organization/info?organization_id=%s", orgID)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization '%s': %s", orgID, err))
		return
	}

	// Set ID
	data.ID = data.OrganizationID

	// Update fields from response
	if alias, ok := result["organization_alias"].(string); ok {
		data.OrganizationAlias = types.StringValue(alias)
	}
	if budgetID, ok := result["budget_id"].(string); ok {
		data.BudgetID = types.StringValue(budgetID)
	}
	if budgetDuration, ok := result["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if updatedAt, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}

	// Numeric fields
	if maxBudget, ok := result["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if spend, ok := result["spend"].(float64); ok {
		data.Spend = types.Float64Value(spend)
	}
	if tpmLimit, ok := result["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	}
	if rpmLimit, ok := result["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	}

	// Boolean fields
	if blocked, ok := result["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	} else {
		data.Blocked = types.BoolValue(false)
	}

	// Handle models list
	if models, ok := result["models"].([]interface{}); ok {
		modelsList := make([]attr.Value, len(models))
		for i, m := range models {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	} else {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle tags list
	if tags, ok := result["tags"].([]interface{}); ok {
		tagsList := make([]attr.Value, len(tags))
		for i, t := range tags {
			if str, ok := t.(string); ok {
				tagsList[i] = types.StringValue(str)
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, tagsList)
	} else {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle metadata map
	if metadata, ok := result["metadata"].(map[string]interface{}); ok {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			if str, ok := v.(string); ok {
				metaMap[k] = types.StringValue(str)
			}
		}
		data.Metadata, _ = types.MapValue(types.StringType, metaMap)
	} else {
		data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
