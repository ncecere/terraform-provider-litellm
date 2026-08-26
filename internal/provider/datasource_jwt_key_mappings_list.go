package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &JWTKeyMappingsListDataSource{}

func NewJWTKeyMappingsListDataSource() datasource.DataSource { return &JWTKeyMappingsListDataSource{} }

type JWTKeyMappingsListDataSource struct{ client *Client }

type JWTKeyMappingsListDataSourceModel struct {
	ID       types.String                 `tfsdk:"id"`
	Mappings []JWTKeyMappingListItemModel `tfsdk:"mappings"`
}

type JWTKeyMappingListItemModel struct {
	ID          types.String `tfsdk:"id"`
	ClaimName   types.String `tfsdk:"jwt_claim_name"`
	ClaimValue  types.String `tfsdk:"jwt_claim_value"`
	Description types.String `tfsdk:"description"`
	IsActive    types.Bool   `tfsdk:"is_active"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
	CreatedBy   types.String `tfsdk:"created_by"`
	UpdatedBy   types.String `tfsdk:"updated_by"`
}

func (d *JWTKeyMappingsListDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jwt_key_mappings"
}

func (d *JWTKeyMappingsListDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Reads every LiteLLM JWT key mapping using two bounded full v1.98 pagination scans, requiring identical observable rows before sorting by UUID.", Attributes: map[string]schema.Attribute{
		"id": schema.StringAttribute{Description: "Stable data source identifier.", Computed: true},
		"mappings": schema.ListNestedAttribute{Description: "Complete mapping inventory in ascending UUID order.", Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Description: "Authoritative mapping UUID.", Computed: true},
			"jwt_claim_name":  schema.StringAttribute{Description: "JWT claim name.", Computed: true},
			"jwt_claim_value": schema.StringAttribute{Description: "Sensitive JWT claim value.", Computed: true, Sensitive: true},
			"description":     schema.StringAttribute{Description: "Description, or null when absent.", Computed: true},
			"is_active":       schema.BoolAttribute{Description: "Whether LiteLLM uses the mapping.", Computed: true},
			"created_at":      schema.StringAttribute{Description: "Creation timestamp.", Computed: true},
			"updated_at":      schema.StringAttribute{Description: "Last-update timestamp.", Computed: true},
			"created_by":      schema.StringAttribute{Description: "Creator provenance when present.", Computed: true, Sensitive: true},
			"updated_by":      schema.StringAttribute{Description: "Updater provenance when present.", Computed: true, Sensitive: true},
		}}},
	}}
}

func (d *JWTKeyMappingsListDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *JWTKeyMappingsListDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	mappings, err := listJWTKeyMappings(ctx, d.client)
	if err != nil {
		resp.Diagnostics.AddError("JWT Key Mapping List Failed", "Unable to read a complete stable mapping inventory. Response details were omitted because they may contain sensitive claim data.")
		return
	}
	data := JWTKeyMappingsListDataSourceModel{ID: types.StringValue("jwt-key-mappings"), Mappings: make([]JWTKeyMappingListItemModel, 0, len(mappings))}
	for _, mapping := range mappings {
		item := JWTKeyMappingListItemModel{ID: types.StringValue(mapping.ID), ClaimName: types.StringValue(mapping.ClaimName), ClaimValue: types.StringValue(mapping.ClaimValue), IsActive: types.BoolValue(mapping.IsActive), CreatedAt: types.StringValue(mapping.CreatedAt), UpdatedAt: types.StringValue(mapping.UpdatedAt)}
		if mapping.Description == nil {
			item.Description = types.StringNull()
		} else {
			item.Description = types.StringValue(*mapping.Description)
		}
		if mapping.CreatedBy == nil {
			item.CreatedBy = types.StringNull()
		} else {
			item.CreatedBy = types.StringValue(*mapping.CreatedBy)
		}
		if mapping.UpdatedBy == nil {
			item.UpdatedBy = types.StringNull()
		} else {
			item.UpdatedBy = types.StringValue(*mapping.UpdatedBy)
		}
		data.Mappings = append(data.Mappings, item)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
