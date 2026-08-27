package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &KeyDataSource{}
var _ datasource.DataSourceWithConfigValidators = &KeyDataSource{}

func NewKeyDataSource() datasource.DataSource {
	return &KeyDataSource{}
}

type KeyDataSource struct {
	client *Client
}

type KeyDataSourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	Key                 types.String  `tfsdk:"key"`
	KeyHash             types.String  `tfsdk:"key_hash"`
	KeyAlias            types.String  `tfsdk:"key_alias"`
	Models              types.List    `tfsdk:"models"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	Spend               types.Float64 `tfsdk:"spend"`
	UserID              types.String  `tfsdk:"user_id"`
	TeamID              types.String  `tfsdk:"team_id"`
	ProjectID           types.String  `tfsdk:"project_id"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	Metadata            types.Map     `tfsdk:"metadata"`
	Tags                types.List    `tfsdk:"tags"`
	Blocked             types.Bool    `tfsdk:"blocked"`
	RouterSettings      types.Object  `tfsdk:"router_settings"`
}

func (d *KeyDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("key"),
			path.MatchRoot("key_hash"),
		),
	}
}

func (d *KeyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key"
}

func (d *KeyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Non-sensitive SHA256 management identifier for this key.",
				Computed:    true,
			},
			"key": schema.StringAttribute{
				Description: "The raw API key value to look up. Conflicts with key_hash.",
				Optional:    true,
				Sensitive:   true,
			},
			"key_hash": schema.StringAttribute{
				Description: "A sha256:<64-hex> management identifier used to look up a write-only key without reintroducing the raw token into Terraform state. Conflicts with key.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^sha256:[0-9a-fA-F]{64}$`), "must use the sha256:<64-hex> management identifier format"),
				},
			},
			"key_alias": schema.StringAttribute{
				Description: "User-friendly alias for the key.",
				Computed:    true,
			},
			"models": schema.ListAttribute{
				Description: "List of models this key can access.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Maximum budget for this key.",
				Computed:    true,
			},
			"spend": schema.Float64Attribute{
				Description: "Amount spent by this key.",
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
			"project_id": schema.StringAttribute{
				Description: "Project ID associated with this key.",
				Computed:    true,
			},
			"max_parallel_requests": schema.Int64Attribute{
				Description: "Maximum parallel requests allowed.",
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
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset duration.",
				Computed:    true,
			},
			"soft_budget": schema.Float64Attribute{
				Description: "Soft budget limit for warnings.",
				Computed:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the key.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"tags": schema.ListAttribute{
				Description: "Tags for the key.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the key is blocked.",
				Computed:    true,
			},
			"router_settings": keyRouterSettingsDataSourceAttribute(),
		},
	}
}

func (d *KeyDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func keyDataSourceLookup(data *KeyDataSourceModel) (string, string, error) {
	if !data.KeyHash.IsNull() && !data.KeyHash.IsUnknown() {
		hash, err := keyHashFromID(data.KeyHash.ValueString())
		if err != nil {
			return "", "", err
		}
		hash = strings.ToLower(hash)
		return hash, "sha256:" + hash, nil
	}
	if data.Key.IsNull() || data.Key.IsUnknown() || data.Key.ValueString() == "" {
		return "", "", fmt.Errorf("exactly one of key or key_hash must be known and non-empty")
	}
	key := data.Key.ValueString()
	return key, hashKeyForID(key), nil
}

func keyDataSourceReadError(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d while reading the key. The response body was omitted because it may contain the lookup token.", apiErr.StatusCode)
	}
	// Go transport errors can embed the complete query URL, including a raw key.
	return "The key read request failed at the transport layer. Error details were omitted because they may contain the lookup token."
}

func (d *KeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data KeyDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lookupValue, managementID, err := keyDataSourceLookup(&data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Key Lookup", err.Error())
		return
	}

	query := url.Values{"key": []string{lookupValue}}
	endpoint := endpointWithQuery("/key/info", query)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Key Read Error", keyDataSourceReadError(err))
		return
	}

	actualKey, err := dataSourceRequiredStringAt(result, "key")
	if err != nil || actualKey.ValueString() != lookupValue {
		resp.Diagnostics.AddError("Invalid API Response", "Key response identity did not match the requested key.")
		return
	}
	info := result
	if raw, presence, infoErr := apiValueAt(result, "info"); infoErr != nil {
		resp.Diagnostics.AddError("Invalid API Response", infoErr.Error())
		return
	} else if presence == apiValuePresent {
		var ok bool
		info, ok = raw.(map[string]interface{})
		if !ok {
			resp.Diagnostics.AddError("Invalid API Response", dataSourceShapeError([]string{"info"}, "an object or null").Error())
			return
		}
	}

	complete := KeyDataSourceModel{
		ID:      types.StringValue(managementID),
		Key:     data.Key,
		KeyHash: data.KeyHash,
	}
	if complete.KeyAlias, err = dataSourceRoleRedactedNullableStringAt(info, "key_alias"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.UserID, err = dataSourceRoleRedactedNullableStringAt(info, "user_id"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.TeamID, err = dataSourceRoleRedactedNullableStringAt(info, "team_id"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.ProjectID, err = dataSourceRoleRedactedNullableStringAt(info, "project_id"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.BudgetDuration, err = dataSourceRoleRedactedNullableStringAt(info, "budget_duration"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.Models, err = dataSourceRoleRedactedNullableStringListAt(info, "models"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.MaxBudget, err = keyDataSourceNullableFloat64AtPaths(info,
		[]string{"max_budget"}, []string{"litellm_budget_table", "max_budget"}); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.SoftBudget, err = keyDataSourceNullableFloat64AtPaths(info,
		[]string{"litellm_budget_table", "soft_budget"}, []string{"soft_budget"}); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.Spend, err = dataSourceRoleRedactedNullableFloat64At(info, "spend"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.MaxParallelRequests, err = dataSourceRoleRedactedNullableInt64At(info, "max_parallel_requests"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.TPMLimit, err = dataSourceRoleRedactedNullableInt64At(info, "tpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.RPMLimit, err = dataSourceRoleRedactedNullableInt64At(info, "rpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.Blocked, err = dataSourceRoleRedactedNullableBoolAt(info, "blocked"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.Tags, complete.Metadata, err = keyDataSourceCollections(info); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if complete.RouterSettings, _, err = keyRouterSettingsFromAPI(info["router_settings"], types.ObjectNull(keyRouterSettingsAttrTypes)); err != nil {
		resp.Diagnostics.AddError("Router Settings Read Error", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &complete)...)
}

func keyDataSourceNullableFloat64AtPaths(object map[string]interface{}, paths ...[]string) (types.Float64, error) {
	selected, err := firstAPIFieldPath(object, paths...)
	if err != nil {
		return types.Float64Null(), err
	}
	if selected == nil {
		return types.Float64Null(), nil
	}
	return dataSourceNullableFloat64At(object, selected...)
}

func keyDataSourceCollections(info map[string]interface{}) (types.List, types.Map, error) {
	metadataRaw, metadataPresence, err := apiValueAt(info, "metadata")
	if err != nil {
		return types.ListNull(types.StringType), types.MapNull(types.StringType), err
	}
	var metadataObject map[string]interface{}
	if metadataPresence == apiValuePresent {
		var ok bool
		metadataObject, ok = metadataRaw.(map[string]interface{})
		if !ok {
			return types.ListNull(types.StringType), types.MapNull(types.StringType), dataSourceShapeError([]string{"metadata"}, "an object of strings or null")
		}
	}

	// LiteLLM stores tags in metadata while historical versions also projected
	// them at the top level. Validate both locations before applying the
	// top-level compatibility precedence.
	metadataTags := types.ListNull(types.StringType)
	if metadataPresence == apiValuePresent {
		metadataTags, err = dataSourceNullableStringListAt(info, "metadata", "tags")
		if err != nil {
			return types.ListNull(types.StringType), types.MapNull(types.StringType), err
		}
	}
	_, topTagsPresence, err := apiValueAt(info, "tags")
	if err != nil {
		return types.ListNull(types.StringType), types.MapNull(types.StringType), err
	}
	tags := metadataTags
	if topTagsPresence != apiValueAbsent {
		tags, err = dataSourceNullableStringListAt(info, "tags")
		if err != nil {
			return types.ListNull(types.StringType), types.MapNull(types.StringType), err
		}
	}

	if metadataPresence != apiValuePresent {
		metadata, mapErr := dataSourceNullableStringMapAt(info, "metadata")
		return tags, metadata, mapErr
	}
	projected := make(map[string]interface{}, len(metadataObject))
	for key, value := range metadataObject {
		if key != "tags" {
			projected[key] = value
		}
	}
	wrapper := map[string]interface{}{"metadata": projected}
	metadata, err := dataSourceNullableStringMapAt(wrapper, "metadata")
	if err != nil {
		return types.ListNull(types.StringType), types.MapNull(types.StringType), err
	}
	return tags, metadata, nil
}
