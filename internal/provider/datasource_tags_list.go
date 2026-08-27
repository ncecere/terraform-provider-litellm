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

var _ datasource.DataSource = &TagsListDataSource{}

func NewTagsListDataSource() datasource.DataSource { return &TagsListDataSource{} }

type TagsListDataSource struct{ client *Client }

type TagsListDataSourceModel struct {
	ID   types.String       `tfsdk:"id"`
	Tags []TagListItemModel `tfsdk:"tags"`
}

type TagListItemModel struct {
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

func (d *TagsListDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tags"
}

func (d *TagsListDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches all LiteLLM tags with authoritative nested budget fields when a stored budget relation exists.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Description: "Placeholder identifier for this data source.", Computed: true},
			"tags": schema.ListNestedAttribute{
				Description: "List of tags.", Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":                  schema.StringAttribute{Description: "The tag name.", Computed: true},
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
				}},
			},
		},
	}
}

func (d *TagsListDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func (d *TagsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TagsListDataSourceModel
	results, err := fetchTopLevelListObjects(ctx, d.client, "/tag/list", "tag item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list tags: %s", err))
		return
	}
	tags := make([]TagListItemModel, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, object := range results {
		var tag TagListItemModel
		tag.Name, err = dataSourceRequiredStringAt(object, "name")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", "/tag/list returned a tag object without a nonempty canonical name")
			return
		}
		if err := dataSourceListIdentity(seen, tag.Name.ValueString(), "/tag/list", "name"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if err := updateTagDescription(&tag.Description, object); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		models, err := dataSourceNullableStringListAt(object, "models")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		tag.Models = models
		if !models.IsNull() {
			modelNames := make([]string, 0, len(models.Elements()))
			for _, value := range models.Elements() {
				modelNames = append(modelNames, value.(types.String).ValueString())
			}
			sort.Strings(modelNames)
			items := make([]attr.Value, 0, len(modelNames))
			for _, model := range modelNames {
				items = append(items, types.StringValue(model))
			}
			tag.Models, _ = types.ListValue(types.StringType, items)
		}
		table, budgetErr := parseBudgetTable(object)
		if budgetErr == nil {
			budgetErr = validateDataSourceBudgetTableNumbers(table)
		}
		if budgetErr == nil {
			budgetErr = updateTagBudgetState(tagListBudgetTargets(&tag), object, false, true)
		}
		if budgetErr != nil {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned malformed tag budget data.")
			return
		}
		tags = append(tags, tag)
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].Name.ValueString() < tags[j].Name.ValueString() })
	data.ID = types.StringValue("tags")
	data.Tags = tags
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
