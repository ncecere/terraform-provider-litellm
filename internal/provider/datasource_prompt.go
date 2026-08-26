package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PromptDataSource{}

func NewPromptDataSource() datasource.DataSource {
	return &PromptDataSource{}
}

type PromptDataSource struct {
	client *Client
}

type PromptDataSourceModel struct {
	ID                                types.String `tfsdk:"id"`
	PromptID                          types.String `tfsdk:"prompt_id"`
	PromptIntegration                 types.String `tfsdk:"prompt_integration"`
	APIBase                           types.String `tfsdk:"api_base"`
	ProviderSpecificQueryParams       types.String `tfsdk:"provider_specific_query_params"`
	IgnorePromptManagerModel          types.Bool   `tfsdk:"ignore_prompt_manager_model"`
	IgnorePromptManagerOptionalParams types.Bool   `tfsdk:"ignore_prompt_manager_optional_params"`
	DotpromptContent                  types.String `tfsdk:"dotprompt_content"`
	PromptType                        types.String `tfsdk:"prompt_type"`
	Environment                       types.String `tfsdk:"environment"`
	Version                           types.Int64  `tfsdk:"version"`
	CreatedAt                         types.String `tfsdk:"created_at"`
	UpdatedAt                         types.String `tfsdk:"updated_at"`
}

func (d *PromptDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompt"
}

func (d *PromptDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM prompt.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this prompt.",
				Computed:    true,
			},
			"prompt_id": schema.StringAttribute{
				Description: "The base prompt ID to look up.",
				Required:    true,
			},
			"environment": schema.StringAttribute{
				Description: "Prompt environment. Defaults to development.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"version": schema.Int64Attribute{
				Description: "Specific prompt version. When omitted, the latest version in the environment is selected.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
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
			"dotprompt_content": schema.StringAttribute{
				Description: "Content for dotprompt integration.",
				Computed:    true,
			},
			"prompt_type": schema.StringAttribute{
				Description: "Type of prompt: 'config' or 'db'.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Creation timestamp of the selected version.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Last-update timestamp of the selected version.",
				Computed:    true,
			},
		},
	}
}

func (d *PromptDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PromptDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PromptDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	promptID := data.PromptID.ValueString()
	environment := promptEnvironment(data.Environment.ValueString())
	var requestedVersion *int64
	if !data.Version.IsNull() && !data.Version.IsUnknown() {
		value := data.Version.ValueInt64()
		if value <= 0 {
			resp.Diagnostics.AddError("Invalid Prompt Version", "version must be a positive integer")
			return
		}
		requestedVersion = &value
	}
	endpoint := promptEndpoint(promptID, environment, requestedVersion)

	var rawResult map[string]interface{}
	if err := readPromptDataSourceWithRetry(ctx, d.client, endpoint, &rawResult, 8); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read prompt: %s", err))
		return
	}
	observed, err := promptObject(rawResult, true, promptID, environment)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if !observed.HasVersion || (requestedVersion != nil && observed.Version != *requestedVersion) {
		resp.Diagnostics.AddError("Invalid API Response", "Prompt response omitted or mismatched the selected version")
		return
	}
	integration, ok := observed.Params["prompt_integration"].(string)
	if !ok || integration == "" {
		resp.Diagnostics.AddError("Invalid API Response", "Prompt response omitted required litellm_params.prompt_integration")
		return
	}

	data.ID = types.StringValue(observed.PromptID)
	data.PromptID = types.StringValue(observed.PromptID)
	data.Environment = types.StringValue(observed.Environment)
	data.Version = types.Int64Value(observed.Version)
	data.PromptIntegration = types.StringValue(integration)
	data.APIBase, err = promptStringFromAPI(observed.Params, "api_base")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.DotpromptContent, err = promptStringFromAPI(observed.Params, "dotprompt_content")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.IgnorePromptManagerModel, err = promptBoolFromAPI(observed.Params, "ignore_prompt_manager_model")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.IgnorePromptManagerOptionalParams, err = promptBoolFromAPI(observed.Params, "ignore_prompt_manager_optional_params")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	data.ProviderSpecificQueryParams = types.StringNull()
	if value, exists := observed.Params["provider_specific_query_params"]; exists && value != nil {
		object, valid := value.(map[string]interface{})
		if !valid {
			resp.Diagnostics.AddError("Invalid API Response", "Prompt response returned invalid provider_specific_query_params")
			return
		}
		encoded, marshalErr := json.Marshal(object)
		if marshalErr != nil {
			resp.Diagnostics.AddError("Invalid API Response", "Prompt response provider_specific_query_params could not be encoded")
			return
		}
		data.ProviderSpecificQueryParams = types.StringValue(string(encoded))
	}
	data.PromptType = types.StringNull()
	if value, exists := observed.Info["prompt_type"]; exists && value != nil {
		promptType, valid := value.(string)
		if !valid {
			resp.Diagnostics.AddError("Invalid API Response", "Prompt response returned invalid prompt_info.prompt_type")
			return
		}
		data.PromptType = types.StringValue(promptType)
	}
	data.CreatedAt = types.StringNull()
	data.UpdatedAt = types.StringNull()
	if observed.CreatedAt != nil {
		data.CreatedAt = types.StringValue(*observed.CreatedAt)
	}
	if observed.UpdatedAt != nil {
		data.UpdatedAt = types.StringValue(*observed.UpdatedAt)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func readPromptDataSourceWithRetry(ctx context.Context, client *Client, endpoint string, result *map[string]interface{}, maxRetries int) error {
	var err error
	delay := 1 * time.Second
	maxDelay := 10 * time.Second

	for i := 0; i < maxRetries; i++ {
		err = client.DoRequestWithResponse(ctx, "GET", endpoint, nil, result)
		if err == nil {
			return nil
		}

		if !isPromptAbsentError(err) {
			return err
		}

		if i < maxRetries-1 {
			time.Sleep(delay)
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	return err
}
