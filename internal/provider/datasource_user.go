package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &UserDataSource{}

func NewUserDataSource() datasource.DataSource {
	return &UserDataSource{}
}

type UserDataSource struct {
	client *Client
}

type UserDataSourceModel struct {
	ID             types.String  `tfsdk:"id"`
	UserID         types.String  `tfsdk:"user_id"`
	UserAlias      types.String  `tfsdk:"user_alias"`
	UserEmail      types.String  `tfsdk:"user_email"`
	UserRole       types.String  `tfsdk:"user_role"`
	Teams          types.List    `tfsdk:"teams"`
	Models         types.List    `tfsdk:"models"`
	MaxBudget      types.Float64 `tfsdk:"max_budget"`
	BudgetDuration types.String  `tfsdk:"budget_duration"`
	TPMLimit       types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit       types.Int64   `tfsdk:"rpm_limit"`
	Metadata       types.Map     `tfsdk:"metadata"`
	Spend          types.Float64 `tfsdk:"spend"`
}

func (d *UserDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *UserDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM user.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this user.",
				Computed:    true,
			},
			"user_id": schema.StringAttribute{
				Description: "The user ID to look up.",
				Required:    true,
			},
			"user_alias": schema.StringAttribute{
				Description: "A descriptive name for the user.",
				Computed:    true,
			},
			"user_email": schema.StringAttribute{
				Description: "The user's email address.",
				Computed:    true,
			},
			"user_role": schema.StringAttribute{
				Description: "The user's role.",
				Computed:    true,
			},
			"teams": schema.ListAttribute{
				Description: "List of team IDs the user belongs to.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"models": schema.ListAttribute{
				Description: "Model names the user is allowed to call.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Maximum budget for the user.",
				Computed:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset duration.",
				Computed:    true,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Tokens per minute limit for the user.",
				Computed:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Requests per minute limit for the user.",
				Computed:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the user.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"spend": schema.Float64Attribute{
				Description: "Amount spent by this user.",
				Computed:    true,
			},
		},
	}
}

func (d *UserDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config UserDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	userID := config.UserID.ValueString()
	if config.UserID.IsNull() || config.UserID.IsUnknown() || userID == "" {
		resp.Diagnostics.AddError("Invalid User Lookup", "user_id must be known and nonempty")
		return
	}
	endpoint := endpointWithQuery("/user/info", url.Values{"user_id": []string{userID}})

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read user '%s': %s", userID, err))
		return
	}

	rootUserID, err := dataSourceRequiredStringAt(result, "user_id")
	if err != nil || rootUserID.ValueString() != userID {
		resp.Diagnostics.AddError("Invalid API Response", "User response root identity did not match the requested user.")
		return
	}
	userInfo, err := dataSourceRequiredObjectAt(result, "user_info")
	if err != nil || len(userInfo) == 0 {
		resp.Diagnostics.AddError("Invalid API Response", "User response omitted the required user_info object.")
		return
	}
	actualUserID, err := dataSourceRequiredStringAt(userInfo, "user_id")
	if err != nil || actualUserID.ValueString() != userID || !actualUserID.Equal(rootUserID) {
		resp.Diagnostics.AddError("Invalid API Response", "User response nested identity did not match the requested user.")
		return
	}
	data := UserDataSourceModel{ID: actualUserID, UserID: config.UserID}
	if data.UserAlias, err = dataSourceRoleRedactedNullableStringAt(userInfo, "user_alias"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.UserEmail, err = dataSourceRoleRedactedNullableStringAt(userInfo, "user_email"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.UserRole, err = dataSourceRoleRedactedNullableStringAt(userInfo, "user_role"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.Teams, err = dataSourceRoleRedactedNullableStringListAt(userInfo, "teams"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.Models, err = dataSourceRoleRedactedNullableStringListAt(userInfo, "models"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.MaxBudget, err = dataSourceRoleRedactedNullableFloat64At(userInfo, "max_budget"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.BudgetDuration, err = dataSourceRoleRedactedNullableStringAt(userInfo, "budget_duration"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.TPMLimit, err = dataSourceRoleRedactedNullableInt64At(userInfo, "tpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.RPMLimit, err = dataSourceRoleRedactedNullableInt64At(userInfo, "rpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.Metadata, err = dataSourceRoleRedactedNullableStringMapAt(userInfo, "metadata"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.Spend, err = dataSourceRoleRedactedNullableFloat64At(userInfo, "spend"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
