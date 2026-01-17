package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &KeysListDataSource{}

func NewKeysListDataSource() datasource.DataSource {
	return &KeysListDataSource{}
}

type KeysListDataSource struct {
	client *Client
}

type KeyListItem struct {
	KeyName   types.String  `tfsdk:"key_name"`
	KeyAlias  types.String  `tfsdk:"key_alias"`
	UserID    types.String  `tfsdk:"user_id"`
	TeamID    types.String  `tfsdk:"team_id"`
	MaxBudget types.Float64 `tfsdk:"max_budget"`
	Spend     types.Float64 `tfsdk:"spend"`
	TPMLimit  types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit  types.Int64   `tfsdk:"rpm_limit"`
	Blocked   types.Bool    `tfsdk:"blocked"`
}

type KeysListDataSourceModel struct {
	ID     types.String  `tfsdk:"id"`
	TeamID types.String  `tfsdk:"team_id"`
	UserID types.String  `tfsdk:"user_id"`
	Keys   []KeyListItem `tfsdk:"keys"`
}

func (d *KeysListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_keys"
}

func (d *KeysListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of LiteLLM API keys.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"team_id": schema.StringAttribute{
				Description: "Optional team ID to filter keys by team.",
				Optional:    true,
			},
			"user_id": schema.StringAttribute{
				Description: "Optional user ID to filter keys by user.",
				Optional:    true,
			},
			"keys": schema.ListNestedAttribute{
				Description: "List of keys.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key_name": schema.StringAttribute{
							Description: "The hashed key name (not the actual key value).",
							Computed:    true,
						},
						"key_alias": schema.StringAttribute{
							Description: "User-friendly alias for the key.",
							Computed:    true,
						},
						"user_id": schema.StringAttribute{
							Description: "User ID associated with this key.",
							Computed:    true,
						},
						"team_id": schema.StringAttribute{
							Description: "Team ID associated with this key.",
							Computed:    true,
						},
						"max_budget": schema.Float64Attribute{
							Description: "Maximum budget for this key.",
							Computed:    true,
						},
						"spend": schema.Float64Attribute{
							Description: "Amount spent by this key.",
							Computed:    true,
						},
						"tpm_limit": schema.Int64Attribute{
							Description: "Tokens per minute limit.",
							Computed:    true,
						},
						"rpm_limit": schema.Int64Attribute{
							Description: "Requests per minute limit.",
							Computed:    true,
						},
						"blocked": schema.BoolAttribute{
							Description: "Whether the key is blocked.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *KeysListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *KeysListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data KeysListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build endpoint with optional filters
	endpoint := "/key/list"
	params := []string{}

	if !data.TeamID.IsNull() && data.TeamID.ValueString() != "" {
		params = append(params, fmt.Sprintf("team_id=%s", data.TeamID.ValueString()))
	}
	if !data.UserID.IsNull() && data.UserID.ValueString() != "" {
		params = append(params, fmt.Sprintf("user_id=%s", data.UserID.ValueString()))
	}

	if len(params) > 0 {
		endpoint += "?"
		for i, p := range params {
			if i > 0 {
				endpoint += "&"
			}
			endpoint += p
		}
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list keys: %s", err))
		return
	}

	// Set placeholder ID
	data.ID = types.StringValue("keys")

	// Parse the response
	var keysData []interface{}
	if keys, ok := result["keys"].([]interface{}); ok {
		keysData = keys
	} else if dataArr, ok := result["data"].([]interface{}); ok {
		keysData = dataArr
	}

	data.Keys = make([]KeyListItem, 0, len(keysData))
	for _, k := range keysData {
		keyMap, ok := k.(map[string]interface{})
		if !ok {
			continue
		}

		item := KeyListItem{}

		if keyName, ok := keyMap["key_name"].(string); ok {
			item.KeyName = types.StringValue(keyName)
		} else if token, ok := keyMap["token"].(string); ok {
			item.KeyName = types.StringValue(token)
		}

		if keyAlias, ok := keyMap["key_alias"].(string); ok {
			item.KeyAlias = types.StringValue(keyAlias)
		}
		if userID, ok := keyMap["user_id"].(string); ok {
			item.UserID = types.StringValue(userID)
		}
		if teamID, ok := keyMap["team_id"].(string); ok {
			item.TeamID = types.StringValue(teamID)
		}
		if maxBudget, ok := keyMap["max_budget"].(float64); ok {
			item.MaxBudget = types.Float64Value(maxBudget)
		}
		if spend, ok := keyMap["spend"].(float64); ok {
			item.Spend = types.Float64Value(spend)
		}
		if tpmLimit, ok := keyMap["tpm_limit"].(float64); ok {
			item.TPMLimit = types.Int64Value(int64(tpmLimit))
		}
		if rpmLimit, ok := keyMap["rpm_limit"].(float64); ok {
			item.RPMLimit = types.Int64Value(int64(rpmLimit))
		}
		if blocked, ok := keyMap["blocked"].(bool); ok {
			item.Blocked = types.BoolValue(blocked)
		} else {
			item.Blocked = types.BoolValue(false)
		}

		data.Keys = append(data.Keys, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
