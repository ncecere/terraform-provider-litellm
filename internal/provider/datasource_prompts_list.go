package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PromptsListDataSource{}

func NewPromptsListDataSource() datasource.DataSource {
	return &PromptsListDataSource{}
}

type PromptsListDataSource struct {
	client *Client
}

type PromptsListDataSourceModel struct {
	ID          types.String          `tfsdk:"id"`
	Environment types.String          `tfsdk:"environment"`
	Prompts     []PromptListItemModel `tfsdk:"prompts"`
}

type PromptListItemModel struct {
	PromptID                          types.String `tfsdk:"prompt_id"`
	PromptIntegration                 types.String `tfsdk:"prompt_integration"`
	APIBase                           types.String `tfsdk:"api_base"`
	ProviderSpecificQueryParams       types.String `tfsdk:"provider_specific_query_params"`
	IgnorePromptManagerModel          types.Bool   `tfsdk:"ignore_prompt_manager_model"`
	IgnorePromptManagerOptionalParams types.Bool   `tfsdk:"ignore_prompt_manager_optional_params"`
	PromptType                        types.String `tfsdk:"prompt_type"`
	Environment                       types.String `tfsdk:"environment"`
	Version                           types.Int64  `tfsdk:"version"`
	CreatedAt                         types.String `tfsdk:"created_at"`
	UpdatedAt                         types.String `tfsdk:"updated_at"`
}

func (d *PromptsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompts"
}

func (d *PromptsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM prompts.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"environment": schema.StringAttribute{
				Description: "Optional environment filter. When omitted, LiteLLM returns its unscoped latest-prompt inventory.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"prompts": schema.ListNestedAttribute{
				Description: "List of prompts.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"prompt_id": schema.StringAttribute{
							Description: "The prompt ID.",
							Computed:    true,
						},
						"environment": schema.StringAttribute{
							Description: "Prompt environment.",
							Computed:    true,
						},
						"version": schema.Int64Attribute{
							Description: "Latest version represented by this item when LiteLLM returns one.",
							Computed:    true,
						},
						"created_at": schema.StringAttribute{
							Description: "Creation timestamp of this version.",
							Computed:    true,
						},
						"updated_at": schema.StringAttribute{
							Description: "Last-update timestamp of this version.",
							Computed:    true,
						},
						"prompt_integration": schema.StringAttribute{
							Description: "The prompt integration provider.",
							Computed:    true,
						},
						"api_base": schema.StringAttribute{
							Description: "Base URL for the prompt provider API.",
							Computed:    true,
						},
						"provider_specific_query_params": schema.StringAttribute{
							Description: "JSON string of provider-specific query parameters.",
							Computed:    true,
						},
						"ignore_prompt_manager_model": schema.BoolAttribute{
							Description: "If true, ignore the model specified in the prompt manager.",
							Computed:    true,
						},
						"ignore_prompt_manager_optional_params": schema.BoolAttribute{
							Description: "If true, ignore optional params from the prompt manager.",
							Computed:    true,
						},
						"prompt_type": schema.StringAttribute{
							Description: "Type of prompt: 'config' or 'db'.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *PromptsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func promptListItemFromAPI(raw map[string]interface{}, expectedEnvironment string) (PromptListItemModel, error) {
	var item PromptListItemModel
	if version, present := raw["version"]; present && version != nil && !dataSourceAPIJSONNumber(version) {
		return item, dataSourceShapeError([]string{"version"}, "an exact integral JSON number or null")
	}
	observed, err := promptObject(raw, false, "", expectedEnvironment)
	if err != nil {
		return item, err
	}
	integration, ok := observed.Params["prompt_integration"].(string)
	if !ok || integration == "" {
		return item, fmt.Errorf("prompt response omitted required litellm_params.prompt_integration")
	}
	item.PromptID = types.StringValue(observed.PromptID)
	item.PromptIntegration = types.StringValue(integration)
	item.Environment = types.StringValue(observed.Environment)
	item.Version = types.Int64Null()
	item.CreatedAt = types.StringNull()
	item.UpdatedAt = types.StringNull()
	item.PromptType = types.StringNull()
	item.ProviderSpecificQueryParams = types.StringNull()
	if observed.HasVersion {
		item.Version = types.Int64Value(observed.Version)
	}
	if observed.CreatedAt != nil {
		item.CreatedAt = types.StringValue(*observed.CreatedAt)
	}
	if observed.UpdatedAt != nil {
		item.UpdatedAt = types.StringValue(*observed.UpdatedAt)
	}
	item.APIBase, err = promptStringFromAPI(observed.Params, "api_base")
	if err != nil {
		return item, err
	}
	item.IgnorePromptManagerModel, err = promptBoolFromAPI(observed.Params, "ignore_prompt_manager_model")
	if err != nil {
		return item, err
	}
	item.IgnorePromptManagerOptionalParams, err = promptBoolFromAPI(observed.Params, "ignore_prompt_manager_optional_params")
	if err != nil {
		return item, err
	}
	if value, exists := observed.Params["provider_specific_query_params"]; exists && value != nil {
		object, valid := value.(map[string]interface{})
		if !valid {
			return item, fmt.Errorf("prompt response returned invalid provider_specific_query_params")
		}
		encoded, marshalErr := json.Marshal(object)
		if marshalErr != nil {
			return item, fmt.Errorf("prompt response provider_specific_query_params could not be encoded")
		}
		item.ProviderSpecificQueryParams = types.StringValue(string(encoded))
	}
	if value, exists := observed.Info["prompt_type"]; exists && value != nil {
		promptType, valid := value.(string)
		if !valid {
			return item, fmt.Errorf("prompt response returned invalid prompt_info.prompt_type")
		}
		item.PromptType = types.StringValue(promptType)
	}
	return item, nil
}

func fetchPromptListItems(ctx context.Context, client *Client, environment string, configured bool) ([]PromptListItemModel, error) {
	if !configured {
		results, err := fetchEnvelopeListObjects(ctx, client, "/prompts/list", "prompts", "prompt item")
		if err != nil {
			return nil, err
		}
		items := make([]PromptListItemModel, 0, len(results))
		seen := make(map[string]struct{}, len(results))
		for _, result := range results {
			item, err := promptListItemFromAPI(result, "")
			if err != nil {
				return nil, err
			}
			identity := promptListIdentity(item)
			if err := dataSourceListIdentity(seen, identity, "/prompts/list", "prompt identity"); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	}

	// v1.98's process-local registry keys omit environment, so its filtered list
	// can lose one side of a same-ID cross-environment collision. Discover base
	// IDs from both snapshots, then use the authoritative environment-scoped
	// versions route for each logical prompt.
	candidates := make(map[string]struct{})
	for _, endpoint := range []string{"/prompts/list", promptListEndpoint(environment, true)} {
		results, err := fetchEnvelopeListObjects(ctx, client, endpoint, "prompts", "prompt item")
		if err != nil {
			return nil, err
		}
		seen := make(map[string]struct{}, len(results))
		for _, result := range results {
			promptID, identityErr := dataSourceRequiredStringAt(result, "prompt_id")
			if identityErr != nil {
				return nil, fmt.Errorf("prompt list response omitted a non-empty prompt_id")
			}
			if err := dataSourceListIdentity(seen, promptID.ValueString(), endpoint, "prompt_id"); err != nil {
				return nil, err
			}
			candidates[promptID.ValueString()] = struct{}{}
		}
	}
	if len(candidates) > 200 {
		return nil, fmt.Errorf("prompt inventory exceeded the bounded environment-enrichment limit")
	}
	items := make([]PromptListItemModel, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for promptID := range candidates {
		var raw map[string]interface{}
		err := client.DoRequestWithResponse(ctx, "GET", promptEndpoint(promptID, environment, nil), nil, &raw)
		if err != nil {
			if isPromptAbsentError(err) {
				continue
			}
			return nil, err
		}
		spec, ok := raw["prompt_spec"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("prompt response omitted required prompt_spec")
		}
		item, err := promptListItemFromAPI(spec, promptEnvironment(environment))
		if err != nil {
			return nil, err
		}
		if item.PromptID.ValueString() != promptID {
			return nil, fmt.Errorf("prompt info response identity did not match the requested prompt")
		}
		if err := dataSourceListIdentity(seen, promptListIdentity(item), "/prompts/info", "prompt identity"); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func promptListIdentity(item PromptListItemModel) string {
	version := "null"
	if !item.Version.IsNull() {
		version = fmt.Sprintf("%d", item.Version.ValueInt64())
	}
	return item.Environment.ValueString() + "\x00" + item.PromptID.ValueString() + "\x00" + version
}

func (d *PromptsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PromptsListDataSourceModel

	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("environment"), &data.Environment)...)
	if resp.Diagnostics.HasError() {
		return
	}

	environmentConfigured := !data.Environment.IsNull() && !data.Environment.IsUnknown()
	environment := data.Environment.ValueString()
	prompts, err := fetchPromptListItems(ctx, d.client, environment, environmentConfigured)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list prompts: %s", err))
		return
	}
	sort.SliceStable(prompts, func(i, j int) bool {
		leftEnvironment, rightEnvironment := prompts[i].Environment.ValueString(), prompts[j].Environment.ValueString()
		if leftEnvironment != rightEnvironment {
			return leftEnvironment < rightEnvironment
		}
		leftID, rightID := prompts[i].PromptID.ValueString(), prompts[j].PromptID.ValueString()
		if leftID != rightID {
			return leftID < rightID
		}
		return prompts[i].Version.ValueInt64() < prompts[j].Version.ValueInt64()
	})

	data.ID = types.StringValue("prompts")
	data.Prompts = prompts

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
