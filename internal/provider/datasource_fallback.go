package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &FallbackDataSource{}

func NewFallbackDataSource() datasource.DataSource {
	return &FallbackDataSource{}
}

type FallbackDataSource struct {
	client *Client
}

type FallbackDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	Model          types.String `tfsdk:"model"`
	FallbackType   types.String `tfsdk:"fallback_type"`
	FallbackModels types.List   `tfsdk:"fallback_models"`
}

func (d *FallbackDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fallback"
}

func (d *FallbackDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves fallback configuration for a LiteLLM model.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for this fallback (model:fallback_type).",
				Computed:    true,
			},
			"model": schema.StringAttribute{
				Description: "The model name to get fallbacks for.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"fallback_type": schema.StringAttribute{
				Description: "Type of fallback: general, context_window, or content_policy. Defaults to general.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					stringvalidator.OneOf(supportedFallbackTypes...),
				},
			},
			"fallback_models": schema.ListAttribute{
				Description: "List of fallback model names in order of priority.",
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (d *FallbackDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FallbackDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FallbackDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fallbackType := data.FallbackType.ValueString()
	if fallbackType == "" {
		fallbackType = "general"
	}

	data.FallbackType = types.StringValue(fallbackType)
	if err := d.readFallbackWithRetry(ctx, &data, fallbackReadMaxAttempts, fallbackReadInitialDelay, fallbackReadMaxDelay); err != nil {
		resp.Diagnostics.AddError("Fallback Data Source Read Error", fallbackOperationDiagnostic("read", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (d *FallbackDataSource) readFallbackWithRetry(ctx context.Context, data *FallbackDataSourceModel, maxAttempts int, initialDelay, maxDelay time.Duration) error {
	return retryFallbackRead(ctx, maxAttempts, initialDelay, maxDelay, func() error {
		return d.readFallback(ctx, data)
	})
}

func (d *FallbackDataSource) readFallback(ctx context.Context, data *FallbackDataSourceModel) error {
	endpoint := fallbackEndpoint(data.Model.ValueString(), data.FallbackType.ValueString())

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return err
	}

	if err := validateFallbackReadResponse(result, data.Model.ValueString(), data.FallbackType.ValueString()); err != nil {
		return err
	}
	data.ID = types.StringValue(data.Model.ValueString() + ":" + data.FallbackType.ValueString())
	fallbackModels := result["fallback_models"].([]interface{})
	list := make([]attr.Value, 0, len(fallbackModels))
	for _, model := range fallbackModels {
		list = append(list, types.StringValue(model.(string)))
	}
	data.FallbackModels, _ = types.ListValue(types.StringType, list)

	return nil
}
