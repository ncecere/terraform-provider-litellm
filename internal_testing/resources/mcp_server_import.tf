variable "mcp_import_phase" {
  description = "Selects the seed or imported MCP server address during the import convergence fixture."
  type        = string
  default     = "seeded"

  validation {
    condition     = contains(["seeded", "imported"], var.mcp_import_phase)
    error_message = "mcp_import_phase must be seeded or imported."
  }
}

resource "litellm_mcp_server" "mcp_import_seed" {
  count = var.mcp_import_phase == "seeded" ? 1 : 0

  server_name = "test_mcp_import_projection"
  url         = "https://example.com/mcp-import-projection"
  transport   = "http"
}

resource "litellm_mcp_server" "mcp_imported" {
  count = var.mcp_import_phase == "imported" ? 1 : 0

  server_name = "test_mcp_import_projection"
  url         = "https://example.com/mcp-import-projection"
  transport   = "http"
}

output "mcp_import_seed_id" {
  value = var.mcp_import_phase == "seeded" ? litellm_mcp_server.mcp_import_seed[0].id : null
}
