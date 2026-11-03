package provider

import "testing"

// A pretty-printed GCP service-account key file starts with "{\n" and ends
// with "}\n"; LiteLLM masks it as "{\n****}\n". Releases before 2.2.3 did not
// recognize that mask, stored it in state, and failed with "Provider produced
// inconsistent result after apply".
func TestCredentialMaskRecognizesMultilineValues(t *testing.T) {
	pretty := "{\n  \"type\": \"service_account\",\n  \"private_key\": \"-----BEGIN PRIVATE KEY-----\\nABC\\n-----END PRIVATE KEY-----\\n\"\n}\n"
	mask := maskLiteLLMCredentialString(pretty)
	if mask != "{\n****}\n" {
		t.Fatalf("mask = %q", mask)
	}
	for _, value := range []string{mask, "{\n****\n}", "sk****ef", "*****"} {
		if !isLiteLLMCredentialMask(value) {
			t.Fatalf("%q was not recognized as a LiteLLM mask", value)
		}
	}
	for _, value := range []string{pretty, "{\n*****}\n", "ab***cd", "plain"} {
		if isLiteLLMCredentialMask(value) {
			t.Fatalf("%q was wrongly recognized as a LiteLLM mask", value)
		}
	}

	ownership := credentialOwnershipForObject(map[string]interface{}{"vertex_credentials": pretty})
	projected, err := projectCredentialObject(map[string]interface{}{"vertex_credentials": mask}, map[string]interface{}{"vertex_credentials": pretty}, ownership, true)
	if err != nil || projected["vertex_credentials"] != pretty {
		t.Fatalf("configured multi-line value not restored: %#v err=%v", projected, err)
	}

	// Recovery: state written by 2.2.1/2.2.2 holds the mask itself.
	projected, err = projectCredentialObject(map[string]interface{}{"vertex_credentials": mask}, map[string]interface{}{"vertex_credentials": mask}, ownership, true)
	if err != nil || projected["vertex_credentials"] != mask {
		t.Fatalf("stored mask equal to the remote mask was not kept: %#v err=%v", projected, err)
	}
	if err := validateCredentialOwnedAtomicPreconditions(map[string]interface{}{"vertex_credentials": mask}, map[string]interface{}{"vertex_credentials": mask}, ownership, true); err != nil {
		t.Fatalf("PATCH precondition rejected an unchanged stored mask: %v", err)
	}
	// A different remote mask (changed outside Terraform) still fails closed.
	other := "[\n****]\n"
	if _, err := projectCredentialObject(map[string]interface{}{"vertex_credentials": other}, map[string]interface{}{"vertex_credentials": mask}, ownership, true); err == nil {
		t.Fatal("a different remote mask was accepted against a stored mask")
	}
	if err := validateCredentialOwnedAtomicPreconditions(map[string]interface{}{"vertex_credentials": other}, map[string]interface{}{"vertex_credentials": mask}, ownership, true); err == nil {
		t.Fatal("PATCH precondition accepted a different remote mask")
	}
}
