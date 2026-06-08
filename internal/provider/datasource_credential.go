package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &CredentialDataSource{}

func NewCredentialDataSource() datasource.DataSource {
	return &CredentialDataSource{}
}

type CredentialDataSource struct {
	client *Client
}

type CredentialDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	CredentialName types.String `tfsdk:"credential_name"`
	ModelID        types.String `tfsdk:"model_id"`
	CredentialInfo types.Map    `tfsdk:"credential_info"`
}

func (d *CredentialDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential"
}

func (d *CredentialDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM credential.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this credential (same as credential_name).",
				Computed:    true,
			},
			"credential_name": schema.StringAttribute{
				Description: "Name of the credential to retrieve.",
				Required:    true,
			},
			"model_id": schema.StringAttribute{
				Description: "Model ID associated with this credential.",
				Optional:    true,
			},
			"credential_info": schema.MapAttribute{
				Description: "Additional information about the credential.",
				Computed:    true,
				ElementType: types.StringType,
			},
			// Note: credential_values are not exposed in data sources for security reasons
		},
	}
}

func (d *CredentialDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CredentialDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CredentialDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	credentialName := data.CredentialName.ValueString()
	endpoint := fmt.Sprintf("/credentials/by_name/%s", credentialName)
	if !data.ModelID.IsNull() && data.ModelID.ValueString() != "" {
		endpoint += fmt.Sprintf("?model_id=%s", data.ModelID.ValueString())
	}

	var result map[string]interface{}
	if err := readCredentialDataSourceWithRetry(ctx, d.client, endpoint, &result, 8); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read credential '%s': %s", credentialName, err))
		return
	}

	// Update fields from response
	if credName, ok := result["credential_name"].(string); ok {
		data.CredentialName = types.StringValue(credName)
		data.ID = types.StringValue(credName)
	}

	// Handle credential_info
	if credInfo, ok := result["credential_info"].(map[string]interface{}); ok {
		infoMap := make(map[string]attr.Value)
		for k, v := range credInfo {
			if str, ok := v.(string); ok {
				infoMap[k] = types.StringValue(str)
			}
		}
		data.CredentialInfo, _ = types.MapValue(types.StringType, infoMap)
	} else {
		// Set empty map if no credential_info
		data.CredentialInfo, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Note: We don't expose credential_values in data sources for security reasons

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func readCredentialDataSourceWithRetry(ctx context.Context, client *Client, endpoint string, result *map[string]interface{}, maxRetries int) error {
	var err error
	delay := 1 * time.Second
	maxDelay := 10 * time.Second

	for i := 0; i < maxRetries; i++ {
		err = client.DoRequestWithResponse(ctx, "GET", endpoint, nil, result)
		if err == nil {
			return nil
		}

		if !IsNotFoundError(err) {
			return err
		}

		if i < maxRetries-1 {
			time.Sleep(delay)
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	return err
}
