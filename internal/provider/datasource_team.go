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

var _ datasource.DataSource = &TeamDataSource{}

func NewTeamDataSource() datasource.DataSource {
	return &TeamDataSource{}
}

type TeamDataSource struct {
	client *Client
}

type TeamDataSourceModel struct {
	ID                    types.String  `tfsdk:"id"`
	TeamID                types.String  `tfsdk:"team_id"`
	TeamAlias             types.String  `tfsdk:"team_alias"`
	OrganizationID        types.String  `tfsdk:"organization_id"`
	AccessGroupIDs        types.Set     `tfsdk:"access_group_ids"`
	Models                types.List    `tfsdk:"models"`
	MaxBudget             types.Float64 `tfsdk:"max_budget"`
	Spend                 types.Float64 `tfsdk:"spend"`
	TPMLimit              types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit              types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration        types.String  `tfsdk:"budget_duration"`
	Metadata              types.Map     `tfsdk:"metadata"`
	TeamMemberPermissions types.List    `tfsdk:"team_member_permissions"`
	Blocked               types.Bool    `tfsdk:"blocked"`
}

func (d *TeamDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (d *TeamDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this team.",
				Computed:    true,
			},
			"team_id": schema.StringAttribute{
				Description: "The team ID to look up.",
				Required:    true,
			},
			"team_alias": schema.StringAttribute{
				Description: "User-defined team alias.",
				Computed:    true,
			},
			"organization_id": schema.StringAttribute{
				Description: "Organization ID for the team.",
				Computed:    true,
			},
			"access_group_ids": schema.SetAttribute{
				Description: "Access group IDs associated with this team.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"models": schema.ListAttribute{
				Description: "List of models the team can access.",
				Computed:    true,
				ElementType: types.StringType,
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
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset duration.",
				Computed:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Arbitrary metadata for the team.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"team_member_permissions": schema.ListAttribute{
				Description: "List of permissions granted to team members.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the team is blocked.",
				Computed:    true,
			},
		},
	}
}

func (d *TeamDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TeamDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TeamDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	teamID := data.TeamID.ValueString()
	endpoint := fmt.Sprintf("/team/info?team_id=%s", url.QueryEscape(teamID))

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read team '%s': %s", teamID, err))
		return
	}

	// The /team/info endpoint may return team data nested inside "team_info"
	teamInfo := result
	if nested, ok := result["team_info"].(map[string]interface{}); ok {
		teamInfo = nested
	}

	// Set ID
	data.ID = data.TeamID

	// Update fields from response
	if teamAlias, ok := teamInfo["team_alias"].(string); ok {
		data.TeamAlias = types.StringValue(teamAlias)
	}
	if orgID, ok := teamInfo["organization_id"].(string); ok {
		data.OrganizationID = types.StringValue(orgID)
	}
	if budgetDuration, ok := teamInfo["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}

	// Numeric fields
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget},
		{"spend", &data.Spend},
	} {
		if err := updateFloat64FromAPI(field.target, teamInfo, true, true, field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit},
		{"rpm_limit", &data.RPMLimit},
	} {
		if err := updateInt64FromAPI(field.target, teamInfo, true, true, field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}

	// Boolean fields
	if blocked, ok := teamInfo["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	} else {
		data.Blocked = types.BoolValue(false)
	}

	// Handle models list
	if models, ok := teamInfo["models"].([]interface{}); ok {
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

	accessGroupIDs, err := stringSetFromAPI(ctx, teamInfo["access_group_ids"])
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Response", fmt.Sprintf("Unable to decode access_group_ids for team '%s': %s", teamID, err))
		return
	}
	data.AccessGroupIDs = accessGroupIDs

	// Handle metadata map
	if metadata, ok := teamInfo["metadata"].(map[string]interface{}); ok {
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

	// Fetch permissions separately
	permEndpoint := fmt.Sprintf("/team/permissions_list?team_id=%s", url.QueryEscape(teamID))
	var permResult map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", permEndpoint, nil, &permResult); err == nil {
		if perms, ok := permResult["team_member_permissions"].([]interface{}); ok {
			permsList := make([]attr.Value, len(perms))
			for i, p := range perms {
				if str, ok := p.(string); ok {
					permsList[i] = types.StringValue(str)
				}
			}
			data.TeamMemberPermissions, _ = types.ListValue(types.StringType, permsList)
		} else {
			data.TeamMemberPermissions, _ = types.ListValue(types.StringType, []attr.Value{})
		}
	} else {
		data.TeamMemberPermissions, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
