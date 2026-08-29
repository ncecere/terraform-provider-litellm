package contract_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/nicholas-cecere/terraform-provider-litellm/internal/metadata"
	"github.com/zclconf/go-cty/cty"
)

const independentRegistryAddress = "registry.terraform.io/ncecere/litellm"

var (
	publishedEntrypoints = []string{
		"examples/complete/main.tf",
		"examples/data-sources/main.tf",
		"examples/mcp-servers/main.tf",
		"examples/minimal/main.tf",
		"examples/multi-provider/main.tf",
		"examples/search-tools/main.tf",
	}
	publishedMarkdownEntrypoints = []string{
		"README.md",
		"docs/index.md",
	}
	hclFencePattern = regexp.MustCompile("(?s)```hcl[ \\t]*\\r?\\n(.*?)\\r?\\n```")
)

func TestInternalMetadataContract(t *testing.T) {
	t.Parallel()

	// This assertion is intentionally independent of internal metadata. It
	// prevents every consumer from agreeing on the same wrong Registry address.
	if metadata.ProviderSource != independentRegistryAddress {
		t.Fatalf("ProviderSource = %q, want independently asserted %q", metadata.ProviderSource, independentRegistryAddress)
	}

	for name, value := range map[string]string{
		"TerraformMinimum":       metadata.TerraformMinimum,
		"OpenTofuMinimum":        metadata.OpenTofuMinimum,
		"GoMinimum":              metadata.GoMinimum,
		"WriteOnlyClientMinimum": metadata.WriteOnlyClientMinimum,
		"TestedLiteLLMVersion":   metadata.TestedLiteLLMVersion,
		"CurrentProviderVersion": metadata.CurrentProviderVersion,
		"MinimumProviderVersion": metadata.MinimumProviderVersion,
	} {
		if _, err := parseVersion(value); err != nil {
			t.Errorf("%s is not an exact three-component version: %v", name, err)
		}
	}
	if !versionSatisfies(t, metadata.MinimumProviderVersion, metadata.ExampleProviderConstraint) {
		t.Fatalf("minimum provider version %q does not satisfy example constraint %q", metadata.MinimumProviderVersion, metadata.ExampleProviderConstraint)
	}
	if !versionSatisfies(t, metadata.CurrentProviderVersion, metadata.ExampleProviderConstraint) {
		t.Fatalf("current provider version %q does not satisfy example constraint %q", metadata.CurrentProviderVersion, metadata.ExampleProviderConstraint)
	}
	if compareVersion(mustParseVersion(t, metadata.CurrentProviderVersion), mustParseVersion(t, metadata.MinimumProviderVersion)) < 0 {
		t.Fatalf("current provider version %q predates supported minimum %q", metadata.CurrentProviderVersion, metadata.MinimumProviderVersion)
	}
	if !versionSatisfies(t, metadata.TerraformMinimum, metadata.TerraformRequiredVersion) {
		t.Fatalf("Terraform minimum %q does not satisfy required_version %q", metadata.TerraformMinimum, metadata.TerraformRequiredVersion)
	}

	providerMinimum := mustParseVersion(t, metadata.MinimumProviderVersion)
	nextMajor := fmt.Sprintf("%d.0.0", providerMinimum.major+1)
	if versionSatisfies(t, nextMajor, metadata.ExampleProviderConstraint) {
		t.Fatalf("next provider major %q unexpectedly satisfies example constraint %q", nextMajor, metadata.ExampleProviderConstraint)
	}
}

func TestMakeInstallDefaultsMatchPublishedContract(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	body := readFile(t, filepath.Join(root, "Makefile"))
	defaults := makeDefaults(body)
	sourceParts := strings.Split(metadata.ProviderSource, "/")
	if len(sourceParts) != 3 {
		t.Fatalf("ProviderSource %q does not have hostname/namespace/name components", metadata.ProviderSource)
	}
	want := map[string]string{
		"HOSTNAME":  sourceParts[0],
		"NAMESPACE": sourceParts[1],
		"NAME":      sourceParts[2],
		"VERSION":   metadata.CurrentProviderVersion,
		"OS_ARCH":   "$(shell go env GOOS)_$(shell go env GOARCH)",
	}
	for name, expected := range want {
		if defaults[name] != expected {
			t.Errorf("Makefile %s default = %q, want %q", name, defaults[name], expected)
		}
	}

	installDirectory := "~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}"
	installedBinary := installDirectory + "/terraform-provider-${NAME}_v${VERSION}"
	if !strings.Contains(body, "mkdir -p "+installDirectory) {
		t.Errorf("Makefile install recipe does not create canonical plugin directory %q", installDirectory)
	}
	if !strings.Contains(body, "mv terraform-provider-${NAME} "+installedBinary) {
		t.Errorf("Makefile install recipe does not select canonical versioned binary %q", installedBinary)
	}

	documentedPath := "~/.terraform.d/plugins/" + defaults["HOSTNAME"] + "/" + defaults["NAMESPACE"] + "/" + defaults["NAME"] + "/" + defaults["VERSION"] + "/GOOS_GOARCH/terraform-provider-" + defaults["NAME"] + "_v" + defaults["VERSION"]
	wantPath := "~/.terraform.d/plugins/" + metadata.ProviderSource + "/" + metadata.CurrentProviderVersion + "/GOOS_GOARCH/terraform-provider-" + sourceParts[2] + "_v" + metadata.CurrentProviderVersion
	if documentedPath != wantPath {
		t.Fatalf("rendered make install path = %q, want canonical metadata path %q", documentedPath, wantPath)
	}
	if !versionSatisfies(t, defaults["VERSION"], metadata.ExampleProviderConstraint) {
		t.Fatalf("make install version %q does not satisfy published examples %q", defaults["VERSION"], metadata.ExampleProviderConstraint)
	}

	readme := readFile(t, filepath.Join(root, "README.md"))
	if !strings.Contains(readme, "git clone https://github.com/ncecere/terraform-provider-litellm.git") {
		t.Fatal("README clone command does not use the canonical repository URL")
	}
	if !strings.Contains(readme, "make install") {
		t.Fatal("README does not document make install")
	}
}

func TestRegistryManifestMatchesProtocolMetadata(t *testing.T) {
	t.Parallel()

	body := readFileBytes(t, filepath.Join(repositoryRoot(t), "terraform-registry-manifest.json"))
	var manifest struct {
		Version  int `json:"version"`
		Metadata struct {
			ProtocolVersions []string `json:"protocol_versions"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if manifest.Version != 1 {
		t.Fatalf("manifest version = %d, want 1", manifest.Version)
	}
	if want := []string{metadata.ProtocolVersion}; !reflect.DeepEqual(manifest.Metadata.ProtocolVersions, want) {
		t.Fatalf("manifest protocols = %#v, want %#v", manifest.Metadata.ProtocolVersions, want)
	}
}

func TestPublishedEntrypointSetAndHCLContracts(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	var candidates, contracts []string
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".tf" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Name() == "main.tf" {
			candidates = append(candidates, relative)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, found := parseOptionalTerraformContract(t, body, path); found {
			contracts = append(contracts, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(candidates)
	sort.Strings(contracts)
	if !reflect.DeepEqual(candidates, publishedEntrypoints) {
		t.Fatalf("example entrypoint candidates changed; review and update explicit coverage deliberately:\nactual: %#v\nexpected: %#v", candidates, publishedEntrypoints)
	}
	if !reflect.DeepEqual(contracts, publishedEntrypoints) {
		t.Fatalf("files containing example terraform contracts changed; review and update explicit coverage deliberately:\nactual: %#v\nexpected: %#v", contracts, publishedEntrypoints)
	}

	for _, relative := range publishedEntrypoints {
		relative := relative
		t.Run(relative, func(t *testing.T) {
			t.Parallel()
			assertPublishedTerraformContract(t, parseTerraformContract(t, filepath.Join(root, relative)), true)
		})
	}
}

func TestPublishedMarkdownEntrypointContracts(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, relative := range publishedMarkdownEntrypoints {
		relative := relative
		t.Run(relative, func(t *testing.T) {
			t.Parallel()
			body := readFileBytes(t, filepath.Join(root, relative))
			var contracts []terraformContract
			for _, match := range hclFencePattern.FindAllSubmatch(body, -1) {
				if contract, found := parseOptionalTerraformContract(t, match[1], relative); found {
					contracts = append(contracts, contract)
				}
			}
			if len(contracts) != 1 {
				t.Fatalf("published provider entrypoint count = %d, want 1", len(contracts))
			}
			assertPublishedTerraformContract(t, contracts[0], true)
		})
	}
}

func TestPublishedCompatibilityProseMatchesMetadata(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	documents := map[string]struct {
		body      string
		fragments []string
	}{
		"README.md": {
			body: readFile(t, filepath.Join(root, "README.md")),
			fragments: []string{
				metadata.ProviderSource,
				`version = "` + metadata.ExampleProviderConstraint + `"`,
				">= " + metadata.TerraformMinimum + " (provider protocol " + metadata.ProtocolVersion + ")",
				">= " + metadata.OpenTofuMinimum,
				">= " + metadata.GoMinimum + " for provider development",
				"Tested backend: exactly LiteLLM " + metadata.TestedLiteLLMVersion,
				"require Terraform or OpenTofu " + metadata.WriteOnlyClientMinimum + " or later",
			},
		},
		"docs/index.md": {
			body: readFile(t, filepath.Join(root, "docs", "index.md")),
			fragments: []string{
				metadata.ProviderSource,
				`version = "` + metadata.ExampleProviderConstraint + `"`,
				"Terraform " + metadata.TerraformMinimum + " or later (provider protocol " + metadata.ProtocolVersion + ")",
				"OpenTofu " + metadata.OpenTofuMinimum + " or later",
				"Go " + metadata.GoMinimum + " or later",
				"tests exactly LiteLLM " + metadata.TestedLiteLLMVersion,
				"require Terraform or OpenTofu " + metadata.WriteOnlyClientMinimum + " or later",
			},
		},
		"examples/README.md": {
			body: readFile(t, filepath.Join(root, "examples", "README.md")),
			fragments: []string{
				metadata.ProviderSource,
				"constrains the provider to `" + metadata.ExampleProviderConstraint + "`",
				"requires Terraform >= " + metadata.TerraformMinimum,
				"OpenTofu >= " + metadata.OpenTofuMinimum,
				"Go >= " + metadata.GoMinimum,
				"tested against exactly LiteLLM " + metadata.TestedLiteLLMVersion,
				"require Terraform or OpenTofu >= " + metadata.WriteOnlyClientMinimum,
			},
		},
		"CHANGELOG.md [" + metadata.CurrentProviderVersion + "]": {
			body: changelogVersionSection(t, readFile(t, filepath.Join(root, "CHANGELOG.md")), metadata.CurrentProviderVersion),
			fragments: []string{
				metadata.ProviderSource,
				"provider to `" + metadata.ExampleProviderConstraint + "`",
				"Terraform >= " + metadata.TerraformMinimum,
				"OpenTofu >= " + metadata.OpenTofuMinimum,
				"Go >= " + metadata.GoMinimum,
				"exactly LiteLLM " + metadata.TestedLiteLLMVersion,
				"require Terraform or OpenTofu >= " + metadata.WriteOnlyClientMinimum,
			},
		},
	}
	for name, document := range documents {
		name, document := name, document
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertContainsAll(t, name, document.body, document.fragments)
		})
	}
}

func TestDevelopmentAndInternalTestingMetadataContracts(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	contract := parseTerraformContract(t, filepath.Join(root, "internal_testing", "provider.tf"))
	assertPublishedTerraformContract(t, contract, false)
	if contract.version != "" {
		t.Fatalf("internal testing unexpectedly constrains the locally installed provider: %q", contract.version)
	}

	assertContainsAll(t, ".go-version", strings.TrimSpace(readFile(t, filepath.Join(root, ".go-version"))), []string{metadata.GoMinimum})
	assertContainsAll(t, "go.mod", readFile(t, filepath.Join(root, "go.mod")), []string{"go " + metadata.GoMinimum})
	assertContainsAll(t, "internal_testing/docker-compose.yml", readFile(t, filepath.Join(root, "internal_testing", "docker-compose.yml")), []string{"litellm:v" + metadata.TestedLiteLLMVersion})
	assertContainsAll(t, "internal_testing/acceptance.sh", readFile(t, filepath.Join(root, "internal_testing", "acceptance.sh")), []string{metadata.TestedLiteLLMVersion})
	assertContainsAll(t, "internal_testing/README.md", readFile(t, filepath.Join(root, "internal_testing", "README.md")), []string{metadata.TestedLiteLLMVersion})
}

func assertPublishedTerraformContract(t *testing.T, contract terraformContract, requireProviderVersion bool) {
	t.Helper()
	if contract.source != metadata.ProviderSource {
		t.Fatalf("provider source = %q, want metadata source %q", contract.source, metadata.ProviderSource)
	}
	assertConstraintEquivalent(t, "required_version", contract.requiredVersion, metadata.TerraformRequiredVersion)
	if requireProviderVersion {
		assertConstraintEquivalent(t, "provider version", contract.version, metadata.ExampleProviderConstraint)
	}
}

func assertContainsAll(t *testing.T, name, body string, expected []string) {
	t.Helper()
	for _, value := range expected {
		if !strings.Contains(body, value) {
			t.Errorf("%s does not encode metadata value %q", name, value)
		}
	}
}

func changelogVersionSection(t *testing.T, changelog, version string) string {
	t.Helper()
	heading := "## [" + version + "]"
	if strings.Count(changelog, heading) != 1 {
		t.Fatalf("CHANGELOG %s heading count = %d, want 1", version, strings.Count(changelog, heading))
	}
	start := strings.Index(changelog, heading) + len(heading)
	section := changelog[start:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

func makeDefaults(body string) map[string]string {
	matches := regexp.MustCompile(`(?m)^([A-Z_]+) \?= ([^\r\n]+)$`).FindAllStringSubmatch(body, -1)
	defaults := make(map[string]string, len(matches))
	for _, match := range matches {
		defaults[match[1]] = match[2]
	}
	return defaults
}

type semanticVersion struct {
	major int
	minor int
	patch int
}

func parseVersion(value string) (semanticVersion, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("version %q must have three components", value)
	}
	values := make([]int, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, fmt.Errorf("version %q has a non-canonical component", value)
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return semanticVersion{}, fmt.Errorf("version %q has an invalid component", value)
		}
		values[index] = parsed
	}
	return semanticVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func mustParseVersion(t *testing.T, value string) semanticVersion {
	t.Helper()
	version, err := parseVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

type parsedConstraint struct {
	operator string
	version  semanticVersion
}

func parseConstraints(t *testing.T, value string) []parsedConstraint {
	t.Helper()
	var constraints []parsedConstraint
	for _, part := range strings.Split(value, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) != 2 || (fields[0] != ">=" && fields[0] != ">" && fields[0] != "<=" && fields[0] != "<" && fields[0] != "=") {
			t.Fatalf("unsupported version constraint %q", value)
		}
		constraints = append(constraints, parsedConstraint{operator: fields[0], version: mustParseVersion(t, fields[1])})
	}
	sort.Slice(constraints, func(i, j int) bool {
		if constraints[i].operator != constraints[j].operator {
			return constraints[i].operator < constraints[j].operator
		}
		return compareVersion(constraints[i].version, constraints[j].version) < 0
	})
	return constraints
}

func assertConstraintEquivalent(t *testing.T, name, actual, expected string) {
	t.Helper()
	if !reflect.DeepEqual(parseConstraints(t, actual), parseConstraints(t, expected)) {
		t.Fatalf("%s = %q, want semantic constraint %q", name, actual, expected)
	}
}

func versionSatisfies(t *testing.T, versionValue, constraintValue string) bool {
	t.Helper()
	version := mustParseVersion(t, versionValue)
	for _, constraint := range parseConstraints(t, constraintValue) {
		comparison := compareVersion(version, constraint.version)
		if (constraint.operator == ">=" && comparison < 0) ||
			(constraint.operator == ">" && comparison <= 0) ||
			(constraint.operator == "<=" && comparison > 0) ||
			(constraint.operator == "<" && comparison >= 0) ||
			(constraint.operator == "=" && comparison != 0) {
			return false
		}
	}
	return true
}

func compareVersion(left, right semanticVersion) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

type terraformContract struct {
	source          string
	version         string
	requiredVersion string
}

func parseTerraformContract(t *testing.T, path string) terraformContract {
	t.Helper()
	body := readFileBytes(t, path)
	contract, found := parseOptionalTerraformContract(t, body, path)
	if !found {
		t.Fatal("terraform block is missing")
	}
	return contract
}

func parseOptionalTerraformContract(t *testing.T, body []byte, path string) (terraformContract, bool) {
	t.Helper()

	file, diagnostics := hclsyntax.ParseConfig(body, path, hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		t.Fatalf("parse HCL: %s", diagnostics.Error())
	}
	blocks := file.Body.(*hclsyntax.Body).Blocks
	var terraformBlocks []*hclsyntax.Block
	for _, block := range blocks {
		if block.Type == "terraform" {
			terraformBlocks = append(terraformBlocks, block)
		}
	}
	if len(terraformBlocks) == 0 {
		return terraformContract{}, false
	}
	if len(terraformBlocks) != 1 {
		t.Fatalf("terraform block count = %d, want 1", len(terraformBlocks))
	}
	attributes := terraformBlocks[0].Body.Attributes
	requiredVersion := constantString(t, path, attributes, "required_version")

	var providerBlocks []*hclsyntax.Block
	for _, block := range terraformBlocks[0].Body.Blocks {
		if block.Type == "required_providers" {
			providerBlocks = append(providerBlocks, block)
		}
	}
	if len(providerBlocks) != 1 {
		t.Fatalf("required_providers block count = %d, want 1", len(providerBlocks))
	}
	providerAttribute, ok := providerBlocks[0].Body.Attributes["litellm"]
	if !ok {
		t.Fatal("required_providers.litellm attribute is missing")
	}
	provider, diagnostics := providerAttribute.Expr.Value(nil)
	if diagnostics.HasErrors() || !provider.IsKnown() || provider.IsNull() {
		t.Fatalf("evaluate required_providers.litellm: %s", diagnostics.Error())
	}
	return terraformContract{
		source:          objectAttribute(t, provider, "source").AsString(),
		version:         optionalObjectString(t, provider, "version"),
		requiredVersion: requiredVersion,
	}, true
}

func constantString(t *testing.T, path string, attributes hclsyntax.Attributes, name string) string {
	t.Helper()
	attribute, ok := attributes[name]
	if !ok {
		t.Fatalf("%s: %s attribute is missing", path, name)
	}
	value, diagnostics := attribute.Expr.Value(nil)
	if diagnostics.HasErrors() || value.Type() != cty.String || !value.IsKnown() || value.IsNull() {
		t.Fatalf("%s: %s must be a constant string: %s", path, name, diagnostics.Error())
	}
	return value.AsString()
}

func objectAttribute(t *testing.T, object cty.Value, name string) cty.Value {
	t.Helper()
	if !object.Type().IsObjectType() || !object.Type().HasAttribute(name) {
		t.Fatalf("object attribute %q is missing", name)
	}
	value := object.GetAttr(name)
	if !value.IsKnown() || value.IsNull() {
		t.Fatalf("object attribute %q is null or unknown", name)
	}
	return value
}

func optionalObjectString(t *testing.T, object cty.Value, name string) string {
	t.Helper()
	if !object.Type().IsObjectType() || !object.Type().HasAttribute(name) {
		return ""
	}
	value := object.GetAttr(name)
	if value.Type() != cty.String || !value.IsKnown() || value.IsNull() {
		t.Fatalf("object attribute %q must be a constant string", name)
	}
	return value.AsString()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	return string(readFileBytes(t, path))
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
