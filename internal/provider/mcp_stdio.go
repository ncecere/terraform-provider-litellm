package provider

import (
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// mcpStdioDisabledDetail explains LiteLLM 1.104.0's stdio gate. The 422 body
// echoes the whole request (including env, headers, and credentials), so the
// provider never shows it and keys the message on the request instead.
const mcpStdioDisabledDetail = "LiteLLM returned HTTP 422 for a request that sets transport = \"stdio\". From LiteLLM 1.104.0 stdio MCP servers are disabled unless the proxy process sets LITELLM_ENABLE_MCP_STDIO=true in its environment (not config.yaml or the database) and is restarted. Existing stdio servers stay listed but do not start. Response details were omitted because they echo request secrets."

// addMCPMutationError reports a rejected MCP create or update, explaining the
// stdio gate when the request asked for stdio and LiteLLM answered 422.
func addMCPMutationError(diagnostics *diag.Diagnostics, err error, request map[string]interface{}, summary, detail string) {
	if transport, _ := request["transport"].(string); transport == "stdio" && IsAPIErrorStatus(err, http.StatusUnprocessableEntity) {
		diagnostics.AddError("stdio MCP Servers Disabled", mcpStdioDisabledDetail)
		return
	}
	diagnostics.AddError(summary, detail)
}
