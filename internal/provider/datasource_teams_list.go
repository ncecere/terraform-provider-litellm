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
	ID             types.String   `tfsdk:"id"`
	OrganizationID types.String   `tfsdk:"organization_id"`
	Teams          []TeamListItem `tfsdk:"teams"`
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
	var data TeamsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	filters := teamListFilters(data.OrganizationID)
	endpoint := endpointWithQuery("/team/list", filters)

	var rawResult json.RawMessage
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &rawResult); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list teams: %s", safeListDiagnostic(err, filters)))
		return
	}
	teamsData, err := decodeTopLevelList(rawResult, "/team/list")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}

	data.ID = types.StringValue("teams")
	data.Teams = make([]TeamListItem, 0, len(teamsData))
	for _, rawTeam := range teamsData {
		teamMap, err := decodeListObject(rawTeam, "/team/list", "team item")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}

		item := TeamListItem{}

		if teamID, ok := teamMap["team_id"].(string); ok {
			item.TeamID = types.StringValue(teamID)
		}
		if teamAlias, ok := teamMap["team_alias"].(string); ok {
			item.TeamAlias = types.StringValue(teamAlias)
		}
		if orgID, ok := teamMap["organization_id"].(string); ok {
			item.OrganizationID = types.StringValue(orgID)
		}
		for _, field := range []struct {
			name   string
			target *types.Float64
		}{
			{"max_budget", &item.MaxBudget},
			{"spend", &item.Spend},
		} {
			if err := updateFloat64FromAPI(field.target, teamMap, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		for _, field := range []struct {
			name   string
			target *types.Int64
		}{
			{"tpm_limit", &item.TPMLimit},
			{"rpm_limit", &item.RPMLimit},
		} {
			if err := updateInt64FromAPI(field.target, teamMap, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		if blocked, ok := teamMap["blocked"].(bool); ok {
			item.Blocked = types.BoolValue(blocked)
		} else {
			item.Blocked = types.BoolValue(false)
		}

		if item.TeamID.ValueString() == "" {
			resp.Diagnostics.AddError("Invalid API Response", "/team/list returned a team object without team_id")
			return
		}
		data.Teams = append(data.Teams, item)
	}
	sort.SliceStable(data.Teams, func(i, j int) bool {
		return data.Teams[i].TeamID.ValueString() < data.Teams[j].TeamID.ValueString()
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func teamListFilters(organizationID types.String) url.Values {
	filters := url.Values{}
	addKnownStringFilter(filters, "organization_id", organizationID)
	return filters
}
