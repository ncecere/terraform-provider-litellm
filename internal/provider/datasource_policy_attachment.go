package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PolicyAttachmentDataSource{}

func NewPolicyAttachmentDataSource() datasource.DataSource {
	return &PolicyAttachmentDataSource{}
}

type PolicyAttachmentDataSource struct {
	client *Client
}

type PolicyAttachmentDataSourceModel struct {
	ID           types.String `tfsdk:"id"`
	AttachmentID types.String `tfsdk:"attachment_id"`
	PolicyName   types.String `tfsdk:"policy_name"`
	Scope        types.String `tfsdk:"scope"`
	Teams        types.List   `tfsdk:"teams"`
	Keys         types.List   `tfsdk:"keys"`
	Models       types.List   `tfsdk:"models"`
	Tags         types.List   `tfsdk:"tags"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
	CreatedBy    types.String `tfsdk:"created_by"`
	UpdatedBy    types.String `tfsdk:"updated_by"`
}

func (d *PolicyAttachmentDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy_attachment"
}

func (d *PolicyAttachmentDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a policy attachment.",
		Attributes: map[string]schema.Attribute{
			"id":            schema.StringAttribute{Description: "The attachment ID.", Computed: true},
			"attachment_id": schema.StringAttribute{Description: "Attachment ID to look up.", Required: true},
			"policy_name":   schema.StringAttribute{Description: "Name of the attached policy.", Computed: true},
			"scope":         schema.StringAttribute{Description: "Attachment scope.", Computed: true},
			"teams":         schema.ListAttribute{Description: "Team patterns.", Computed: true, ElementType: types.StringType},
			"keys":          schema.ListAttribute{Description: "Key patterns.", Computed: true, ElementType: types.StringType},
			"models":        schema.ListAttribute{Description: "Model patterns.", Computed: true, ElementType: types.StringType},
			"tags":          schema.ListAttribute{Description: "Tag patterns.", Computed: true, ElementType: types.StringType},
			"created_at":    schema.StringAttribute{Description: "When the attachment was created.", Computed: true},
			"updated_at":    schema.StringAttribute{Description: "When the attachment was last updated.", Computed: true},
			"created_by":    schema.StringAttribute{Description: "Who created the attachment.", Computed: true},
			"updated_by":    schema.StringAttribute{Description: "Who last updated the attachment.", Computed: true},
		},
	}
}

func (d *PolicyAttachmentDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PolicyAttachmentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PolicyAttachmentDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/policies/attachments/%s", data.AttachmentID.ValueString())

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read policy attachment: %s", err))
		return
	}

	if v, ok := result["attachment_id"].(string); ok && v != "" {
		data.AttachmentID = types.StringValue(v)
		data.ID = types.StringValue(v)
	} else {
		data.ID = data.AttachmentID
	}
	if v, ok := result["policy_name"].(string); ok {
		data.PolicyName = types.StringValue(v)
	}
	if v, ok := result["scope"].(string); ok && v != "" {
		data.Scope = types.StringValue(v)
	} else {
		data.Scope = types.StringNull()
	}

	if values, ok := result["teams"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(values))
		for _, value := range values {
			if strValue, ok := value.(string); ok {
				list = append(list, types.StringValue(strValue))
			}
		}
		data.Teams, _ = types.ListValue(types.StringType, list)
	} else {
		data.Teams, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	if values, ok := result["keys"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(values))
		for _, value := range values {
			if strValue, ok := value.(string); ok {
				list = append(list, types.StringValue(strValue))
			}
		}
		data.Keys, _ = types.ListValue(types.StringType, list)
	} else {
		data.Keys, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	if values, ok := result["models"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(values))
		for _, value := range values {
			if strValue, ok := value.(string); ok {
				list = append(list, types.StringValue(strValue))
			}
		}
		data.Models, _ = types.ListValue(types.StringType, list)
	} else {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	if values, ok := result["tags"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(values))
		for _, value := range values {
			if strValue, ok := value.(string); ok {
				list = append(list, types.StringValue(strValue))
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, list)
	} else {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	if v, ok := result["created_at"].(string); ok && v != "" {
		data.CreatedAt = types.StringValue(v)
	} else {
		data.CreatedAt = types.StringNull()
	}
	if v, ok := result["updated_at"].(string); ok && v != "" {
		data.UpdatedAt = types.StringValue(v)
	} else {
		data.UpdatedAt = types.StringNull()
	}
	if v, ok := result["created_by"].(string); ok && v != "" {
		data.CreatedBy = types.StringValue(v)
	} else {
		data.CreatedBy = types.StringNull()
	}
	if v, ok := result["updated_by"].(string); ok && v != "" {
		data.UpdatedBy = types.StringValue(v)
	} else {
		data.UpdatedBy = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
