package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// liteLLMOrganizationLicenseBody is LiteLLM 1.104.0's organization gate body.
const liteLLMOrganizationLicenseBody = `{"detail":{"error":"Organizations are only available for LiteLLM Enterprise users. You must be a LiteLLM Enterprise user to use this feature. If you have a license please set ` + "`LITELLM_LICENSE`" + ` in your env."}}`

func TestEnterpriseLicenseRequiredClassification(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"organization license gate", http.StatusForbidden, liteLLMOrganizationLicenseBody, true},
		{"organization access check", http.StatusForbidden, `{"detail":"You do not have access to this organization"}`, false},
		{"license text on another status", http.StatusBadRequest, liteLLMOrganizationLicenseBody, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client := &Client{APIBase: server.URL, APIKey: "sk-test", HTTPClient: server.Client()}
			err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/organization/list", nil, nil)
			if got := isEnterpriseLicenseRequiredError(err); got != test.want {
				t.Fatalf("isEnterpriseLicenseRequiredError=%t want %t (err=%v)", got, test.want, err)
			}
			if IsNotFoundError(err) {
				t.Fatal("a 403 must never be treated as absence")
			}
		})
	}
}

func TestOrganizationLicenseGateDiagnosticsRetainState(t *testing.T) {
	ctx := context.Background()
	gated := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if gated {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(liteLLMOrganizationLicenseBody))
			return
		}
		_, _ = writer.Write([]byte(`{"organization_id":"org-license","organization_alias":"licensed","models":[],"spend":0,"litellm_budget_table":null,"metadata":{},"members":[],"teams":[]}`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_organization", ID: "org-license"})
	if err != nil || len(imported.ImportedResources) != 1 {
		t.Fatalf("import err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	gated = true
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil {
		t.Fatal(err)
	}
	text := agentProtocolDiagnosticsText(read.Diagnostics)
	if !strings.Contains(text, enterpriseLicenseRequiredSummary) || !strings.Contains(text, "LITELLM_LICENSE") {
		t.Fatalf("organization read diagnostic lacks license guidance: %s", text)
	}
	schema := schemas.ResourceSchemas["litellm_organization"]
	if value, _ := read.NewState.Unmarshal(schema.ValueType()); value.IsNull() {
		t.Fatal("license 403 removed the organization from state")
	}

	dataSchema := schemas.DataSourceSchemas["litellm_organizations"]
	config := accessGroupProtocolDynamicValue(t, dataSchema, tftypes.NewValue(dataSchema.ValueType(), nil))
	listed, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_organizations", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if text := agentProtocolDiagnosticsText(listed.Diagnostics); !strings.Contains(text, enterpriseLicenseRequiredSummary) {
		t.Fatalf("organization list diagnostic lacks license guidance: %s", text)
	}
}
