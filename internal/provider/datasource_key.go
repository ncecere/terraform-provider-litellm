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
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	endpoint := fmt.Sprintf("/key/info?key=%s", url.QueryEscape(lookupValue))

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Key Read Error", keyDataSourceReadError(err))
		return
	}

	// The /key/info endpoint may return key data nested inside "info"
	info := result
	if nested, ok := result["info"].(map[string]interface{}); ok {
		info = nested
	}

	// Never copy the sensitive raw key into the non-sensitive data source ID.
	data.ID = types.StringValue(managementID)

	// Update fields from response
	if keyAlias, ok := info["key_alias"].(string); ok {
		data.KeyAlias = types.StringValue(keyAlias)
	}
	if userID, ok := info["user_id"].(string); ok {
		data.UserID = types.StringValue(userID)
	}
	if teamID, ok := info["team_id"].(string); ok {
		data.TeamID = types.StringValue(teamID)
	}
	if projectID, ok := info["project_id"].(string); ok {
		data.ProjectID = types.StringValue(projectID)
	}
	if budgetDuration, ok := info["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}

	// Numeric fields
	if maxBudget, ok := info["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if spend, ok := info["spend"].(float64); ok {
		data.Spend = types.Float64Value(spend)
	}
	if softBudget, ok := info["soft_budget"].(float64); ok {
		data.SoftBudget = types.Float64Value(softBudget)
	}
	if maxParallel, ok := info["max_parallel_requests"].(float64); ok {
		data.MaxParallelRequests = types.Int64Value(int64(maxParallel))
	}
	if tpmLimit, ok := info["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	}
	if rpmLimit, ok := info["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	}

	// Boolean fields
	if blocked, ok := info["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	} else {
		data.Blocked = types.BoolValue(false)
	}

	// Handle models list
	if models, ok := info["models"].([]interface{}); ok {
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

	// Handle tags list.
	// LiteLLM stores tags inside metadata["tags"] rather than as a top-level field in /key/info,
	// so we check both locations.
	var rawTags []interface{}
	if tags, ok := info["tags"].([]interface{}); ok {
		rawTags = tags
	} else if metadata, ok := info["metadata"].(map[string]interface{}); ok {
		if tags, ok := metadata["tags"].([]interface{}); ok {
			rawTags = tags
		}
	}
	if len(rawTags) > 0 {
		tagsList := make([]attr.Value, 0, len(rawTags))
		for _, t := range rawTags {
			if str, ok := t.(string); ok {
				tagsList = append(tagsList, types.StringValue(str))
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, tagsList)
	} else {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	if routerSettings, present, err := keyRouterSettingsFromAPI(info["router_settings"], types.ObjectNull(keyRouterSettingsAttrTypes)); err != nil {
		resp.Diagnostics.AddError("Router Settings Read Error", err.Error())
		return
	} else if present {
		data.RouterSettings = routerSettings
	} else {
		data.RouterSettings = types.ObjectNull(keyRouterSettingsAttrTypes)
	}

	// Handle metadata map
	if metadata, ok := info["metadata"].(map[string]interface{}); ok {
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
