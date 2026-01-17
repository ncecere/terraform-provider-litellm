package provider

import (
	"context"
	"fmt"

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
				Description: "Placeholder identifier.",
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

	endpoint := "/team/list"
	if !data.OrganizationID.IsNull() && data.OrganizationID.ValueString() != "" {
		endpoint = fmt.Sprintf("/team/list?organization_id=%s", data.OrganizationID.ValueString())
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list teams: %s", err))
		return
	}

	// Set placeholder ID
	data.ID = types.StringValue("teams")

	// Parse the response
	var teamsData []interface{}
	if teams, ok := result["teams"].([]interface{}); ok {
		teamsData = teams
	} else if dataArr, ok := result["data"].([]interface{}); ok {
		teamsData = dataArr
	}

	data.Teams = make([]TeamListItem, 0, len(teamsData))
	for _, t := range teamsData {
		teamMap, ok := t.(map[string]interface{})
		if !ok {
			continue
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
		if maxBudget, ok := teamMap["max_budget"].(float64); ok {
			item.MaxBudget = types.Float64Value(maxBudget)
		}
		if spend, ok := teamMap["spend"].(float64); ok {
			item.Spend = types.Float64Value(spend)
		}
		if tpmLimit, ok := teamMap["tpm_limit"].(float64); ok {
			item.TPMLimit = types.Int64Value(int64(tpmLimit))
		}
		if rpmLimit, ok := teamMap["rpm_limit"].(float64); ok {
			item.RPMLimit = types.Int64Value(int64(rpmLimit))
		}
		if blocked, ok := teamMap["blocked"].(bool); ok {
			item.Blocked = types.BoolValue(blocked)
		} else {
			item.Blocked = types.BoolValue(false)
		}

		data.Teams = append(data.Teams, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
