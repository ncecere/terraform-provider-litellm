package provider

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &TeamsListDataSource{}

func NewTeamsListDataSource() datasource.DataSource {
	return &TeamsListDataSource{}
}

type TeamsListDataSource struct {
	client *Client
}

type TeamListItem struct {
	TeamID         types.String  `tfsdk:"team_id"`
	TeamAlias      types.String  `tfsdk:"team_alias"`
	OrganizationID types.String  `tfsdk:"organization_id"`
	MaxBudget      types.Float64 `tfsdk:"max_budget"`
	Spend          types.Float64 `tfsdk:"spend"`
	TPMLimit       types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit       types.Int64   `tfsdk:"rpm_limit"`
	Blocked        types.Bool    `tfsdk:"blocked"`
}

type TeamsListDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Teams          types.List   `tfsdk:"teams"`
}

func (d *TeamsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_teams"
}

func (d *TeamsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of LiteLLM teams.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Stable historical identifier for this data source.",
				Computed:    true,
			},
			"organization_id": schema.StringAttribute{
				Description: "Optional organization ID to filter teams.",
				Optional:    true,
			},
			"teams": schema.ListNestedAttribute{
				Description: "List of teams.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"team_id": schema.StringAttribute{
							Description: "The unique identifier for this team.",
							Computed:    true,
						},
						"team_alias": schema.StringAttribute{
							Description: "User-defined team alias.",
							Computed:    true,
						},
						"organization_id": schema.StringAttribute{
							Description: "Organization ID for the team.",
							Computed:    true,
						},
						"max_budget": schema.Float64Attribute{
							Description: "Maximum budget for the team.",
							Computed:    true,
						},
						"spend": schema.Float64Attribute{
							Description: "Amount spent by this team.",
							Computed:    true,
						},
						"tpm_limit": schema.Int64Attribute{
							Description: "Tokens per minute limit for the team.",
							Computed:    true,
						},
						"rpm_limit": schema.Int64Attribute{
							Description: "Requests per minute limit for the team.",
							Computed:    true,
						},
						"blocked": schema.BoolAttribute{
							Description: "Whether the team is blocked.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *TeamsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TeamsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config TeamsListDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.OrganizationID.IsUnknown() {
		resp.Diagnostics.AddError("Invalid Team List Filter", "organization_id must be known or null")
		return
	}

	filters := teamListFilters(config.OrganizationID)
	// Preserve the v1 organization_id query contract.
	results, err := fetchTopLevelListObjects(ctx, d.client, endpointWithQuery("/team/list", filters), "team item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list teams: %s", safeListDiagnostic(err, filters)))
		return
	}
	teams, err := projectTeamsListDataSource(results)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}

	// Keep the established public ID and configured filter while publishing the
	// complete validated inventory exactly once.
	next := struct {
		ID             types.String   `tfsdk:"id"`
		OrganizationID types.String   `tfsdk:"organization_id"`
		Teams          []TeamListItem `tfsdk:"teams"`
	}{
		ID:             types.StringValue("teams"),
		OrganizationID: config.OrganizationID,
		Teams:          teams,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func projectTeamsListDataSource(results []map[string]interface{}) ([]TeamListItem, error) {
	teams := make([]TeamListItem, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		item := TeamListItem{
			TeamAlias:      types.StringNull(),
			OrganizationID: types.StringNull(),
			MaxBudget:      types.Float64Null(),
			Spend:          types.Float64Null(),
			TPMLimit:       types.Int64Null(),
			RPMLimit:       types.Int64Null(),
			Blocked:        types.BoolNull(),
		}

		var err error
		item.TeamID, err = dataSourceRequiredStringAt(result, "team_id")
		if err != nil {
			return nil, fmt.Errorf("/team/list returned a team object without a canonical team_id")
		}
		if err := dataSourceListIdentity(seen, item.TeamID.ValueString(), "/team/list", "team_id"); err != nil {
			return nil, err
		}
		for _, field := range []struct {
			name   string
			target *types.String
		}{
			{"team_alias", &item.TeamAlias},
			{"organization_id", &item.OrganizationID},
		} {
			value, fieldErr := dataSourceNullableStringAt(result, field.name)
			if fieldErr != nil {
				return nil, fieldErr
			}
			*field.target = value
		}
		for _, field := range []struct {
			name   string
			target *types.Float64
		}{
			{"max_budget", &item.MaxBudget},
			{"spend", &item.Spend},
		} {
			value, fieldErr := dataSourceNullableFloat64At(result, field.name)
			if fieldErr != nil {
				return nil, fieldErr
			}
			*field.target = value
		}
		for _, field := range []struct {
			name   string
			target *types.Int64
		}{
			{"tpm_limit", &item.TPMLimit},
			{"rpm_limit", &item.RPMLimit},
		} {
			value, fieldErr := dataSourceNullableInt64At(result, field.name)
			if fieldErr != nil {
				return nil, fieldErr
			}
			*field.target = value
		}
		item.Blocked, err = dataSourceNullableBoolAt(result, "blocked")
		if err != nil {
			return nil, err
		}
		teams = append(teams, item)
	}

	sort.SliceStable(teams, func(i, j int) bool {
		return teams[i].TeamID.ValueString() < teams[j].TeamID.ValueString()
	})
	return teams, nil
}

func teamListFilters(organizationID types.String) url.Values {
	filters := url.Values{}
	addKnownStringFilter(filters, "organization_id", organizationID)
	return filters
}
