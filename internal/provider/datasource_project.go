package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ProjectDataSource{}

func NewProjectDataSource() datasource.DataSource { return &ProjectDataSource{} }

type ProjectDataSource struct{ client *Client }

type ProjectDataSourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	ProjectAlias        types.String  `tfsdk:"project_alias"`
	Description         types.String  `tfsdk:"description"`
	TeamID              types.String  `tfsdk:"team_id"`
	Models              types.List    `tfsdk:"models"`
	Metadata            types.Map     `tfsdk:"metadata"`
	Tags                types.List    `tfsdk:"tags"`
	Blocked             types.Bool    `tfsdk:"blocked"`
	Spend               types.Float64 `tfsdk:"spend"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	ModelRPMLimit       types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit       types.Map     `tfsdk:"model_tpm_limit"`
	CreatedAt           types.String  `tfsdk:"created_at"`
	UpdatedAt           types.String  `tfsdk:"updated_at"`
	CreatedBy           types.String  `tfsdk:"created_by"`
	UpdatedBy           types.String  `tfsdk:"updated_by"`
}

func (d *ProjectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *ProjectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a LiteLLM Project, including authoritative nested budget controls.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The project ID to look up.", Required: true},
			"project_alias":         schema.StringAttribute{Description: "Human-friendly project name.", Computed: true},
			"description":           schema.StringAttribute{Description: "Project description.", Computed: true},
			"team_id":               schema.StringAttribute{Description: "Parent team ID.", Computed: true},
			"models":                schema.ListAttribute{Description: "Models the project can access.", Computed: true, ElementType: types.StringType},
			"metadata":              schema.MapAttribute{Description: "Project metadata excluding dedicated tags/rate maps.", Computed: true, ElementType: types.StringType},
			"tags":                  schema.ListAttribute{Description: "Project tags stored in metadata.", Computed: true, ElementType: types.StringType},
			"blocked":               schema.BoolAttribute{Description: "Whether the project is blocked.", Computed: true},
			"spend":                 schema.Float64Attribute{Description: "Total project spend.", Computed: true},
			"budget_id":             schema.StringAttribute{Description: "Associated budget ID.", Computed: true},
			"max_budget":            schema.Float64Attribute{Description: "Maximum hard budget.", Computed: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget alert threshold.", Computed: true},
			"budget_duration":       schema.StringAttribute{Description: "Budget reset duration.", Computed: true},
			"tpm_limit":             schema.Int64Attribute{Description: "Tokens per minute limit.", Computed: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Requests per minute limit.", Computed: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Maximum parallel requests.", Computed: true},
			"model_rpm_limit":       schema.MapAttribute{Description: "Per-model RPM limits.", Computed: true, ElementType: types.Int64Type},
			"model_tpm_limit":       schema.MapAttribute{Description: "Per-model TPM limits.", Computed: true, ElementType: types.Int64Type},
			"created_at":            schema.StringAttribute{Description: "Creation timestamp.", Computed: true},
			"updated_at":            schema.StringAttribute{Description: "Last update timestamp.", Computed: true},
			"created_by":            schema.StringAttribute{Description: "Creating user.", Computed: true},
			"updated_by":            schema.StringAttribute{Description: "Last updating user.", Computed: true},
		},
	}
}

func (d *ProjectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func (d *ProjectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ProjectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID := data.ID.ValueString()
	var result map[string]interface{}
	endpoint := "/project/info?project_id=" + url.QueryEscape(projectID)
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read project: %s", err))
		return
	}
	object, err := unwrapObjectEnvelope(result, "project_info", "data")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := validateImportedObjectIdentity(true, "project data source", object, "project_id", projectID); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	table, err := parseBudgetTable(object)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	budgetID, budgetPresence, err := budgetTableID(object, table)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if budgetPresence == apiValuePresent {
		data.BudgetID = types.StringValue(budgetID)
	} else {
		data.BudgetID = types.StringNull()
	}
	for _, field := range []struct {
		name   string
		target *types.String
	}{
		{"project_alias", &data.ProjectAlias}, {"description", &data.Description}, {"team_id", &data.TeamID}, {"created_at", &data.CreatedAt}, {"updated_at", &data.UpdatedAt}, {"created_by", &data.CreatedBy}, {"updated_by", &data.UpdatedBy},
	} {
		if err := updateNullableString(field.target, object, field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	models, modelsPresence, err := stringListFromAPI(object, "models")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if modelsPresence == apiValuePresent {
		data.Models = models
	} else {
		data.Models = types.ListNull(types.StringType)
	}
	if blocked, presence, err := apiValueAt(object, "blocked"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	} else if presence == apiValuePresent {
		value, ok := blocked.(bool)
		if !ok {
			resp.Diagnostics.AddError("Invalid API Response", "invalid response field blocked: expected a boolean")
			return
		}
		data.Blocked = types.BoolValue(value)
	} else {
		data.Blocked = types.BoolNull()
	}
	if err := updateFloat64FromAPI(&data.Spend, object, true, true, "spend"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget}, {"soft_budget", &data.SoftBudget},
	} {
		if err := updateBudgetFloat64(field.target, table, true, true, field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit}, {"rpm_limit", &data.RPMLimit}, {"max_parallel_requests", &data.MaxParallelRequests},
	} {
		if err := updateBudgetInt64(field.target, table, true, true, field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	if err := updateBudgetDuration(&data.BudgetDuration, table, true, true); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	metadata, metadataPresence, err := stringMapFromAPI(object, "metadata", "tags", "model_rpm_limit", "model_tpm_limit")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if metadataPresence == apiValuePresent {
		data.Metadata = metadata
	} else {
		data.Metadata = types.MapNull(types.StringType)
	}
	if metadataObject, ok := object["metadata"].(map[string]interface{}); ok {
		tags, presence, err := stringListFromAPI(metadataObject, "tags")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", fmt.Sprintf("invalid response field metadata.tags: %s", err))
			return
		}
		if presence == apiValuePresent {
			data.Tags = tags
		} else {
			data.Tags = types.ListNull(types.StringType)
		}
	} else {
		data.Tags = types.ListNull(types.StringType)
	}
	if err := updateInt64MapFromAPI(&data.ModelRPMLimit, object, true, true, "metadata", "model_rpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if err := updateInt64MapFromAPI(&data.ModelTPMLimit, object, true, true, "metadata", "model_tpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
