package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &OrganizationsListDataSource{}

func NewOrganizationsListDataSource() datasource.DataSource {
	return &OrganizationsListDataSource{}
}

type OrganizationsListDataSource struct {
	client *Client
}

type OrganizationListItem struct {
	OrganizationID    types.String  `tfsdk:"organization_id"`
	OrganizationAlias types.String  `tfsdk:"organization_alias"`
	MaxBudget         types.Float64 `tfsdk:"max_budget"`
	Spend             types.Float64 `tfsdk:"spend"`
	TPMLimit          types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit          types.Int64   `tfsdk:"rpm_limit"`
	Blocked           types.Bool    `tfsdk:"blocked"`
}

type OrganizationsListDataSourceModel struct {
	ID            types.String           `tfsdk:"id"`
	OrgAlias      types.String           `tfsdk:"org_alias"`
	Organizations []OrganizationListItem `tfsdk:"organizations"`
}

func (d *OrganizationsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organizations"
}

func (d *OrganizationsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of LiteLLM organizations.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Stable historical identifier for this data source.",
				Computed:    true,
			},
			"org_alias": schema.StringAttribute{
				Description: "Optional organization alias to filter by (partial match, case-insensitive).",
				Optional:    true,
			},
			"organizations": schema.ListNestedAttribute{
				Description: "List of organizations.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"organization_id": schema.StringAttribute{
							Description: "The unique identifier for this organization.",
							Computed:    true,
						},
						"organization_alias": schema.StringAttribute{
							Description: "The name/alias of the organization.",
							Computed:    true,
						},
						"max_budget": schema.Float64Attribute{
							Description: "Max budget for the organization.",
							Computed:    true,
						},
						"spend": schema.Float64Attribute{
							Description: "Amount spent by this organization.",
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
						"blocked": schema.BoolAttribute{
							Description: "Compatibility field. LiteLLM v1.98 does not return organization-level blocked state, so this is false.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *OrganizationsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OrganizationsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OrganizationsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	filters := organizationListFilters(data.OrgAlias)
	endpoint := endpointWithQuery("/organization/list", filters)

	var rawResult json.RawMessage
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &rawResult); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list organizations: %s", safeListDiagnostic(err, filters)))
		return
	}
	orgsData, err := decodeTopLevelList(rawResult, "/organization/list")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}

	data.ID = types.StringValue("organizations")
	data.Organizations = make([]OrganizationListItem, 0, len(orgsData))
	for _, rawOrganization := range orgsData {
		orgMap, err := decodeListObject(rawOrganization, "/organization/list", "organization item")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}

		item := OrganizationListItem{}

		if orgID, ok := orgMap["organization_id"].(string); ok {
			item.OrganizationID = types.StringValue(orgID)
		}
		if alias, ok := orgMap["organization_alias"].(string); ok {
			item.OrganizationAlias = types.StringValue(alias)
		}
		if err := updateFloat64FromAPI(&item.MaxBudget, orgMap, true, true, "litellm_budget_table", "max_budget"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if err := updateFloat64FromAPI(&item.Spend, orgMap, true, true, "spend"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if err := updateInt64FromAPI(&item.TPMLimit, orgMap, true, true, "litellm_budget_table", "tpm_limit"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if err := updateInt64FromAPI(&item.RPMLimit, orgMap, true, true, "litellm_budget_table", "rpm_limit"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if blocked, ok := orgMap["blocked"].(bool); ok {
			item.Blocked = types.BoolValue(blocked)
		} else {
			item.Blocked = types.BoolValue(false)
		}

		if item.OrganizationID.ValueString() == "" {
			resp.Diagnostics.AddError("Invalid API Response", "/organization/list returned an organization object without organization_id")
			return
		}
		data.Organizations = append(data.Organizations, item)
	}
	sort.SliceStable(data.Organizations, func(i, j int) bool {
		return data.Organizations[i].OrganizationID.ValueString() < data.Organizations[j].OrganizationID.ValueString()
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func organizationListFilters(orgAlias types.String) url.Values {
	filters := url.Values{}
	addKnownStringFilter(filters, "org_alias", orgAlias)
	return filters
}
