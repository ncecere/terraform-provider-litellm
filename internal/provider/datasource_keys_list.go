package provider

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const sha256ManagementHashLength = 64

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

func (item KeyListItem) listItemIdentity() string {
	return item.KeyName.ValueString()
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
				Description: "Stable historical identifier for this data source.",
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
							Description: "Canonical lowercase SHA256 management hash. Full objects use only token; string entries are already management hashes.",
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

	filters := keyListFilters(data.TeamID, data.UserID)

	keys, err := listKeys(ctx, d.client, filters)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list keys: %s", safeListDiagnostic(err, filters)))
		return
	}

	data.ID = types.StringValue("keys")
	data.Keys = keys
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func keyListFilters(teamID, userID types.String) url.Values {
	filters := url.Values{}
	addKnownStringFilter(filters, "team_id", teamID)
	addKnownStringFilter(filters, "user_id", userID)
	return filters
}

type keyListWirePage struct {
	Keys        json.RawMessage `json:"keys"`
	TotalCount  *int            `json:"total_count"`
	CurrentPage *int            `json:"current_page"`
	TotalPages  *int            `json:"total_pages"`
}

func listKeys(ctx context.Context, client *Client, filters url.Values) ([]KeyListItem, error) {
	keys, err := collectNumberedPages(ctx, "/key/list", func(ctx context.Context, page int) (numberedListPage[KeyListItem], error) {
		query := cloneURLValues(filters)
		query.Set("page", fmt.Sprintf("%d", page))
		query.Set("size", "100") // LiteLLM v1.98 endpoint maximum.
		query.Set("return_full_object", "true")
		query.Set("sort_by", "token")
		query.Set("sort_order", "asc")

		var wire keyListWirePage
		if err := client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/key/list", query), nil, &wire); err != nil {
			return numberedListPage[KeyListItem]{}, err
		}
		if wire.TotalCount == nil || wire.CurrentPage == nil || wire.TotalPages == nil {
			return numberedListPage[KeyListItem]{}, fmt.Errorf("/key/list response omitted required pagination metadata")
		}
		rawItems, err := decodeNamedList(wire.Keys, "/key/list", "keys")
		if err != nil {
			return numberedListPage[KeyListItem]{}, err
		}
		items := make([]KeyListItem, 0, len(rawItems))
		for _, rawItem := range rawItems {
			item, err := decodeKeyListItem(rawItem)
			if err != nil {
				return numberedListPage[KeyListItem]{}, err
			}
			items = append(items, item)
		}
		return numberedListPage[KeyListItem]{
			Items:      items,
			Number:     *wire.CurrentPage,
			TotalPages: *wire.TotalPages,
			TotalCount: *wire.TotalCount,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(keys, func(i, j int) bool {
		left := []string{keys[i].KeyName.ValueString(), keys[i].KeyAlias.ValueString(), keys[i].UserID.ValueString(), keys[i].TeamID.ValueString()}
		right := []string{keys[j].KeyName.ValueString(), keys[j].KeyAlias.ValueString(), keys[j].UserID.ValueString(), keys[j].TeamID.ValueString()}
		for index := range left {
			if left[index] != right[index] {
				return left[index] < right[index]
			}
		}
		return false
	})
	return keys, nil
}

func decodeKeyListItem(raw json.RawMessage) (KeyListItem, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return KeyListItem{}, fmt.Errorf("/key/list returned an invalid key item")
	}
	if trimmed[0] == '"' {
		var tokenHash string
		if err := json.Unmarshal(trimmed, &tokenHash); err != nil {
			return KeyListItem{}, fmt.Errorf("/key/list returned a malformed key string")
		}
		canonicalHash, ok := canonicalSHA256ManagementHash(tokenHash)
		if !ok {
			return KeyListItem{}, fmt.Errorf("/key/list returned a key string without a valid SHA256 management hash")
		}
		return KeyListItem{KeyName: types.StringValue(canonicalHash), Blocked: types.BoolValue(false)}, nil
	}

	keyMap, err := decodeListObject(trimmed, "/key/list", "key item")
	if err != nil {
		return KeyListItem{}, err
	}
	item := KeyListItem{Blocked: types.BoolValue(false)}
	token, ok := keyMap["token"].(string)
	canonicalHash, validHash := canonicalSHA256ManagementHash(token)
	if !ok || !validHash {
		return KeyListItem{}, fmt.Errorf("/key/list returned a key object without a valid token management hash")
	}
	// LiteLLM v1.98 key_name includes a raw-token suffix. Never read it into
	// Terraform state; token is the endpoint's hash-only management identity.
	item.KeyName = types.StringValue(canonicalHash)
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
	}
	return item, nil
}

func canonicalSHA256ManagementHash(value string) (string, bool) {
	if len(value) != sha256ManagementHashLength {
		return "", false
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", false
	}
	return strings.ToLower(value), true
}
