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

var _ datasource.DataSource = &OrganizationDataSource{}

func NewOrganizationDataSource() datasource.DataSource { return &OrganizationDataSource{} }

type OrganizationDataSource struct{ client *Client }

type OrganizationDataSourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	OrganizationID      types.String  `tfsdk:"organization_id"`
	OrganizationAlias   types.String  `tfsdk:"organization_alias"`
	Models              types.List    `tfsdk:"models"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	ModelRPMLimit       types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit       types.Map     `tfsdk:"model_tpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	Metadata            types.Map     `tfsdk:"metadata"`
	Blocked             types.Bool    `tfsdk:"blocked"`
	Tags                types.List    `tfsdk:"tags"`
	Spend               types.Float64 `tfsdk:"spend"`
	CreatedAt           types.String  `tfsdk:"created_at"`
	UpdatedAt           types.String  `tfsdk:"updated_at"`
}

func (d *OrganizationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (d *OrganizationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a LiteLLM organization, including authoritative nested budget controls.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The unique identifier for this organization.", Computed: true},
			"organization_id":       schema.StringAttribute{Description: "The organization ID to look up.", Required: true},
			"organization_alias":    schema.StringAttribute{Description: "The name/alias of the organization.", Computed: true},
			"models":                schema.ListAttribute{Description: "The models the organization has access to.", Computed: true, ElementType: types.StringType},
			"budget_id":             schema.StringAttribute{Description: "The organization's budget ID.", Computed: true},
			"max_budget":            schema.Float64Attribute{Description: "Maximum hard budget.", Computed: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget alert threshold.", Computed: true},
			"tpm_limit":             schema.Int64Attribute{Description: "Maximum tokens per minute.", Computed: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Maximum requests per minute.", Computed: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Maximum parallel requests.", Computed: true},
			"model_rpm_limit":       schema.MapAttribute{Description: "Per-model RPM limits stored in metadata.", Computed: true, ElementType: types.Int64Type},
			"model_tpm_limit":       schema.MapAttribute{Description: "Per-model TPM limits stored in metadata.", Computed: true, ElementType: types.Int64Type},
			"budget_duration":       schema.StringAttribute{Description: "Budget reset duration.", Computed: true},
			"metadata":              schema.MapAttribute{Description: "Metadata excluding dedicated per-model rate maps.", Computed: true, ElementType: types.StringType},
			"blocked":               schema.BoolAttribute{Description: "Compatibility field. LiteLLM v1.98 has no organization blocked column, so this is false.", Computed: true},
			"tags":                  schema.ListAttribute{Description: "Compatibility field. LiteLLM v1.98 has no organization tags column, so this is empty.", Computed: true, ElementType: types.StringType},
			"spend":                 schema.Float64Attribute{Description: "Amount spent by this organization.", Computed: true},
			"created_at":            schema.StringAttribute{Description: "Creation timestamp.", Computed: true},
			"updated_at":            schema.StringAttribute{Description: "Last update timestamp.", Computed: true},
		},
	}
}

func (d *OrganizationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
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
	organizationID := data.OrganizationID.ValueString()
	var result map[string]interface{}
	endpoint := "/organization/info?organization_id=" + url.QueryEscape(organizationID)
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization %q: %s", organizationID, err))
		return
	}
	object, err := unwrapObjectEnvelope(result, "organization_info", "data")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := validateImportedObjectIdentity(true, "organization data source", object, "organization_id", organizationID); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	table, err := parseBudgetTable(object)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	budgetID, budgetPresence, err := budgetTableID(object, table)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.ID = types.StringValue(organizationID)
	if budgetPresence == apiValuePresent {
		data.BudgetID = types.StringValue(budgetID)
	} else {
		data.BudgetID = types.StringNull()
	}
	if alias, ok := object["organization_alias"].(string); ok {
		data.OrganizationAlias = types.StringValue(alias)
	} else {
		data.OrganizationAlias = types.StringNull()
	}
	models, presence, err := stringListFromAPI(object, "models")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if presence == apiValuePresent {
		data.Models = models
	} else {
		data.Models = types.ListNull(types.StringType)
	}
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget}, {"soft_budget", &data.SoftBudget},
	} {
		if err := updateBudgetFloat64(field.target, table, true, true, field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit}, {"rpm_limit", &data.RPMLimit}, {"max_parallel_requests", &data.MaxParallelRequests},
	} {
		if err := updateBudgetInt64(field.target, table, true, true, field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	if err := updateBudgetDuration(&data.BudgetDuration, table, true, true); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateFloat64FromAPI(&data.Spend, object, true, true, "spend"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	metadata, metadataPresence, err := stringMapFromAPI(object, "metadata", "model_rpm_limit", "model_tpm_limit")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if metadataPresence == apiValuePresent {
		data.Metadata = metadata
	} else {
		data.Metadata = types.MapNull(types.StringType)
	}
	if err := updateInt64MapFromAPI(&data.ModelRPMLimit, object, true, true, "metadata", "model_rpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateInt64MapFromAPI(&data.ModelTPMLimit, object, true, true, "metadata", "model_tpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.Blocked = types.BoolValue(false)
	data.Tags = types.ListValueMust(types.StringType, []attr.Value{})
	if err := updateNullableString(&data.CreatedAt, object, "created_at"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateNullableString(&data.UpdatedAt, object, "updated_at"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
