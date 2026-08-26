package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &TagDataSource{}

func NewTagDataSource() datasource.DataSource { return &TagDataSource{} }

type TagDataSource struct{ client *Client }

type TagDataSourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	Name                types.String  `tfsdk:"name"`
	Description         types.String  `tfsdk:"description"`
	Models              types.List    `tfsdk:"models"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	ModelMaxBudget      types.String  `tfsdk:"model_max_budget"`
}

func (d *TagDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tag"
}

func (d *TagDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM tag, including its authoritative nested budget relation.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The unique identifier for this tag.", Computed: true},
			"name":                  schema.StringAttribute{Description: "The tag name to look up.", Required: true},
			"description":           schema.StringAttribute{Description: "Description of the tag.", Computed: true},
			"models":                schema.ListAttribute{Description: "Models associated with this tag.", Computed: true, ElementType: types.StringType},
			"budget_id":             schema.StringAttribute{Description: "Budget ID associated with this tag.", Computed: true},
			"max_budget":            schema.Float64Attribute{Description: "Max budget in USD.", Computed: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget in USD.", Computed: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Max concurrent requests allowed.", Computed: true},
			"tpm_limit":             schema.Int64Attribute{Description: "Max tokens per minute.", Computed: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Max requests per minute.", Computed: true},
			"budget_duration":       schema.StringAttribute{Description: "Duration for budget reset.", Computed: true},
			"model_max_budget":      schema.StringAttribute{Description: "Canonical JSON object of per-model GenericBudgetConfig values.", Computed: true},
		},
	}
}

func (d *TagDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := configuredClient(req.ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func (d *TagDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TagDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tagName := data.Name.ValueString()
	var raw interface{}
	if err := d.client.DoRequestWithResponse(ctx, "POST", "/tag/info", map[string]interface{}{"names": []string{tagName}}, &raw); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tag: %s", err))
		return
	}
	object, err := selectTagInfoObject(raw, tagName, false)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	name, ok := object["name"].(string)
	if !ok || name != tagName {
		resp.Diagnostics.AddError("Invalid API Response", fmt.Sprintf("tag response identity does not match requested name %q", tagName))
		return
	}
	data.ID = types.StringValue(name)
	if err := updateTagDescription(&data.Description, object); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	models, presence, err := stringListFromAPI(object, "models")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if presence == apiValuePresent {
		modelNames := make([]string, 0, len(models.Elements()))
		for _, value := range models.Elements() {
			modelNames = append(modelNames, value.(types.String).ValueString())
		}
		sort.Strings(modelNames)
		items := make([]attr.Value, 0, len(modelNames))
		for _, model := range modelNames {
			items = append(items, types.StringValue(model))
		}
		data.Models, _ = types.ListValue(types.StringType, items)
	} else {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}
	if err := updateTagBudgetState(tagDataSourceBudgetTargets(&data), object, false, true); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// parseTagInfoResult remains as a compatibility seam for existing focused
// tests and callers. Lifecycle code uses selectTagInfoObject so malformed and
// ambiguous envelopes produce diagnostics rather than an empty projection.
func parseTagInfoResult(raw interface{}, tagName string) map[string]interface{} {
	object, err := selectTagInfoObject(raw, tagName, false)
	if err != nil {
		return nil
	}
	return object
}
