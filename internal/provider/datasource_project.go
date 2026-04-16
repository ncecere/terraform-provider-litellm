package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ProjectDataSource{}

func NewProjectDataSource() datasource.DataSource {
	return &ProjectDataSource{}
}

type ProjectDataSource struct {
	client *Client
}

type ProjectDataSourceModel struct {
	ID            types.String  `tfsdk:"id"`
	ProjectAlias  types.String  `tfsdk:"project_alias"`
	Description   types.String  `tfsdk:"description"`
	TeamID        types.String  `tfsdk:"team_id"`
	Models        types.List    `tfsdk:"models"`
	Metadata      types.Map     `tfsdk:"metadata"`
	Tags          types.List    `tfsdk:"tags"`
	Blocked       types.Bool    `tfsdk:"blocked"`
	Spend         types.Float64 `tfsdk:"spend"`
	ModelRPMLimit types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit types.Map     `tfsdk:"model_tpm_limit"`
	CreatedAt     types.String  `tfsdk:"created_at"`
	UpdatedAt     types.String  `tfsdk:"updated_at"`
	CreatedBy     types.String  `tfsdk:"created_by"`
	UpdatedBy     types.String  `tfsdk:"updated_by"`
}

func (d *ProjectDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *ProjectDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM Project.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The project ID to look up.",
				Required:    true,
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
			"models": schema.ListAttribute{
				Description: "Models the project can access.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"metadata": schema.MapAttribute{
				Description: "Project metadata.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"tags": schema.ListAttribute{
				Description: "Tags associated with the project.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the project is blocked.",
				Computed:    true,
			},
			"spend": schema.Float64Attribute{
				Description: "Total spend for this project.",
				Computed:    true,
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits.",
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits.",
				Computed:    true,
				ElementType: types.Int64Type,
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
	}
}

func (d *ProjectDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ProjectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ProjectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/project/info?project_id=%s", url.QueryEscape(data.ID.ValueString()))

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read project: %s", err))
		return
	}

	if v, ok := result["project_id"].(string); ok {
		data.ID = types.StringValue(v)
	}
	if v, ok := result["project_alias"].(string); ok {
		data.ProjectAlias = types.StringValue(v)
	}
	if v, ok := result["description"].(string); ok {
		data.Description = types.StringValue(v)
	}
	if v, ok := result["team_id"].(string); ok {
		data.TeamID = types.StringValue(v)
	}
	if v, ok := result["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(v)
	}
	if v, ok := result["spend"].(float64); ok {
		data.Spend = types.Float64Value(v)
	}
	if v, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(v)
	}
	if v, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(v)
	}
	if v, ok := result["created_by"].(string); ok {
		data.CreatedBy = types.StringValue(v)
	}
	if v, ok := result["updated_by"].(string); ok {
		data.UpdatedBy = types.StringValue(v)
	}

	// Models
	if models, ok := result["models"].([]interface{}); ok && len(models) > 0 {
		vals := make([]attr.Value, 0, len(models))
		for _, m := range models {
			if s, ok := m.(string); ok {
				vals = append(vals, types.StringValue(s))
			}
		}
		data.Models, _ = types.ListValue(types.StringType, vals)
	} else {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Tags
	if tags, ok := result["tags"].([]interface{}); ok && len(tags) > 0 {
		vals := make([]attr.Value, 0, len(tags))
		for _, t := range tags {
			if s, ok := t.(string); ok {
				vals = append(vals, types.StringValue(s))
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, vals)
	} else {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Metadata
	if metadata, ok := result["metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			metaMap[k] = types.StringValue(valueToJSONString(v))
		}
		data.Metadata, _ = types.MapValue(types.StringType, metaMap)
	} else {
		data.Metadata = types.MapNull(types.StringType)
	}

	// Model RPM/TPM limits
	if mrpm, ok := result["model_rpm_limit"].(map[string]interface{}); ok && len(mrpm) > 0 {
		rpmMap := make(map[string]attr.Value)
		for k, v := range mrpm {
			if num, ok := v.(float64); ok {
				rpmMap[k] = types.Int64Value(int64(num))
			}
		}
		data.ModelRPMLimit, _ = types.MapValue(types.Int64Type, rpmMap)
	} else {
		data.ModelRPMLimit = types.MapNull(types.Int64Type)
	}
	if mtpm, ok := result["model_tpm_limit"].(map[string]interface{}); ok && len(mtpm) > 0 {
		tpmMap := make(map[string]attr.Value)
		for k, v := range mtpm {
			if num, ok := v.(float64); ok {
				tpmMap[k] = types.Int64Value(int64(num))
			}
		}
		data.ModelTPMLimit, _ = types.MapValue(types.Int64Type, tpmMap)
	} else {
		data.ModelTPMLimit = types.MapNull(types.Int64Type)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
