package provider

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type collectionAuditViolation struct {
	File   string
	Symbol string
	Kind   string
}

func (v collectionAuditViolation) String() string {
	return v.File + "|" + v.Symbol + "|" + v.Kind
}

// legacyCollectionConversionAllowlist is deliberately keyed by source path,
// enclosing symbol, and violation kind, never by line number. Counts make the
// inventory exact even when one symbol contains repeated legacy calls. Each
// migration stage must remove the corresponding entry or lower its count.
var legacyCollectionConversionAllowlist = map[collectionAuditViolation]int{
	{File: "agent_structured.go", Symbol: "readAgentSecurity", Kind: "production ListValueMust constructor"}:                                               1,
	{File: "agent_structured.go", Symbol: "readAgentSecurity", Kind: "production MapValueMust constructor"}:                                                1,
	{File: "datasource_agent.go", Symbol: "projectAgentData", Kind: "production MapValueMust constructor"}:                                                 1,
	{File: "datasource_fallback.go", Symbol: "*FallbackDataSource.readFallback", Kind: "discarded ListValue constructor diagnostics"}:                      1,
	{File: "datasource_key.go", Symbol: "*KeyDataSource.Read", Kind: "discarded ListValue constructor diagnostics"}:                                        4,
	{File: "datasource_key.go", Symbol: "*KeyDataSource.Read", Kind: "discarded MapValue constructor diagnostics"}:                                         2,
	{File: "datasource_mcp_server.go", Symbol: "*MCPServerDataSource.Read", Kind: "discarded ListValue constructor diagnostics"}:                           8,
	{File: "datasource_mcp_server.go", Symbol: "*MCPServerDataSource.Read", Kind: "discarded MapValue constructor diagnostics"}:                            4,
	{File: "datasource_organization.go", Symbol: "*OrganizationDataSource.Read", Kind: "production ListValueMust constructor"}:                             1,
	{File: "datasource_tag.go", Symbol: "*TagDataSource.Read", Kind: "discarded ListValue constructor diagnostics"}:                                        2,
	{File: "datasource_tags_list.go", Symbol: "*TagsListDataSource.Read", Kind: "discarded ListValue constructor diagnostics"}:                             2,
	{File: "datasource_team.go", Symbol: "*TeamDataSource.Read", Kind: "discarded ListValue constructor diagnostics"}:                                      5,
	{File: "datasource_team.go", Symbol: "*TeamDataSource.Read", Kind: "discarded MapValue constructor diagnostics"}:                                       2,
	{File: "datasource_user.go", Symbol: "*UserDataSource.Read", Kind: "discarded ListValue constructor diagnostics"}:                                      4,
	{File: "datasource_user.go", Symbol: "*UserDataSource.Read", Kind: "discarded MapValue constructor diagnostics"}:                                       2,
	{File: "resource_agent.go", Symbol: "*AgentResource.hydrateAgentUpdateFieldsWithOwnership", Kind: "production ListValueMust constructor"}:              1,
	{File: "resource_agent.go", Symbol: "interfaceSliceToStringList", Kind: "discarded ListValue constructor diagnostics"}:                                 2,
	{File: "resource_agent_lifecycle.go", Symbol: "*AgentResource.ModifyPlan", Kind: "production MapValueMust constructor"}:                                1,
	{File: "resource_agent_lifecycle.go", Symbol: "reconcileConfirmedAgentState", Kind: "production MapValueMust constructor"}:                             1,
	{File: "resource_fallback.go", Symbol: "*FallbackResource.readFallback", Kind: "discarded ListValue constructor diagnostics"}:                          1,
	{File: "resource_key.go", Symbol: "*KeyResource.readKeyWithNumericOwnership", Kind: "discarded ListValue constructor diagnostics"}:                     16,
	{File: "resource_key.go", Symbol: "*KeyResource.readKeyWithNumericOwnership", Kind: "discarded MapValue constructor diagnostics"}:                      9,
	{File: "resource_key.go", Symbol: "*KeyResource.readKeyWithNumericOwnership", Kind: "ignored ElementsAs diagnostics"}:                                  1,
	{File: "resource_mcp_server.go", Symbol: "*MCPServerResource.buildMCPServerRequest", Kind: "ignored ElementsAs diagnostics"}:                           7,
	{File: "resource_mcp_server.go", Symbol: "*MCPServerResource.readMCPServerProjection", Kind: "discarded ListValue constructor diagnostics"}:            8,
	{File: "resource_mcp_server.go", Symbol: "*MCPServerResource.readMCPServerProjection", Kind: "discarded MapValue constructor diagnostics"}:             6,
	{File: "resource_model.go", Symbol: "*ModelResource.ModifyPlan", Kind: "production ListValueMust constructor"}:                                         1,
	{File: "resource_model.go", Symbol: "*ModelResource.readModelWithOwnership", Kind: "discarded ListValue constructor diagnostics"}:                      2,
	{File: "resource_model.go", Symbol: "*ModelResource.readModelWithOwnership", Kind: "discarded MapValue constructor diagnostics"}:                       4,
	{File: "resource_model.go", Symbol: "*ModelResource.readModelWithOwnership", Kind: "ignored ElementsAs diagnostics"}:                                   1,
	{File: "resource_model.go", Symbol: "finalizeModelComputedDefaults", Kind: "discarded ListValue constructor diagnostics"}:                              1,
	{File: "resource_model.go", Symbol: "finalizeModelComputedDefaults", Kind: "discarded MapValue constructor diagnostics"}:                               2,
	{File: "resource_organization.go", Symbol: "*OrganizationResource.readOrganizationWithNumericOwnership", Kind: "production ListValueMust constructor"}: 1,
	{File: "resource_tag.go", Symbol: "applyTagObjectToResource", Kind: "discarded ListValue constructor diagnostics"}:                                     2,
	{File: "resource_tag.go", Symbol: "resolveTagCreateUnknowns", Kind: "discarded ListValue constructor diagnostics"}:                                     1,
	{File: "resource_team.go", Symbol: "*TeamResource.Update", Kind: "ignored ElementsAs diagnostics"}:                                                     1,
	{File: "resource_team.go", Symbol: "*TeamResource.buildTeamRequest", Kind: "ignored ElementsAs diagnostics"}:                                           8,
	{File: "resource_team.go", Symbol: "*TeamResource.readTeamWithNumericOwnership", Kind: "discarded ListValue constructor diagnostics"}:                  10,
	{File: "resource_team.go", Symbol: "*TeamResource.readTeamWithNumericOwnership", Kind: "discarded MapValue constructor diagnostics"}:                   5,
	{File: "resource_team.go", Symbol: "*TeamResource.readTeamWithNumericOwnership", Kind: "ignored ElementsAs diagnostics"}:                               1,
	{File: "resource_team.go", Symbol: "apiFormatToFallbackEntries", Kind: "discarded ListValue constructor diagnostics"}:                                  2,
	{File: "resource_team.go", Symbol: "apiFormatToFallbackEntries", Kind: "discarded ObjectValue constructor diagnostics"}:                                1,
	{File: "resource_team.go", Symbol: "buildRouterSettingsPayload", Kind: "ignored Object.As diagnostics"}:                                                1,
	{File: "resource_team.go", Symbol: "fallbackEntriesToAPIFormat", Kind: "ignored ElementsAs diagnostics"}:                                               2,
	{File: "resource_team.go", Symbol: "parseRouterSettingsFromAPI", Kind: "discarded ObjectValue constructor diagnostics"}:                                1,
	{File: "resource_unified_access_group.go", Symbol: "resolveUnifiedAccessGroupUnknowns", Kind: "production ListValueMust constructor"}:                  1,
	{File: "resource_unified_access_group.go", Symbol: "setListFromResponse", Kind: "discarded ListValue constructor diagnostics"}:                         3,
	{File: "resource_unified_access_group.go", Symbol: "unifiedAccessGroupKeyList", Kind: "production ListValueMust constructor"}:                          1,
	{File: "resource_user.go", Symbol: "*UserResource.readUserWithNumericOwnership", Kind: "discarded ListValue constructor diagnostics"}:                  2,
	{File: "resource_user.go", Symbol: "*UserResource.readUserWithNumericOwnership", Kind: "discarded MapValue constructor diagnostics"}:                   2,
	{File: "resource_user.go", Symbol: "reconcileUnorderedUserTeams", Kind: "production ListValueMust constructor"}:                                        2,
}

func TestCollectionConversionSafetyAudit(t *testing.T) {
	violations, err := scanCollectionConversionViolations(".")
	if err != nil {
		t.Fatal(err)
	}
	actual := countCollectionAuditViolations(violations)
	if difference := collectionAuditDifference(legacyCollectionConversionAllowlist, actual); difference != "" {
		t.Fatalf("collection conversion safety inventory changed:\n%s", difference)
	}
}

func TestCollectionConversionSafetyAuditRecognizesNewViolations(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	source := `package fixture
import "github.com/hashicorp/terraform-plugin-framework/types"
func ignoredConversions() {
	value.ElementsAs(ctx, &items, false)
	_ = object.As(ctx, &decoded, options)
	list, _ := types.ListValue(elementType, elements)
	set, _ := types.SetValue(elementType, elements)
	mapped, _ = types.MapValue(elementType, entries)
	object, _ := types.ObjectValue(attributeTypes, attributes)
	_ = types.SetValueMust(elementType, elements)
}
func propagatedConversions() diagnostics {
	if diagnostics := value.ElementsAs(ctx, &items, false); diagnostics.HasError() { return diagnostics }
	list, diagnostics := types.ListValue(elementType, elements)
	return diagnostics
}
`
	if err := os.WriteFile(filepath.Join(directory, "fixture.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	violations, err := scanCollectionConversionViolations(directory)
	if err != nil {
		t.Fatal(err)
	}
	counts := countCollectionAuditViolations(violations)
	want := map[string]int{
		"ignored ElementsAs diagnostics":                1,
		"ignored Object.As diagnostics":                 1,
		"discarded ListValue constructor diagnostics":   1,
		"discarded SetValue constructor diagnostics":    1,
		"discarded MapValue constructor diagnostics":    1,
		"discarded ObjectValue constructor diagnostics": 1,
		"production SetValueMust constructor":           1,
	}
	got := make(map[string]int)
	for violation, count := range counts {
		got[violation.Kind] += count
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("recognized violations = %#v, want %#v", got, want)
	}
}

func scanCollectionConversionViolations(directory string) ([]collectionAuditViolation, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var violations []collectionAuditViolation
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		filename := filepath.Join(directory, entry.Name())
		fileSet := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fileSet, filename, nil, 0)
		if parseErr != nil {
			return nil, parseErr
		}
		violations = append(violations, scanCollectionConversionFile(entry.Name(), parsed)...)
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].String() < violations[j].String() })
	return violations, nil
}

func scanCollectionConversionFile(filename string, file *ast.File) []collectionAuditViolation {
	var violations []collectionAuditViolation
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		symbol := function.Name.Name
		if function.Recv != nil && len(function.Recv.List) > 0 {
			symbol = receiverTypeName(function.Recv.List[0].Type) + "." + symbol
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.ExprStmt:
				if call, ok := typed.X.(*ast.CallExpr); ok {
					if kind := ignoredConversionCallKind(call); kind != "" {
						violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: kind})
					}
				}
			case *ast.AssignStmt:
				if len(typed.Rhs) != 1 {
					break
				}
				call, ok := typed.Rhs[0].(*ast.CallExpr)
				if !ok {
					break
				}
				if len(typed.Lhs) == 1 && isBlankIdentifier(typed.Lhs[0]) {
					if kind := ignoredConversionCallKind(call); kind != "" {
						violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: kind})
					}
				}
				if len(typed.Lhs) >= 2 && isBlankIdentifier(typed.Lhs[len(typed.Lhs)-1]) {
					if constructor := collectionConstructorName(call); constructor != "" {
						violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: "discarded " + constructor + " constructor diagnostics"})
					}
				}
			}
			if call, ok := node.(*ast.CallExpr); ok {
				if must := collectionValueMustName(call); must != "" {
					violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: "production " + must + " constructor"})
				}
			}
			return true
		})
	}
	return violations
}

func ignoredConversionCallKind(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	switch selector.Sel.Name {
	case "ElementsAs":
		return "ignored ElementsAs diagnostics"
	case "As":
		// The three-argument form is Terraform Framework Object.As. This
		// excludes errors.As and protocol tftypes.Value.As without requiring
		// fragile repository-wide type checking in the audit.
		if len(call.Args) == 3 {
			return "ignored Object.As diagnostics"
		}
	}
	return ""
}

func collectionConstructorName(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	switch selector.Sel.Name {
	case "ListValue", "SetValue", "MapValue", "ObjectValue", "NewListValue", "NewSetValue", "NewMapValue", "NewObjectValue":
		return selector.Sel.Name
	default:
		return ""
	}
}

func collectionValueMustName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.SelectorExpr:
		if strings.HasSuffix(function.Sel.Name, "ValueMust") {
			return function.Sel.Name
		}
	case *ast.Ident:
		if strings.HasSuffix(function.Name, "ValueMust") {
			return function.Name
		}
	}
	return ""
}

func receiverTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return "*" + receiverTypeName(typed.X)
	case *ast.IndexExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typed.X)
	default:
		return "receiver"
	}
}

func isBlankIdentifier(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "_"
}

func countCollectionAuditViolations(violations []collectionAuditViolation) map[collectionAuditViolation]int {
	counts := make(map[collectionAuditViolation]int)
	for _, violation := range violations {
		counts[violation]++
	}
	return counts
}

func collectionAuditDifference(want, got map[collectionAuditViolation]int) string {
	keys := make(map[collectionAuditViolation]struct{}, len(want)+len(got))
	for violation := range want {
		keys[violation] = struct{}{}
	}
	for violation := range got {
		keys[violation] = struct{}{}
	}
	ordered := make([]collectionAuditViolation, 0, len(keys))
	for violation := range keys {
		ordered = append(ordered, violation)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	var differences []string
	for _, violation := range ordered {
		if want[violation] != got[violation] {
			differences = append(differences, fmt.Sprintf("%s: got %d, allowlist %d", violation, got[violation], want[violation]))
		}
	}
	return strings.Join(differences, "\n")
}
