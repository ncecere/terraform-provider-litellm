package provider

import (
	"context"
	"fmt"
	"net/url"

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
	var config TeamDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	teamID := config.TeamID.ValueString()
	if config.TeamID.IsNull() || config.TeamID.IsUnknown() || teamID == "" {
		resp.Diagnostics.AddError("Invalid Team Lookup", "team_id must be a known nonempty string")
		return
	}
	var result map[string]interface{}
	query := url.Values{"team_id": []string{teamID}}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/team/info", query), nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", "Unable to read team information: "+safeListDiagnostic(err, query))
		return
	}

	// Projection is deliberately off-state. The permissions request is part of
	// the same logical observation, so neither GET may publish independently.
	next, err := projectTeamDataSourceInfo(result, teamID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Response", err.Error())
		return
	}

	permissionEndpoint := endpointWithQuery("/team/permissions_list", query)
	var permissionResult map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", permissionEndpoint, nil, &permissionResult); err != nil {
		resp.Diagnostics.AddError("Client Error", "Unable to read team permissions: "+safeListDiagnostic(err, query))
		return
	}
	permissions, err := projectTeamDataSourcePermissions(permissionResult, teamID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Permissions Response", err.Error())
		return
	}
	next.TeamMemberPermissions = permissions

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func projectTeamDataSourceInfo(result map[string]interface{}, expectedTeamID string) (TeamDataSourceModel, error) {
	next := TeamDataSourceModel{
		ID:                    types.StringValue(expectedTeamID),
		TeamID:                types.StringValue(expectedTeamID),
		TeamAlias:             types.StringNull(),
		OrganizationID:        types.StringNull(),
		AccessGroupIDs:        types.SetNull(types.StringType),
		Models:                types.ListNull(types.StringType),
		MaxBudget:             types.Float64Null(),
		Spend:                 types.Float64Null(),
		TPMLimit:              types.Int64Null(),
		RPMLimit:              types.Int64Null(),
		BudgetDuration:        types.StringNull(),
		Metadata:              types.MapNull(types.StringType),
		TeamMemberPermissions: types.ListNull(types.StringType),
		Blocked:               types.BoolNull(),
	}
	if result == nil || len(result) == 0 {
		return next, fmt.Errorf("invalid /team/info response: expected the authoritative v1.98 object envelope")
	}
	for field := range result {
		switch field {
		case "team_id", "team_info", "keys", "team_memberships":
		default:
			return next, fmt.Errorf("invalid /team/info response: envelope contains a field outside the authoritative v1.98 relation")
		}
	}

	rootID, err := dataSourceRequiredStringAt(result, "team_id")
	if err != nil || rootID.ValueString() != expectedTeamID {
		return next, fmt.Errorf("invalid /team/info response: root identity does not match the requested team")
	}
	teamInfo, err := dataSourceRequiredObjectAt(result, "team_info")
	if err != nil || len(teamInfo) == 0 {
		return next, fmt.Errorf("invalid /team/info response: team_info must be a nonempty object")
	}
	nestedID, err := dataSourceRequiredStringAt(teamInfo, "team_id")
	if err != nil || nestedID.ValueString() != expectedTeamID {
		return next, fmt.Errorf("invalid /team/info response: nested identity does not match the requested team")
	}
	for _, relation := range []string{"keys", "team_memberships"} {
		if err := validateTeamDataSourceObjectRelation(result, relation); err != nil {
			return next, err
		}
	}

	for _, field := range []struct {
		name   string
		target *types.String
	}{
		{"team_alias", &next.TeamAlias},
		{"organization_id", &next.OrganizationID},
		{"budget_duration", &next.BudgetDuration},
	} {
		value, fieldErr := dataSourceNullableStringAt(teamInfo, field.name)
		if fieldErr != nil {
			return next, fieldErr
		}
		*field.target = value
	}
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &next.MaxBudget},
		{"spend", &next.Spend},
	} {
		value, fieldErr := dataSourceNullableFloat64At(teamInfo, field.name)
		if fieldErr != nil {
			return next, fieldErr
		}
		*field.target = value
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &next.TPMLimit},
		{"rpm_limit", &next.RPMLimit},
	} {
		value, fieldErr := dataSourceNullableInt64At(teamInfo, field.name)
		if fieldErr != nil {
			return next, fieldErr
		}
		*field.target = value
	}

	next.Blocked, err = dataSourceNullableBoolAt(teamInfo, "blocked")
	if err != nil {
		return next, err
	}
	next.Models, err = dataSourceNullableStringListAt(teamInfo, "models")
	if err != nil {
		return next, err
	}
	next.AccessGroupIDs, err = dataSourceNullableStringSetAt(teamInfo, "access_group_ids")
	if err != nil {
		return next, err
	}
	next.Metadata, err = dataSourceNullableStringMapAt(teamInfo, "metadata")
	if err != nil {
		return next, err
	}
	return next, nil
}

func validateTeamDataSourceObjectRelation(result map[string]interface{}, field string) error {
	raw, err := dataSourceRequiredValueAt(result, "a list of objects", field)
	if err != nil {
		return fmt.Errorf("invalid /team/info response: required relation is missing or malformed")
	}
	rows, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("invalid /team/info response: required relation must be a list of objects")
	}
	for _, row := range rows {
		object, ok := row.(map[string]interface{})
		if !ok || object == nil {
			return fmt.Errorf("invalid /team/info response: required relation contains a non-object row")
		}
	}
	return nil
}

func projectTeamDataSourcePermissions(result map[string]interface{}, expectedTeamID string) (types.List, error) {
	null := types.ListNull(types.StringType)
	if result == nil || len(result) == 0 {
		return null, fmt.Errorf("invalid /team/permissions_list response: expected the authoritative v1.98 object envelope")
	}
	for field := range result {
		switch field {
		case "team_id", "all_available_permissions", "team_member_permissions":
		default:
			return null, fmt.Errorf("invalid /team/permissions_list response: envelope contains a field outside the authoritative v1.98 relation")
		}
	}
	teamID, err := dataSourceRequiredStringAt(result, "team_id")
	if err != nil || teamID.ValueString() != expectedTeamID {
		return null, fmt.Errorf("invalid /team/permissions_list response: identity does not match the requested team")
	}
	if _, err := dataSourceRequiredStringListAt(result, "all_available_permissions"); err != nil {
		return null, fmt.Errorf("invalid /team/permissions_list response: required permission relation is missing or malformed")
	}
	if _, exists := result["team_member_permissions"]; !exists {
		return null, fmt.Errorf("invalid /team/permissions_list response: required team permission relation is missing")
	}
	permissions, err := dataSourceNullableStringListAt(result, "team_member_permissions")
	if err != nil {
		return null, fmt.Errorf("invalid /team/permissions_list response: team permission relation is malformed")
	}
	return permissions, nil
}
