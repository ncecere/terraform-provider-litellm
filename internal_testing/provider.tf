terraform {
  required_providers {
    litellm = {
      source = "ncecere/litellm"
    }
  }
}

provider "litellm" {
  api_base = var.litellm_api_base
  api_key  = var.litellm_api_key
}
