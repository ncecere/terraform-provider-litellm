package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &UsersListDataSource{}

func NewUsersListDataSource() datasource.DataSource {
	return &UsersListDataSource{}
}

type UsersListDataSource struct {
	client *Client
}

type UserListItem struct {
	UserID    types.String  `tfsdk:"user_id"`
	UserAlias types.String  `tfsdk:"user_alias"`
	UserEmail types.String  `tfsdk:"user_email"`
	UserRole  types.String  `tfsdk:"user_role"`
	MaxBudget types.Float64 `tfsdk:"max_budget"`
	Spend     types.Float64 `tfsdk:"spend"`
	TPMLimit  types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit  types.Int64   `tfsdk:"rpm_limit"`
}

type UsersListDataSourceModel struct {
	ID       types.String   `tfsdk:"id"`
	UserRole types.String   `tfsdk:"user_role"`
	Users    []UserListItem `tfsdk:"users"`
}

func (d *UsersListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *UsersListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of LiteLLM users.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"user_role": schema.StringAttribute{
				Description: "Optional user role to filter by.",
				Optional:    true,
			},
			"users": schema.ListNestedAttribute{
				Description: "List of users.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"user_id": schema.StringAttribute{
							Description: "The unique identifier for this user.",
							Computed:    true,
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
						"max_budget": schema.Float64Attribute{
							Description: "Maximum budget for the user.",
							Computed:    true,
						},
						"spend": schema.Float64Attribute{
							Description: "Amount spent by this user.",
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
					},
				},
			},
		},
	}
}

func (d *UsersListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UsersListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data UsersListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := "/user/list"
	if !data.UserRole.IsNull() && data.UserRole.ValueString() != "" {
		endpoint = fmt.Sprintf("/user/list?user_role=%s", data.UserRole.ValueString())
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list users: %s", err))
		return
	}

	// Set placeholder ID
	data.ID = types.StringValue("users")

	// Parse the response
	var usersData []interface{}
	if users, ok := result["users"].([]interface{}); ok {
		usersData = users
	} else if dataArr, ok := result["data"].([]interface{}); ok {
		usersData = dataArr
	}

	data.Users = make([]UserListItem, 0, len(usersData))
	for _, u := range usersData {
		userMap, ok := u.(map[string]interface{})
		if !ok {
			continue
		}

		item := UserListItem{}

		if userID, ok := userMap["user_id"].(string); ok {
			item.UserID = types.StringValue(userID)
		}
		if alias, ok := userMap["user_alias"].(string); ok {
			item.UserAlias = types.StringValue(alias)
		}
		if email, ok := userMap["user_email"].(string); ok {
			item.UserEmail = types.StringValue(email)
		}
		if role, ok := userMap["user_role"].(string); ok {
			item.UserRole = types.StringValue(role)
		}
		if maxBudget, ok := userMap["max_budget"].(float64); ok {
			item.MaxBudget = types.Float64Value(maxBudget)
		}
		if spend, ok := userMap["spend"].(float64); ok {
			item.Spend = types.Float64Value(spend)
		}
		if tpmLimit, ok := userMap["tpm_limit"].(float64); ok {
			item.TPMLimit = types.Int64Value(int64(tpmLimit))
		}
		if rpmLimit, ok := userMap["rpm_limit"].(float64); ok {
			item.RPMLimit = types.Int64Value(int64(rpmLimit))
		}

		data.Users = append(data.Users, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
