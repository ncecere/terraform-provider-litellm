package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &GuardrailsListDataSource{}

func NewGuardrailsListDataSource() datasource.DataSource {
	return &GuardrailsListDataSource{}
}

type GuardrailsListDataSource struct {
	client *Client
}

type GuardrailsListDataSourceModel struct {
	ID         types.String             `tfsdk:"id"`
	Guardrails []GuardrailListItemModel `tfsdk:"guardrails"`
}

type GuardrailListItemModel struct {
	GuardrailID   types.String `tfsdk:"guardrail_id"`
	GuardrailName types.String `tfsdk:"guardrail_name"`
	Guardrail     types.String `tfsdk:"guardrail"`
	Mode          types.String `tfsdk:"mode"`
	DefaultOn     types.Bool   `tfsdk:"default_on"`
	LitellmParams types.String `tfsdk:"litellm_params"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

func (d *GuardrailsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrails"
}

func (d *GuardrailsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM guardrails.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"guardrails": schema.ListNestedAttribute{
				Description: "List of guardrails.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"guardrail_id": schema.StringAttribute{
							Description: "The guardrail ID.",
							Computed:    true,
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
							Description: "When to apply the guardrail.",
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
						"created_at": schema.StringAttribute{
							Description: "Timestamp when the guardrail was created.",
							Computed:    true,
						},
						"updated_at": schema.StringAttribute{
							Description: "Timestamp when the guardrail was last updated.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *GuardrailsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func guardrailListItemFromAPI(raw map[string]interface{}) (GuardrailListItemModel, error) {
	var item GuardrailListItemModel
	observed, err := decodeGuardrailAPIObject(raw, "", true)
	if err != nil {
		return item, err
	}
	if observed.Params == nil {
		return item, fmt.Errorf("guardrail response omitted required litellm_params")
	}
	guardrail, ok := observed.Params["guardrail"].(string)
	if !ok || guardrail == "" {
		return item, fmt.Errorf("guardrail response omitted required litellm_params.guardrail")
	}
	mode, present, err := guardrailModeFromAPI(observed.Params)
	if err != nil || !present {
		return item, fmt.Errorf("guardrail response omitted or returned invalid litellm_params.mode")
	}

	item.GuardrailID = types.StringValue(observed.ID)
	item.GuardrailName = types.StringValue(observed.Name)
	item.Guardrail = types.StringValue(guardrail)
	item.Mode = types.StringValue(mode)
	item.DefaultOn = types.BoolNull()
	item.LitellmParams = types.StringNull()
	item.CreatedAt = types.StringNull()
	item.UpdatedAt = types.StringNull()
	if value, exists := observed.Params["default_on"]; exists && value != nil {
		defaultOn, ok := value.(bool)
		if !ok {
			return item, fmt.Errorf("guardrail response returned invalid litellm_params.default_on")
		}
		item.DefaultOn = types.BoolValue(defaultOn)
	}
	additional := guardrailAdditionalParams(observed.Params)
	encoded, err := canonicalGuardrailJSONObject(additional, "litellm_params")
	if err != nil {
		return item, err
	}
	item.LitellmParams = types.StringValue(encoded)
	if observed.CreatedAt != nil {
		item.CreatedAt = types.StringValue(*observed.CreatedAt)
	}
	if observed.UpdatedAt != nil {
		item.UpdatedAt = types.StringValue(*observed.UpdatedAt)
	}
	return item, nil
}

func fetchGuardrailListItems(ctx context.Context, client *Client) ([]GuardrailListItemModel, error) {
	const endpoint = "/v2/guardrails/list"
	results, err := fetchEnvelopeListObjects(ctx, client, endpoint, "guardrails", "guardrail item")
	if err != nil {
		return nil, err
	}
	guardrails := make([]GuardrailListItemModel, 0, len(results))
	for _, result := range results {
		guardrail, err := guardrailListItemFromAPI(result)
		if err != nil {
			return nil, err
		}
		guardrails = append(guardrails, guardrail)
	}
	return guardrails, nil
}

func (d *GuardrailsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data GuardrailsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	guardrails, err := fetchGuardrailListItems(ctx, d.client)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list guardrails: %s", err))
		return
	}
	sort.SliceStable(guardrails, func(i, j int) bool {
		leftID := guardrails[i].GuardrailID.ValueString()
		rightID := guardrails[j].GuardrailID.ValueString()
		if leftID != rightID {
			return leftID < rightID
		}
		return guardrails[i].GuardrailName.ValueString() < guardrails[j].GuardrailName.ValueString()
	})

	data.ID = types.StringValue("guardrails")
	data.Guardrails = guardrails

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
