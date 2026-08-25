# litellm_credential - Real source-free import safety fixture
# The acceptance runner creates seed[0], removes only its state, then imports
# the exact slash/percent/Unicode name into imported[0] with phase="imported".

variable "credential_import_phase" {
  type    = string
  default = "seed"
}

locals {
  credential_import_name = "test/cred%import-雪"
}

resource "litellm_credential" "seed" {
  count = var.credential_import_phase == "seed" ? 1 : 0

  credential_name = local.credential_import_name
  credential_info = {
    owner = "acceptance-import"
  }
  credential_values = {
    api_key = "sk-fake-credential-import-key"
  }
}

resource "litellm_credential" "imported" {
  count = var.credential_import_phase == "imported" ? 1 : 0

  credential_name = local.credential_import_name
  # Deliberately source-free: masked remote values remain absent and unowned.
}

output "credential_import_id" {
  value = var.credential_import_phase == "imported" ? litellm_credential.imported[0].id : litellm_credential.seed[0].id
}
