package provider

import (
	"context"
	"fmt"

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
				Description: "Placeholder identifier.",
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
							Description: "Flag indicating if the org is blocked.",
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

	endpoint := "/organization/list"
	if !data.OrgAlias.IsNull() && data.OrgAlias.ValueString() != "" {
		endpoint = fmt.Sprintf("/organization/list?org_alias=%s", data.OrgAlias.ValueString())
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list organizations: %s", err))
		return
	}

	// Set placeholder ID
	data.ID = types.StringValue("organizations")

	// Parse the response
	var orgsData []interface{}
	if orgs, ok := result["organizations"].([]interface{}); ok {
		orgsData = orgs
	} else if dataArr, ok := result["data"].([]interface{}); ok {
		orgsData = dataArr
	}

	data.Organizations = make([]OrganizationListItem, 0, len(orgsData))
	for _, o := range orgsData {
		orgMap, ok := o.(map[string]interface{})
		if !ok {
			continue
		}

		item := OrganizationListItem{}

		if orgID, ok := orgMap["organization_id"].(string); ok {
			item.OrganizationID = types.StringValue(orgID)
		}
		if alias, ok := orgMap["organization_alias"].(string); ok {
			item.OrganizationAlias = types.StringValue(alias)
		}
		if maxBudget, ok := orgMap["max_budget"].(float64); ok {
			item.MaxBudget = types.Float64Value(maxBudget)
		}
		if spend, ok := orgMap["spend"].(float64); ok {
			item.Spend = types.Float64Value(spend)
		}
		if tpmLimit, ok := orgMap["tpm_limit"].(float64); ok {
			item.TPMLimit = types.Int64Value(int64(tpmLimit))
		}
		if rpmLimit, ok := orgMap["rpm_limit"].(float64); ok {
			item.RPMLimit = types.Int64Value(int64(rpmLimit))
		}
		if blocked, ok := orgMap["blocked"].(bool); ok {
			item.Blocked = types.BoolValue(blocked)
		} else {
			item.Blocked = types.BoolValue(false)
		}

		data.Organizations = append(data.Organizations, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
