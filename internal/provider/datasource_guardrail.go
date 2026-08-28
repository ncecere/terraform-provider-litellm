package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &GuardrailDataSource{}

func NewGuardrailDataSource() datasource.DataSource {
	return &GuardrailDataSource{}
}

type GuardrailDataSource struct {
	client *Client
}

type GuardrailDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	GuardrailID   types.String `tfsdk:"guardrail_id"`
	GuardrailName types.String `tfsdk:"guardrail_name"`
	Guardrail     types.String `tfsdk:"guardrail"`
	Mode          types.String `tfsdk:"mode"`
	DefaultOn     types.Bool   `tfsdk:"default_on"`
	LitellmParams types.String `tfsdk:"litellm_params"`
	GuardrailInfo types.String `tfsdk:"guardrail_info"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

func (d *GuardrailDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrail"
}

func (d *GuardrailDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM guardrail.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this guardrail.",
				Computed:    true,
			},
			"guardrail_id": schema.StringAttribute{
				Description: "The guardrail ID to look up.",
				Required:    true,
			},
			"guardrail_name": schema.StringAttribute{
				Description: "Human-readable name for the guardrail.",
				Computed:    true,
			},
			"guardrail": schema.StringAttribute{
				Description: "The guardrail integration type.",
				Computed:    true,
			},
			"mode": schema.StringAttribute{
				Description: "When to apply the guardrail (pre_call, post_call, during_call, or JSON array).",
				Computed:    true,
			},
			"default_on": schema.BoolAttribute{
				Description: "Whether the guardrail is enabled by default.",
				Computed:    true,
			},
			"litellm_params": schema.StringAttribute{
				Description: "JSON string containing additional provider-specific parameters.",
				Computed:    true,
				Sensitive:   true,
			},
			"guardrail_info": schema.StringAttribute{
				Description: "JSON string containing additional metadata.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the guardrail was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the guardrail was last updated.",
				Computed:    true,
			},
		},
	}
}

func (d *GuardrailDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *GuardrailDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data GuardrailDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	guardrailID := data.GuardrailID.ValueString()
	endpoint := endpointWithPathSegment("/guardrails/", guardrailID, "/info")
	var raw map[string]interface{}
	if err := d.client.DoReadWithResponse(ctx, http.MethodGet, endpoint, nil, &raw); err != nil {
		resp.Diagnostics.AddError("Guardrail Read Error", "Unable to read the guardrail. Response and request details were omitted.")
		return
	}
	complete, err := projectGuardrailDataSourceAPIObject(raw, guardrailID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed or identity-mismatched guardrail response. Response details were omitted.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &complete)...)
}

func projectGuardrailDataSourceAPIObject(raw map[string]interface{}, guardrailID string) (GuardrailDataSourceModel, error) {
	observed, err := decodeGuardrailAPIObject(raw, guardrailID, true)
	if err != nil {
		return GuardrailDataSourceModel{}, err
	}
	if observed.DefinitionLocation != "db" && observed.DefinitionLocation != "config" {
		return GuardrailDataSourceModel{}, fmt.Errorf("guardrail response omitted its definition location")
	}
	if observed.Params == nil {
		return GuardrailDataSourceModel{}, fmt.Errorf("guardrail response omitted required parameters")
	}

	complete := GuardrailDataSourceModel{
		ID:            types.StringValue(observed.ID),
		GuardrailID:   types.StringValue(observed.ID),
		GuardrailName: types.StringValue(observed.Name),
		Guardrail:     types.StringNull(),
		Mode:          types.StringNull(),
		DefaultOn:     types.BoolNull(),
		LitellmParams: types.StringNull(),
		GuardrailInfo: types.StringNull(),
		CreatedAt:     types.StringNull(),
		UpdatedAt:     types.StringNull(),
	}
	if observed.CreatedAt != nil {
		complete.CreatedAt = types.StringValue(*observed.CreatedAt)
	}
	if observed.UpdatedAt != nil {
		complete.UpdatedAt = types.StringValue(*observed.UpdatedAt)
	}
	guardrail, ok := observed.Params["guardrail"].(string)
	if !ok || guardrail == "" {
		return GuardrailDataSourceModel{}, fmt.Errorf("guardrail response omitted required integration")
	}
	complete.Guardrail = types.StringValue(guardrail)
	mode, present, err := guardrailModeFromAPI(observed.Params)
	if err != nil || !present {
		return GuardrailDataSourceModel{}, fmt.Errorf("guardrail response omitted or returned an invalid mode")
	}
	complete.Mode = types.StringValue(mode)
	if value, exists := observed.Params["default_on"]; exists && value != nil {
		defaultOn, ok := value.(bool)
		if !ok {
			return GuardrailDataSourceModel{}, fmt.Errorf("guardrail response returned an invalid default state")
		}
		complete.DefaultOn = types.BoolValue(defaultOn)
	}
	additional := guardrailAdditionalParams(observed.Params)
	encoded, err := canonicalGuardrailJSONObject(additional, "litellm_params")
	if err != nil {
		return GuardrailDataSourceModel{}, err
	}
	complete.LitellmParams = types.StringValue(encoded)
	if observed.Info != nil {
		encoded, err := canonicalGuardrailJSONObject(observed.Info, "guardrail_info")
		if err != nil {
			return GuardrailDataSourceModel{}, err
		}
		complete.GuardrailInfo = types.StringValue(encoded)
	}
	return complete, nil
}
