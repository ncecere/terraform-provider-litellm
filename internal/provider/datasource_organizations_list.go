package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &OrganizationsListDataSource{}

func NewOrganizationsListDataSource() datasource.DataSource { return &OrganizationsListDataSource{} }

type OrganizationsListDataSource struct{ client *Client }

type OrganizationListItem struct {
	OrganizationID      types.String  `tfsdk:"organization_id"`
	OrganizationAlias   types.String  `tfsdk:"organization_alias"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	Spend               types.Float64 `tfsdk:"spend"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	ModelRPMLimit       types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit       types.Map     `tfsdk:"model_tpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	Blocked             types.Bool    `tfsdk:"blocked"`
}

type OrganizationsListDataSourceModel struct {
	ID            types.String           `tfsdk:"id"`
	OrgAlias      types.String           `tfsdk:"org_alias"`
	Organizations []OrganizationListItem `tfsdk:"organizations"`
}

func (d *OrganizationsListDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organizations"
}

func (d *OrganizationsListDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	budgetAttributes := map[string]schema.Attribute{
		"organization_id":       schema.StringAttribute{Description: "Organization ID.", Computed: true},
		"organization_alias":    schema.StringAttribute{Description: "Organization alias.", Computed: true},
		"budget_id":             schema.StringAttribute{Description: "Associated budget ID.", Computed: true},
		"max_budget":            schema.Float64Attribute{Description: "Maximum hard budget.", Computed: true},
		"soft_budget":           schema.Float64Attribute{Description: "Soft budget alert threshold.", Computed: true},
		"spend":                 schema.Float64Attribute{Description: "Organization spend.", Computed: true},
		"tpm_limit":             schema.Int64Attribute{Description: "Tokens per minute limit.", Computed: true},
		"rpm_limit":             schema.Int64Attribute{Description: "Requests per minute limit.", Computed: true},
		"max_parallel_requests": schema.Int64Attribute{Description: "Maximum parallel requests.", Computed: true},
		"model_rpm_limit":       schema.MapAttribute{Description: "Per-model RPM limits stored in metadata.", Computed: true, ElementType: types.Int64Type},
		"model_tpm_limit":       schema.MapAttribute{Description: "Per-model TPM limits stored in metadata.", Computed: true, ElementType: types.Int64Type},
		"budget_duration":       schema.StringAttribute{Description: "Budget reset duration.", Computed: true},
		"blocked":               schema.BoolAttribute{Description: "Compatibility field; always false because v1.98 has no organization blocked column.", Computed: true},
	}
	resp.Schema = schema.Schema{Description: "Retrieves LiteLLM organizations with authoritative nested budget inventories.", Attributes: map[string]schema.Attribute{
		"id":            schema.StringAttribute{Description: "Stable historical identifier.", Computed: true},
		"org_alias":     schema.StringAttribute{Description: "Optional alias filter.", Optional: true},
		"organizations": schema.ListNestedAttribute{Description: "Organizations.", Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: budgetAttributes}},
	}}
}

func (d *OrganizationsListDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OrganizationsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OrganizationsListDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("org_alias"), &data.OrgAlias)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateOptionalStringFilter("org_alias", data.OrgAlias); err != nil {
		resp.Diagnostics.AddError("Invalid Organization List Filter", err.Error())
		return
	}
	filters := organizationListFilters(data.OrgAlias)
	endpoint := endpointWithQuery("/organization/list", filters)
	var rawResult json.RawMessage
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &rawResult); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list organizations: %s", safeListDiagnostic(err, filters)))
		return
	}
	items, err := decodeTopLevelList(rawResult, "/organization/list")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.ID = types.StringValue("organizations")
	data.Organizations = make([]OrganizationListItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, raw := range items {
		object, err := decodeListObject(raw, "/organization/list", "organization item")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		identity, err := dataSourceRequiredStringAt(object, "organization_id")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", "/organization/list returned an organization object without a canonical organization_id")
			return
		}
		organizationID := identity.ValueString()
		if err := dataSourceListIdentity(seen, organizationID, "/organization/list", "organization_id"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		table, err := parseBudgetTable(object)
		if err == nil {
			err = validateDataSourceBudgetTableNumbers(table)
		}
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		item := OrganizationListItem{OrganizationID: identity, Blocked: types.BoolNull()}
		item.OrganizationAlias, err = dataSourceNullableStringAt(object, "organization_alias")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		item.Blocked, err = dataSourceNullableBoolAt(object, "blocked")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		budgetID, presence, err := budgetTableID(object, table)
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if presence == apiValuePresent {
			item.BudgetID = types.StringValue(budgetID)
		} else {
			item.BudgetID = types.StringNull()
		}
		for _, field := range []struct {
			name   string
			target *types.Float64
		}{
			{"max_budget", &item.MaxBudget}, {"soft_budget", &item.SoftBudget},
		} {
			if err := updateBudgetFloat64(field.target, table, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		item.Spend, err = dataSourceNullableFloat64At(object, "spend")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		for _, field := range []struct {
			name   string
			target *types.Int64
		}{
			{"tpm_limit", &item.TPMLimit}, {"rpm_limit", &item.RPMLimit}, {"max_parallel_requests", &item.MaxParallelRequests},
		} {
			if err := updateBudgetInt64(field.target, table, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		if err := updateBudgetDuration(&item.BudgetDuration, table, true, true); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		item.ModelRPMLimit, err = dataSourceNullableInt64MapAt(object, "metadata", "model_rpm_limit")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		item.ModelTPMLimit, err = dataSourceNullableInt64MapAt(object, "metadata", "model_tpm_limit")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		data.Organizations = append(data.Organizations, item)
	}
	sort.SliceStable(data.Organizations, func(i, j int) bool {
		return data.Organizations[i].OrganizationID.ValueString() < data.Organizations[j].OrganizationID.ValueString()
	})
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func organizationListFilters(alias types.String) url.Values {
	filters := url.Values{}
	addKnownStringFilter(filters, "org_alias", alias)
	return filters
}
