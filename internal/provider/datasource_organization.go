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
	ModelRPMLimit     types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit     types.Map     `tfsdk:"model_tpm_limit"`
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
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits stored by LiteLLM in organization metadata.",
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits stored by LiteLLM in organization metadata.",
				Computed:    true,
				ElementType: types.Int64Type,
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

	// The /organization/info endpoint may return data nested inside "organization_info"
	orgInfo := result
	if nested, ok := result["organization_info"].(map[string]interface{}); ok {
		orgInfo = nested
	}

	// Set ID
	data.ID = data.OrganizationID

	// Update fields from response
	if alias, ok := orgInfo["organization_alias"].(string); ok {
		data.OrganizationAlias = types.StringValue(alias)
	}
	if budgetID, ok := orgInfo["budget_id"].(string); ok {
		data.BudgetID = types.StringValue(budgetID)
	}
	if budgetDuration, presence, err := apiValueAt(orgInfo, "litellm_budget_table", "budget_duration"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	} else if presence == apiValuePresent {
		value, ok := budgetDuration.(string)
		if !ok {
			resp.Diagnostics.AddError("Invalid API Response", "invalid response field \"litellm_budget_table.budget_duration\": expected a string")
			return
		}
		data.BudgetDuration = types.StringValue(value)
	} else {
		data.BudgetDuration = types.StringNull()
	}
	if createdAt, ok := orgInfo["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if updatedAt, ok := orgInfo["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}

	// Numeric fields
	if err := updateFloat64FromAPI(&data.MaxBudget, orgInfo, true, true, "litellm_budget_table", "max_budget"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateFloat64FromAPI(&data.Spend, orgInfo, true, true, "spend"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateInt64FromAPI(&data.TPMLimit, orgInfo, true, true, "litellm_budget_table", "tpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateInt64FromAPI(&data.RPMLimit, orgInfo, true, true, "litellm_budget_table", "rpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}

	// Boolean fields
	if blocked, ok := orgInfo["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	} else {
		data.Blocked = types.BoolValue(false)
	}

	// Handle models list
	if models, ok := orgInfo["models"].([]interface{}); ok {
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
	if tags, ok := orgInfo["tags"].([]interface{}); ok {
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

	// Organization metadata and its two reserved numeric maps are one atomic
	// response unit. Validate both maps before publishing any related state.
	metadata, metadataPresent := orgInfo["metadata"].(map[string]interface{})
	metaMap := make(map[string]attr.Value)
	if metadataPresent {
		for key, value := range metadata {
			if key == "model_rpm_limit" || key == "model_tpm_limit" {
				continue
			}
			metaMap[key] = types.StringValue(metadataValueToString(value))
		}
	}
	nextMetadata, _ := types.MapValue(types.StringType, metaMap)
	nextModelRPM := types.MapNull(types.Int64Type)
	nextModelTPM := types.MapNull(types.Int64Type)
	if err := updateInt64MapFromAPI(&nextModelRPM, orgInfo, true, true, "metadata", "model_rpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateInt64MapFromAPI(&nextModelTPM, orgInfo, true, true, "metadata", "model_tpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.Metadata = nextMetadata
	data.ModelRPMLimit = nextModelRPM
	data.ModelTPMLimit = nextModelTPM

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
