package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &KeyDataSource{}

func NewKeyDataSource() datasource.DataSource {
	return &KeyDataSource{}
}

type KeyDataSource struct {
	client *Client
}

type KeyDataSourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	Key                 types.String  `tfsdk:"key"`
	KeyAlias            types.String  `tfsdk:"key_alias"`
	Models              types.List    `tfsdk:"models"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	Spend               types.Float64 `tfsdk:"spend"`
	UserID              types.String  `tfsdk:"user_id"`
	TeamID              types.String  `tfsdk:"team_id"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	Metadata            types.Map     `tfsdk:"metadata"`
	Tags                types.List    `tfsdk:"tags"`
	Blocked             types.Bool    `tfsdk:"blocked"`
}

func (d *KeyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key"
}

func (d *KeyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this key.",
				Computed:    true,
			},
			"key": schema.StringAttribute{
				Description: "The API key value to look up.",
				Required:    true,
				Sensitive:   true,
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

func (d *KeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data KeyDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyValue := data.Key.ValueString()
	endpoint := fmt.Sprintf("/key/info?key=%s", keyValue)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read key: %s", err))
		return
	}

	// The /key/info endpoint may return key data nested inside "info"
	info := result
	if nested, ok := result["info"].(map[string]interface{}); ok {
		info = nested
	}

	// Set ID
	data.ID = data.Key

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

	// Handle tags list
	if tags, ok := info["tags"].([]interface{}); ok {
		tagsList := make([]attr.Value, len(tags))
		for i, t := range tags {
			if str, ok := t.(string); ok {
				tagsList[i] = types.StringValue(str)
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, tagsList)
	} else {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
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
