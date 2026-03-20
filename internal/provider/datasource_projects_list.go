package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ProjectsListDataSource{}

func NewProjectsListDataSource() datasource.DataSource {
	return &ProjectsListDataSource{}
}

type ProjectsListDataSource struct {
	client *Client
}

type ProjectsListDataSourceModel struct {
	ID       types.String           `tfsdk:"id"`
	Projects []ProjectListItemModel `tfsdk:"projects"`
}

type ProjectListItemModel struct {
	ProjectID    types.String  `tfsdk:"project_id"`
	ProjectAlias types.String  `tfsdk:"project_alias"`
	Description  types.String  `tfsdk:"description"`
	TeamID       types.String  `tfsdk:"team_id"`
	Blocked      types.Bool    `tfsdk:"blocked"`
	Spend        types.Float64 `tfsdk:"spend"`
	CreatedAt    types.String  `tfsdk:"created_at"`
	UpdatedAt    types.String  `tfsdk:"updated_at"`
	CreatedBy    types.String  `tfsdk:"created_by"`
	UpdatedBy    types.String  `tfsdk:"updated_by"`
}

func (d *ProjectsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_projects"
}

func (d *ProjectsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM Projects.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"projects": schema.ListNestedAttribute{
				Description: "List of projects.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"project_id": schema.StringAttribute{
							Description: "The unique project ID.",
							Computed:    true,
						},
						"project_alias": schema.StringAttribute{
							Description: "Human-friendly name for the project.",
							Computed:    true,
						},
						"description": schema.StringAttribute{
							Description: "Description of the project.",
							Computed:    true,
						},
						"team_id": schema.StringAttribute{
							Description: "The team ID this project belongs to.",
							Computed:    true,
						},
						"blocked": schema.BoolAttribute{
							Description: "Whether the project is blocked.",
							Computed:    true,
						},
						"spend": schema.Float64Attribute{
							Description: "Total spend for this project.",
							Computed:    true,
						},
						"created_at": schema.StringAttribute{
							Description: "Timestamp when the project was created.",
							Computed:    true,
						},
						"updated_at": schema.StringAttribute{
							Description: "Timestamp when the project was last updated.",
							Computed:    true,
						},
						"created_by": schema.StringAttribute{
							Description: "User who created the project.",
							Computed:    true,
						},
						"updated_by": schema.StringAttribute{
							Description: "User who last updated the project.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *ProjectsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func (d *ProjectsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ProjectsListDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var result []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/project/list", nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list projects: %s", err))
		return
	}

	projects := make([]ProjectListItemModel, 0, len(result))
	for _, item := range result {
		project := ProjectListItemModel{}
		if v, ok := item["project_id"].(string); ok {
			project.ProjectID = types.StringValue(v)
		}
		if v, ok := item["project_alias"].(string); ok {
			project.ProjectAlias = types.StringValue(v)
		}
		if v, ok := item["description"].(string); ok {
			project.Description = types.StringValue(v)
		}
		if v, ok := item["team_id"].(string); ok {
			project.TeamID = types.StringValue(v)
		}
		if v, ok := item["blocked"].(bool); ok {
			project.Blocked = types.BoolValue(v)
		}
		if v, ok := item["spend"].(float64); ok {
			project.Spend = types.Float64Value(v)
		}
		if v, ok := item["created_at"].(string); ok {
			project.CreatedAt = types.StringValue(v)
		}
		if v, ok := item["updated_at"].(string); ok {
			project.UpdatedAt = types.StringValue(v)
		}
		if v, ok := item["created_by"].(string); ok {
			project.CreatedBy = types.StringValue(v)
		}
		if v, ok := item["updated_by"].(string); ok {
			project.UpdatedBy = types.StringValue(v)
		}
		projects = append(projects, project)
	}

	data.ID = types.StringValue("projects-list")
	data.Projects = projects

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
