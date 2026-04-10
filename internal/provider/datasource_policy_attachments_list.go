package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PolicyAttachmentsListDataSource{}

func NewPolicyAttachmentsListDataSource() datasource.DataSource {
	return &PolicyAttachmentsListDataSource{}
}

type PolicyAttachmentsListDataSource struct {
	client *Client
}

type PolicyAttachmentListItemModel struct {
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

type PolicyAttachmentsListDataSourceModel struct {
	ID          types.String                    `tfsdk:"id"`
	Attachments []PolicyAttachmentListItemModel `tfsdk:"attachments"`
	TotalCount  types.Int64                     `tfsdk:"total_count"`
}

func (d *PolicyAttachmentsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy_attachments"
}

func (d *PolicyAttachmentsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of policy attachments.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"total_count": schema.Int64Attribute{
				Description: "Total number of returned attachments.",
				Computed:    true,
			},
			"attachments": schema.ListNestedAttribute{
				Description: "List of policy attachments.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"attachment_id": schema.StringAttribute{Description: "Attachment ID.", Computed: true},
						"policy_name":   schema.StringAttribute{Description: "Name of attached policy.", Computed: true},
						"scope":         schema.StringAttribute{Description: "Attachment scope.", Computed: true},
						"teams":         schema.ListAttribute{Description: "Team patterns.", Computed: true, ElementType: types.StringType},
						"keys":          schema.ListAttribute{Description: "Key patterns.", Computed: true, ElementType: types.StringType},
						"models":        schema.ListAttribute{Description: "Model patterns.", Computed: true, ElementType: types.StringType},
						"tags":          schema.ListAttribute{Description: "Tag patterns.", Computed: true, ElementType: types.StringType},
						"created_at":    schema.StringAttribute{Description: "When created.", Computed: true},
						"updated_at":    schema.StringAttribute{Description: "When updated.", Computed: true},
						"created_by":    schema.StringAttribute{Description: "Who created it.", Computed: true},
						"updated_by":    schema.StringAttribute{Description: "Who updated it.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *PolicyAttachmentsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PolicyAttachmentsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PolicyAttachmentsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/policies/attachments/list", nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list policy attachments: %s", err))
		return
	}

	data.ID = types.StringValue("policy_attachments")

	if v, ok := result["total_count"].(float64); ok {
		data.TotalCount = types.Int64Value(int64(v))
	} else {
		data.TotalCount = types.Int64Value(0)
	}

	attachments := make([]PolicyAttachmentListItemModel, 0)
	if rows, ok := result["attachments"].([]interface{}); ok {
		for _, row := range rows {
			itemMap, ok := row.(map[string]interface{})
			if !ok {
				continue
			}

			item := PolicyAttachmentListItemModel{}

			if v, ok := itemMap["attachment_id"].(string); ok {
				item.AttachmentID = types.StringValue(v)
			}
			if v, ok := itemMap["policy_name"].(string); ok {
				item.PolicyName = types.StringValue(v)
			}
			if v, ok := itemMap["scope"].(string); ok && v != "" {
				item.Scope = types.StringValue(v)
			} else {
				item.Scope = types.StringNull()
			}

			item.Teams = toStringListValue(itemMap["teams"])
			item.Keys = toStringListValue(itemMap["keys"])
			item.Models = toStringListValue(itemMap["models"])
			item.Tags = toStringListValue(itemMap["tags"])

			if v, ok := itemMap["created_at"].(string); ok && v != "" {
				item.CreatedAt = types.StringValue(v)
			} else {
				item.CreatedAt = types.StringNull()
			}
			if v, ok := itemMap["updated_at"].(string); ok && v != "" {
				item.UpdatedAt = types.StringValue(v)
			} else {
				item.UpdatedAt = types.StringNull()
			}
			if v, ok := itemMap["created_by"].(string); ok && v != "" {
				item.CreatedBy = types.StringValue(v)
			} else {
				item.CreatedBy = types.StringNull()
			}
			if v, ok := itemMap["updated_by"].(string); ok && v != "" {
				item.UpdatedBy = types.StringValue(v)
			} else {
				item.UpdatedBy = types.StringNull()
			}

			attachments = append(attachments, item)
		}
	}

	data.Attachments = attachments

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func toStringListValue(raw interface{}) types.List {
	values := make([]attr.Value, 0)

	if arr, ok := raw.([]interface{}); ok {
		for _, item := range arr {
			if str, ok := item.(string); ok {
				values = append(values, types.StringValue(str))
			}
		}
	}

	list, _ := types.ListValue(types.StringType, values)
	return list
}
