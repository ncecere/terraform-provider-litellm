// Package metadata contains release and compatibility metadata shared by the
// provider entrypoint, documentation contracts, and release tooling.
//
// It is internal implementation detail, not a supported Go API.
package metadata

const (
	// ProviderSource is the exact source published in the Terraform Registry.
	ProviderSource = "registry.terraform.io/ncecere/litellm"

	// ProtocolVersion is the Terraform provider protocol advertised by releases.
	ProtocolVersion = "6.0"

	// Baseline client and development versions.
	TerraformMinimum = "1.1.0"
	OpenTofuMinimum  = "1.6.0"
	GoMinimum        = "1.24.0"

	// WriteOnlyClientMinimum applies only when optional write-only attributes are
	// configured. It is deliberately not the global client minimum.
	WriteOnlyClientMinimum = "1.11.0"

	// TestedLiteLLMVersion is the exact backend release used by the acceptance
	// harness. Other backend versions are not implied by this literal.
	TestedLiteLLMVersion = "1.98.0"

	// MinimumProviderVersion is the stable lower bound for published examples.
	// It must not move merely because a later compatible 2.x release is tagged.
	MinimumProviderVersion    = "2.0.1"
	ExampleProviderConstraint = ">= 2.0.1, < 3.0.0"
	TerraformRequiredVersion  = ">= 1.1.0"
)
