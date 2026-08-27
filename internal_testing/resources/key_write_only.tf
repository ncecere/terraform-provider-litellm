# Terraform/OpenTofu >=1.11 only: write-only predefined key protocol coverage.
variable "acceptance_key_wo" {
  type      = string
  sensitive = true
  ephemeral = true
  default   = "issue210-write-only-placeholder-128-bits"
}

resource "litellm_key" "write_only" {
  key_wo         = var.acceptance_key_wo
  key_wo_version = "1"
  key_alias      = "acceptance-write-only-key"
}

output "key_write_only_id" {
  value = litellm_key.write_only.id
}
