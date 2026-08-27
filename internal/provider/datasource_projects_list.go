package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ProjectsListDataSource{}

func NewProjectsListDataSource() datasource.DataSource { return &ProjectsListDataSource{} }

type ProjectsListDataSource struct{ client *Client }

type ProjectsListDataSourceModel struct {
	ID       types.String           `tfsdk:"id"`
	Projects []ProjectListItemModel `tfsdk:"projects"`
}

type ProjectListItemModel struct {
	ProjectID           types.String  `tfsdk:"project_id"`
	ProjectAlias        types.String  `tfsdk:"project_alias"`
	Description         types.String  `tfsdk:"description"`
	TeamID              types.String  `tfsdk:"team_id"`
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

func (d *ProjectsListDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_projects"
}

func (d *ProjectsListDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"project_id":            schema.StringAttribute{Description: "Project ID.", Computed: true},
		"project_alias":         schema.StringAttribute{Description: "Project alias.", Computed: true},
		"description":           schema.StringAttribute{Description: "Project description.", Computed: true},
		"team_id":               schema.StringAttribute{Description: "Parent team ID.", Computed: true},
		"blocked":               schema.BoolAttribute{Description: "Whether the project is blocked.", Computed: true},
		"spend":                 schema.Float64Attribute{Description: "Project spend.", Computed: true},
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
	}
	resp.Schema = schema.Schema{Description: "Fetches LiteLLM projects with authoritative nested budget inventories.", Attributes: map[string]schema.Attribute{
		"id":       schema.StringAttribute{Description: "Stable data source identifier.", Computed: true},
		"projects": schema.ListNestedAttribute{Description: "Projects.", Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: attributes}},
	}}
}

func (d *ProjectsListDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ProjectsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ProjectsListDataSourceModel
	result, err := fetchTopLevelListObjects(ctx, d.client, "/project/list", "project item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list projects: %s", err))
		return
	}
	projects := make([]ProjectListItemModel, 0, len(result))
	seen := make(map[string]struct{}, len(result))
	for _, object := range result {
		identity, err := dataSourceRequiredStringAt(object, "project_id")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", "/project/list returned a project object without a canonical project_id")
			return
		}
		projectID := identity.ValueString()
		if err := dataSourceListIdentity(seen, projectID, "/project/list", "project_id"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		table, err := parseBudgetTable(object)
		if err == nil {
			err = validateDataSourceBudgetTableNumbers(table)
		}
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		project := ProjectListItemModel{ProjectID: identity}
		for _, field := range []struct {
			name   string
			target *types.String
		}{
			{"project_alias", &project.ProjectAlias}, {"description", &project.Description}, {"team_id", &project.TeamID}, {"created_at", &project.CreatedAt}, {"updated_at", &project.UpdatedAt}, {"created_by", &project.CreatedBy}, {"updated_by", &project.UpdatedBy},
		} {
			if err := updateNullableString(field.target, object, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
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
			project.Blocked = types.BoolValue(value)
		} else {
			project.Blocked = types.BoolNull()
		}
		project.Spend, err = dataSourceNullableFloat64At(object, "spend")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		budgetID, presence, err := budgetTableID(object, table)
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if presence == apiValuePresent {
			project.BudgetID = types.StringValue(budgetID)
		} else {
			project.BudgetID = types.StringNull()
		}
		for _, field := range []struct {
			name   string
			target *types.Float64
		}{
			{"max_budget", &project.MaxBudget}, {"soft_budget", &project.SoftBudget},
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
			{"tpm_limit", &project.TPMLimit}, {"rpm_limit", &project.RPMLimit}, {"max_parallel_requests", &project.MaxParallelRequests},
		} {
			if err := updateBudgetInt64(field.target, table, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		if err := updateBudgetDuration(&project.BudgetDuration, table, true, true); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		project.ModelRPMLimit, err = dataSourceNullableInt64MapAt(object, "metadata", "model_rpm_limit")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		project.ModelTPMLimit, err = dataSourceNullableInt64MapAt(object, "metadata", "model_tpm_limit")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		projects = append(projects, project)
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].ProjectID.ValueString() < projects[j].ProjectID.ValueString() })
	data.ID = types.StringValue("projects-list")
	data.Projects = projects
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
