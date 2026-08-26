package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &JWTKeyMappingDataSource{}

func NewJWTKeyMappingDataSource() datasource.DataSource { return &JWTKeyMappingDataSource{} }

type JWTKeyMappingDataSource struct{ client *Client }

type JWTKeyMappingDataSourceModel struct {
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

func (d *JWTKeyMappingDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jwt_key_mapping"
}

func jwtKeyMappingReadOnlyAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"jwt_claim_name":  schema.StringAttribute{Description: "JWT claim name.", Computed: true},
		"jwt_claim_value": schema.StringAttribute{Description: "Sensitive JWT claim value.", Computed: true, Sensitive: true},
		"description":     schema.StringAttribute{Description: "Mapping description, or null when absent.", Computed: true},
		"is_active":       schema.BoolAttribute{Description: "Whether LiteLLM uses the mapping.", Computed: true},
		"created_at":      schema.StringAttribute{Description: "Creation timestamp.", Computed: true},
		"updated_at":      schema.StringAttribute{Description: "Last-update timestamp.", Computed: true},
		"created_by":      schema.StringAttribute{Description: "Creator provenance when present.", Computed: true, Sensitive: true},
		"updated_by":      schema.StringAttribute{Description: "Updater provenance when present.", Computed: true, Sensitive: true},
	}
}

func (d *JWTKeyMappingDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := jwtKeyMappingReadOnlyAttributes()
	attributes["id"] = schema.StringAttribute{Description: "Canonical LiteLLM mapping UUID to read.", Required: true}
	resp.Schema = schema.Schema{Description: "Reads one LiteLLM JWT claim-to-virtual-key mapping by authoritative UUID. Tokens and hashes are never returned.", Attributes: attributes}
}

func (d *JWTKeyMappingDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *JWTKeyMappingDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data JWTKeyMappingDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := canonicalJWTKeyMappingID(data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid JWT Key Mapping ID", "id must be a canonical lowercase UUID.")
		return
	}
	mapping, err := readJWTKeyMapping(ctx, d.client, id)
	if err != nil {
		resp.Diagnostics.AddError("JWT Key Mapping Read Failed", "Unable to read the JWT key mapping. Response details were omitted because they may contain sensitive claim data.")
		return
	}
	setJWTKeyMappingDataSourceState(&data, mapping)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func setJWTKeyMappingDataSourceState(data *JWTKeyMappingDataSourceModel, mapping jwtKeyMappingObject) {
	data.ID, data.ClaimName, data.ClaimValue = types.StringValue(mapping.ID), types.StringValue(mapping.ClaimName), types.StringValue(mapping.ClaimValue)
	if mapping.Description == nil {
		data.Description = types.StringNull()
	} else {
		data.Description = types.StringValue(*mapping.Description)
	}
	data.IsActive = types.BoolValue(mapping.IsActive)
	data.CreatedAt, data.UpdatedAt = types.StringValue(mapping.CreatedAt), types.StringValue(mapping.UpdatedAt)
	if mapping.CreatedBy == nil {
		data.CreatedBy = types.StringNull()
	} else {
		data.CreatedBy = types.StringValue(*mapping.CreatedBy)
	}
	if mapping.UpdatedBy == nil {
		data.UpdatedBy = types.StringNull()
	} else {
		data.UpdatedBy = types.StringValue(*mapping.UpdatedBy)
	}
}
