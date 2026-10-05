package provider

import (
	"bytes"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// liteLLMNotPremiumUserMarker is the stable prefix of LiteLLM's
// CommonProxyErrors.not_premium_user message, which every premium-feature
// gate returns with HTTP 403.
var liteLLMNotPremiumUserMarker = []byte("You must be a LiteLLM Enterprise user to use this feature")

// classifyEnterpriseLicenseRequiredBody inspects a 403 body once, at the
// client boundary, so callers never need the raw body.
func classifyEnterpriseLicenseRequiredBody(body []byte) bool {
	return bytes.Contains(body, liteLLMNotPremiumUserMarker)
}

// isEnterpriseLicenseRequiredError reports LiteLLM's premium-feature 403.
func isEnterpriseLicenseRequiredError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 403 && apiErr.enterpriseLicenseRequired
}

const enterpriseLicenseRequiredSummary = "LiteLLM Enterprise License Required"

// enterpriseLicenseRequiredDetail explains the 403 without echoing the
// response. It never implies absence: Terraform state is left unchanged.
const enterpriseLicenseRequiredDetail = "LiteLLM returned HTTP 403 because this feature requires a LiteLLM Enterprise license. From LiteLLM 1.102.0 every organization endpoint, including reads, is license-gated; project endpoints were already gated. Set LITELLM_LICENSE on the proxy, or remove these resources from configuration. Terraform state was not changed."

// addLicenseAwareError adds the enterprise-license diagnostic for LiteLLM's
// premium-feature 403 and the caller's diagnostic for every other failure.
func addLicenseAwareError(diagnostics *diag.Diagnostics, err error, summary, detail string) {
	if isEnterpriseLicenseRequiredError(err) {
		diagnostics.AddError(enterpriseLicenseRequiredSummary, enterpriseLicenseRequiredDetail)
		return
	}
	diagnostics.AddError(summary, detail)
}
