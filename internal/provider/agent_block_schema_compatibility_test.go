package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

type agentSchemaCompatibilityGolden struct {
	Version              int64    `json:"version"`
	AgentCardNesting     string   `json:"agent_card_nesting"`
	SkillsNesting        string   `json:"skills_nesting"`
	SignaturesNesting    string   `json:"signatures_nesting"`
	SkillsAttributes     []string `json:"skills_attributes"`
	SignaturesAttributes []string `json:"signatures_attributes"`
}

func findAgentNestedBlock(t *testing.T, block *tfprotov6.SchemaBlock, name string) *tfprotov6.SchemaNestedBlock {
	t.Helper()
	for _, nested := range block.BlockTypes {
		if nested.TypeName == name {
			return nested
		}
	}
	t.Fatalf("nested block %q missing", name)
	return nil
}

func TestAgentSchemaMatchesOriginIssuesBlockGolden(t *testing.T) {
	server := providerserver.NewProtocol6(New("test")())()
	response, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("schema: err=%v diagnostics=%v", err, response.Diagnostics)
	}
	resourceSchema := response.ResourceSchemas["litellm_agent"]
	agentCard := findAgentNestedBlock(t, resourceSchema.Block, "agent_card")
	skills := findAgentNestedBlock(t, agentCard.Block, "skills")
	signatures := findAgentNestedBlock(t, agentCard.Block, "signatures")
	for _, attribute := range agentCard.Block.Attributes {
		if attribute.Name == "skills" || attribute.Name == "signatures" {
			t.Fatalf("%s was converted from block_types to assignment attribute", attribute.Name)
		}
	}
	attributeNames := func(block *tfprotov6.SchemaBlock) []string {
		result := make([]string, 0, len(block.Attributes))
		for _, attribute := range block.Attributes {
			result = append(result, attribute.Name)
		}
		sort.Strings(result)
		return result
	}
	actual := agentSchemaCompatibilityGolden{
		Version: resourceSchema.Version, AgentCardNesting: agentCard.Nesting.String(),
		SkillsNesting: skills.Nesting.String(), SignaturesNesting: signatures.Nesting.String(),
		SkillsAttributes: attributeNames(skills.Block), SignaturesAttributes: attributeNames(signatures.Block),
	}
	actualJSON, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actualJSON = append(actualJSON, '\n')
	golden, err := os.ReadFile(filepath.Join("testdata", "agent-schema-origin-issues.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualJSON, golden) {
		t.Fatalf("agent protocol schema differs from origin/issues golden:\n%s", actualJSON)
	}
}

func TestExistingAgentFixturesUseValidBlockGrammar(t *testing.T) {
	fixtures := []string{
		filepath.Join("..", "..", "internal_testing", "resources", "agent_lifecycle_clear.tf"),
		filepath.Join("..", "..", "internal_testing", "resources", "agent_structured_advanced.tf"),
	}
	assignment := regexp.MustCompile(`(?m)^\s*(skills|signatures)\s*=`)
	for _, fixture := range fixtures {
		source, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if assignment.Match(source) {
			t.Fatalf("%s uses incompatible collection assignment syntax", fixture)
		}
		_, diagnostics := hclsyntax.ParseConfig(source, fixture, hcl.Pos{Line: 1, Column: 1})
		if diagnostics.HasErrors() {
			t.Fatalf("%s does not parse as existing block HCL: %s", fixture, diagnostics.Error())
		}
	}
}
