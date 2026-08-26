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

func (item UserListItem) listItemIdentity() string {
	return item.UserID.ValueString()
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
				Description: "Stable historical identifier for this data source.",
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

	client, ok := configuredClient(req.ProviderData)
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

	filters := userListFilters(data.UserRole)
	users, err := listUsers(ctx, d.client, filters)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list users: %s", safeListDiagnostic(err, filters)))
		return
	}

	data.ID = types.StringValue("users")
	data.Users = users
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func userListFilters(userRole types.String) url.Values {
	filters := url.Values{}
	// The Terraform argument keeps its existing user_role name, while LiteLLM
	// v1.98's exact query parameter is role.
	addKnownStringFilter(filters, "role", userRole)
	return filters
}

type userListWirePage struct {
	Users      json.RawMessage `json:"users"`
	Total      *int            `json:"total"`
	Page       *int            `json:"page"`
	PageSize   *int            `json:"page_size"`
	TotalPages *int            `json:"total_pages"`
}

func listUsers(ctx context.Context, client *Client, filters url.Values) ([]UserListItem, error) {
	users, err := collectNumberedPages(ctx, "/user/list", func(ctx context.Context, page int) (numberedListPage[UserListItem], error) {
		query := cloneURLValues(filters)
		query.Set("page", fmt.Sprintf("%d", page))
		query.Set("page_size", "100") // LiteLLM v1.98 endpoint maximum.
		query.Set("sort_by", "user_id")
		query.Set("sort_order", "asc")

		var wire userListWirePage
		if err := client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/user/list", query), nil, &wire); err != nil {
			return numberedListPage[UserListItem]{}, err
		}
		if wire.Total == nil || wire.Page == nil || wire.PageSize == nil || wire.TotalPages == nil {
			return numberedListPage[UserListItem]{}, fmt.Errorf("/user/list response omitted required pagination metadata")
		}
		if *wire.PageSize != 100 {
			return numberedListPage[UserListItem]{}, fmt.Errorf("/user/list did not honor the requested page_size")
		}
		rawItems, err := decodeNamedList(wire.Users, "/user/list", "users")
		if err != nil {
			return numberedListPage[UserListItem]{}, err
		}
		items := make([]UserListItem, 0, len(rawItems))
		for _, rawItem := range rawItems {
			item, err := decodeUserListItem(rawItem)
			if err != nil {
				return numberedListPage[UserListItem]{}, err
			}
			items = append(items, item)
		}
		return numberedListPage[UserListItem]{
			Items:      items,
			Number:     *wire.Page,
			TotalPages: *wire.TotalPages,
			TotalCount: *wire.Total,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(users, func(i, j int) bool {
		if users[i].UserID.ValueString() != users[j].UserID.ValueString() {
			return users[i].UserID.ValueString() < users[j].UserID.ValueString()
		}
		return users[i].UserEmail.ValueString() < users[j].UserEmail.ValueString()
	})
	return users, nil
}

func decodeUserListItem(rawItem json.RawMessage) (UserListItem, error) {
	userMap, err := decodeListObject(rawItem, "/user/list", "user item")
	if err != nil {
		return UserListItem{}, err
	}
	userID, ok := userMap["user_id"].(string)
	if !ok || userID == "" {
		return UserListItem{}, fmt.Errorf("/user/list returned a user object without user_id")
	}
	item := UserListItem{UserID: types.StringValue(userID)}
	if alias, ok := userMap["user_alias"].(string); ok {
		item.UserAlias = types.StringValue(alias)
	}
	if email, ok := userMap["user_email"].(string); ok {
		item.UserEmail = types.StringValue(email)
	}
	if role, ok := userMap["user_role"].(string); ok {
		item.UserRole = types.StringValue(role)
	}
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &item.MaxBudget},
		{"spend", &item.Spend},
	} {
		if err := updateFloat64FromAPI(field.target, userMap, true, true, field.name); err != nil {
			return UserListItem{}, err
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &item.TPMLimit},
		{"rpm_limit", &item.RPMLimit},
	} {
		if err := updateInt64FromAPI(field.target, userMap, true, true, field.name); err != nil {
			return UserListItem{}, err
		}
	}
	return item, nil
}
