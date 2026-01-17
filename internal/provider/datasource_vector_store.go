package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &VectorStoreDataSource{}

func NewVectorStoreDataSource() datasource.DataSource {
	return &VectorStoreDataSource{}
}

type VectorStoreDataSource struct {
	client *Client
}

type VectorStoreDataSourceModel struct {
	ID                     types.String `tfsdk:"id"`
	VectorStoreID          types.String `tfsdk:"vector_store_id"`
	VectorStoreName        types.String `tfsdk:"vector_store_name"`
	CustomLLMProvider      types.String `tfsdk:"custom_llm_provider"`
	VectorStoreDescription types.String `tfsdk:"vector_store_description"`
	VectorStoreMetadata    types.Map    `tfsdk:"vector_store_metadata"`
	LiteLLMCredentialName  types.String `tfsdk:"litellm_credential_name"`
	LiteLLMParams          types.Map    `tfsdk:"litellm_params"`
	CreatedAt              types.String `tfsdk:"created_at"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
}

func (d *VectorStoreDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vector_store"
}

func (d *VectorStoreDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM vector store.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this vector store (same as vector_store_id).",
				Computed:    true,
			},
			"vector_store_id": schema.StringAttribute{
				Description: "Unique identifier for the vector store to retrieve.",
				Required:    true,
			},
			"vector_store_name": schema.StringAttribute{
				Description: "Name of the vector store.",
				Computed:    true,
			},
			"custom_llm_provider": schema.StringAttribute{
				Description: "Custom LLM provider for the vector store.",
				Computed:    true,
			},
			"vector_store_description": schema.StringAttribute{
				Description: "Description of the vector store.",
				Computed:    true,
			},
			"vector_store_metadata": schema.MapAttribute{
				Description: "Metadata associated with the vector store.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"litellm_credential_name": schema.StringAttribute{
				Description: "Name of the LiteLLM credential used.",
				Computed:    true,
			},
			"litellm_params": schema.MapAttribute{
				Description: "Additional LiteLLM parameters.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the vector store was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the vector store was last updated.",
				Computed:    true,
			},
		},
	}
}

func (d *VectorStoreDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *VectorStoreDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data VectorStoreDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vectorStoreID := data.VectorStoreID.ValueString()

	infoReq := map[string]interface{}{
		"vector_store_id": vectorStoreID,
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "POST", "/vector_store/info", infoReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read vector store '%s': %s", vectorStoreID, err))
		return
	}

	// Update fields from response
	if vsID, ok := result["vector_store_id"].(string); ok {
		data.VectorStoreID = types.StringValue(vsID)
		data.ID = types.StringValue(vsID)
	}
	if vsName, ok := result["vector_store_name"].(string); ok {
		data.VectorStoreName = types.StringValue(vsName)
	}
	if provider, ok := result["custom_llm_provider"].(string); ok {
		data.CustomLLMProvider = types.StringValue(provider)
	}
	if desc, ok := result["vector_store_description"].(string); ok {
		data.VectorStoreDescription = types.StringValue(desc)
	}
	if credName, ok := result["litellm_credential_name"].(string); ok {
		data.LiteLLMCredentialName = types.StringValue(credName)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if updatedAt, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}

	// Handle vector_store_metadata
	if metadata, ok := result["vector_store_metadata"].(map[string]interface{}); ok {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			if str, ok := v.(string); ok {
				metaMap[k] = types.StringValue(str)
			}
		}
		data.VectorStoreMetadata, _ = types.MapValue(types.StringType, metaMap)
	} else {
		data.VectorStoreMetadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle litellm_params
	if params, ok := result["litellm_params"].(map[string]interface{}); ok {
		paramsMap := make(map[string]attr.Value)
		for k, v := range params {
			if str, ok := v.(string); ok {
				paramsMap[k] = types.StringValue(str)
			}
		}
		data.LiteLLMParams, _ = types.MapValue(types.StringType, paramsMap)
	} else {
		data.LiteLLMParams, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
