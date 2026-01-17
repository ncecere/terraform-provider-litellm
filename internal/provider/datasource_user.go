package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	var data UserDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userID := data.UserID.ValueString()
	endpoint := fmt.Sprintf("/user/info?user_id=%s", userID)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read user '%s': %s", userID, err))
		return
	}

	// Set ID
	data.ID = data.UserID

	// The /user/info endpoint may return user_info nested
	userInfo := result
	if ui, ok := result["user_info"].(map[string]interface{}); ok {
		userInfo = ui
	}

	// Update fields from response
	if alias, ok := userInfo["user_alias"].(string); ok {
		data.UserAlias = types.StringValue(alias)
	}
	if email, ok := userInfo["user_email"].(string); ok {
		data.UserEmail = types.StringValue(email)
	}
	if role, ok := userInfo["user_role"].(string); ok {
		data.UserRole = types.StringValue(role)
	}
	if budgetDuration, ok := userInfo["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}

	// Numeric fields
	if maxBudget, ok := userInfo["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if spend, ok := userInfo["spend"].(float64); ok {
		data.Spend = types.Float64Value(spend)
	}
	if tpmLimit, ok := userInfo["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	}
	if rpmLimit, ok := userInfo["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	}

	// Handle teams list
	if teams, ok := userInfo["teams"].([]interface{}); ok {
		teamsList := make([]attr.Value, len(teams))
		for i, t := range teams {
			if str, ok := t.(string); ok {
				teamsList[i] = types.StringValue(str)
			}
		}
		data.Teams, _ = types.ListValue(types.StringType, teamsList)
	} else {
		data.Teams, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle models list
	if models, ok := userInfo["models"].([]interface{}); ok {
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

	// Handle metadata map
	if metadata, ok := userInfo["metadata"].(map[string]interface{}); ok {
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

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
