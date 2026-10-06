package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func TestAddMCPMutationErrorExplainsStdioGate(t *testing.T) {
	rejected := &APIError{StatusCode: 422, DetailOmitted: true}
	for _, test := range []struct {
		name    string
		err     error
		request map[string]interface{}
		want    string
	}{
		{"stdio rejected", rejected, map[string]interface{}{"transport": "stdio"}, "stdio MCP Servers Disabled"},
		{"http rejected", rejected, map[string]interface{}{"transport": "http"}, "Client Error"},
		{"stdio other status", &APIError{StatusCode: 500}, map[string]interface{}{"transport": "stdio"}, "Client Error"},
		{"update without transport", rejected, map[string]interface{}{"description": "x"}, "Client Error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var diagnostics diag.Diagnostics
			addMCPMutationError(&diagnostics, test.err, test.request, "Client Error", "generic")
			if len(diagnostics) != 1 || diagnostics[0].Summary() != test.want {
				t.Fatalf("diagnostics=%v", diagnostics)
			}
			if test.want != "Client Error" && !strings.Contains(diagnostics[0].Detail(), "LITELLM_ENABLE_MCP_STDIO=true") {
				t.Fatalf("detail lacks remediation: %s", diagnostics[0].Detail())
			}
		})
	}
}
