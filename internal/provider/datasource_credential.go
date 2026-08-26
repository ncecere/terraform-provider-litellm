package provider

import (
	"context"
	"fmt"
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
				Description: "Canonical full JSON metadata object, including heterogeneous nested values and the number lexemes returned by LiteLLM.",
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
	lookupByModel := !data.ModelID.IsNull() && !data.ModelID.IsUnknown() && data.ModelID.ValueString() != ""
	var sample credentialProbeSample
	var probeErr error
	if lookupByModel {
		sample, probeErr = probeCredentialEndpoint(ctx, d.client, credentialByModelPath(data.ModelID.ValueString()), "")
	} else {
		sample, probeErr = probeCredentialEndpoint(ctx, d.client, credentialByNamePath(credentialName), credentialName)
	}
	if probeErr != nil || !sample.hasPresence() {
		resp.Diagnostics.AddError("Credential Data Source Read Error", "Bounded fresh-connection probes did not return a usable credential from the selected exact lookup route. Retry transient failures or reconcile LiteLLM v1.98 process-local worker caches before retrying.")
		return
	}
	if !sample.versionsMatch() {
		resp.Diagnostics.AddError("Credential Worker Convergence Uncertain", "Fresh-connection probes returned different cached credential versions from LiteLLM v1.98 workers. No arbitrary version was selected. Reload or restart workers as appropriate, verify their process-local credential caches are consistent, and retry.")
		return
	}
	remote := sample.present[0]
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
	if sample.absent != 0 {
		resp.Diagnostics.AddWarning(
			"Credential Worker Convergence Uncertain",
			"At least one fresh-connection probe returned one consistent credential version while another LiteLLM v1.98 worker returned exact 404. The data source returned that version. LiteLLM stores the durable record in its database but serves this lookup from each worker's process-local credential_list, so Terraform does not claim worker-cache or cluster-wide convergence.",
		)
	}
}
