package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
	ID                 types.String `tfsdk:"id"`
	CredentialName     types.String `tfsdk:"credential_name"`
	ModelID            types.String `tfsdk:"model_id"`
	CredentialInfo     types.Map    `tfsdk:"credential_info"`
	CredentialInfoJSON types.String `tfsdk:"credential_info_json"`
}

func (d *CredentialDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential"
}

func (d *CredentialDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves non-sensitive LiteLLM credential metadata by exact name or through the exact by-model route.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Stable data source identifier (same as the configured credential_name).",
				Computed:    true,
			},
			"credential_name": schema.StringAttribute{
				Description: "Non-empty credential name for name lookup and stable Terraform identity for model lookup.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"model_id": schema.StringAttribute{
				Description: "Optional model deployment ID selecting /credentials/by_model/{model_id}. This LiteLLM route is not path-capable, so model IDs containing slash cannot be represented safely here.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^[^/]*$`), "must not contain '/' because LiteLLM's by-model route uses one non-path URL segment"),
				},
			},
			"credential_info": schema.MapAttribute{
				Description: "Legacy computed map(string) projection of top-level string metadata. Existing type and semantics are unchanged.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"credential_info_json": schema.StringAttribute{
				Description: "Canonical full JSON metadata object, including heterogeneous nested values and exact numbers.",
				Computed:    true,
			},
		},
	}
}

func (d *CredentialDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CredentialDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CredentialDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	credentialName := data.CredentialName.ValueString()
	endpoint := credentialByNamePath(credentialName)
	lookupByModel := !data.ModelID.IsNull() && !data.ModelID.IsUnknown() && data.ModelID.ValueString() != ""
	if lookupByModel {
		endpoint = credentialByModelPath(data.ModelID.ValueString())
	}
	var result credentialAPIResponse
	if err := readCredentialDataSourceWithRetry(ctx, d.client, endpoint, &result, credentialReadAttempts); err != nil {
		resp.Diagnostics.AddError("Credential Data Source Read Error", "LiteLLM did not return a valid credential from the selected exact lookup route.")
		return
	}
	remote, err := decodeCredentialResponse(result)
	if err != nil {
		resp.Diagnostics.AddError("Credential Data Source Read Error", "LiteLLM returned a malformed credential object.")
		return
	}
	if !lookupByModel && remote.name != credentialName {
		resp.Diagnostics.AddError("Credential Data Source Read Error", "LiteLLM returned a credential identity that did not match the exact requested name.")
		return
	}
	infoMap, err := stringMapValueFromObject(remote.info)
	if err != nil {
		resp.Diagnostics.AddError("Credential Data Source Read Error", "LiteLLM metadata could not be represented by the compatibility map.")
		return
	}
	infoJSON, err := canonicalCredentialJSON(remote.info)
	if err != nil {
		resp.Diagnostics.AddError("Credential Data Source Read Error", "LiteLLM metadata could not be represented as canonical JSON.")
		return
	}

	// By-model responses synthesize an unrelated credential_name. Preserve the
	// configured name as the stable Terraform identity and public HCL value.
	data.ID = types.StringValue(credentialName)
	data.CredentialInfo = infoMap
	data.CredentialInfoJSON = types.StringValue(infoJSON)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func readCredentialDataSourceWithRetry(ctx context.Context, client *Client, endpoint string, result *credentialAPIResponse, maxRetries int) error {
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		*result = credentialAPIResponse{}
		err = client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, result)
		if err == nil {
			return nil
		}
		if !IsAPIErrorStatus(err, http.StatusNotFound) {
			return err
		}
		if attempt < maxRetries-1 {
			if waitErr := waitCredentialRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
		}
	}
	if err == nil {
		return errors.New("credential lookup failed without a response")
	}
	return err
}
