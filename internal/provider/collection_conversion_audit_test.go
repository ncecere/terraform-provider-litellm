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
// Every audited production conversion has been migrated. Any new ignored
// diagnostic or Must constructor therefore fails this inventory immediately.
var legacyCollectionConversionAllowlist = map[collectionAuditViolation]int{}

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
var packageList, packageListDiagnostics = types.ListValue(elementType, elements)
var packageMust = types.SetValueMust(elementType, elements)
func ignoredConversions() {
	value.ElementsAs(ctx, &items, false)
	_ = object.As(ctx, &decoded, options)
	elementDiagnostics := value.ElementsAs(ctx, &items, false)
	_ = elementDiagnostics
	objectDiagnostics := object.As(ctx, &decoded, options)
	_ = objectDiagnostics
	var declaredElements = value.ElementsAs(ctx, &items, false)
	_ = declaredElements
	var declaredObject = object.As(ctx, &decoded, options)
	_ = declaredObject
	sentinel, multiElements := 0, value.ElementsAs(ctx, &items, false)
	_ = multiElements
	var otherSentinel, multiObject = 0, object.As(ctx, &decoded, options)
	_ = multiObject
	var declaredList, declaredListDiagnostics = types.ListValue(elementType, elements)
	_ = declaredListDiagnostics
	list, _ := types.ListValue(elementType, elements)
	from, _ := types.SetValueFrom(ctx, elementType, values)
	named, diagnostics := types.MapValue(elementType, entries)
	_ = diagnostics
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
func reusedDiagnostics() diagnostics {
	list, diagnostics := types.ListValue(elementType, elements)
	if diagnostics.HasError() { return diagnostics }
	mapped, diagnostics = types.MapValue(elementType, entries)
	_ = diagnostics
	return nil
}
func observedOnly() {
	list, diagnostics := types.ListValue(elementType, elements)
	diagnostics.HasError()
}
func lengthOnly() {
	list, diagnostics := types.ListValue(elementType, elements)
	_ = len(diagnostics)
}
func invertedHasError() {
	list, diagnostics := types.ListValue(elementType, elements)
	if !diagnostics.HasError() { return }
}
func zeroLengthReturn() {
	list, diagnostics := types.ListValue(elementType, elements)
	if len(diagnostics) == 0 { return }
}
func invertedProjectionError() {
	list, diagnostics := types.ListValue(elementType, elements)
	if err := collectionProjectionError(ctx, diagnostics); err == nil { return }
}
func appendWithoutPropagation() {
	list, diagnostics := types.ListValue(elementType, elements)
	output.Append(diagnostics...)
}
func appendAndReturn() diagnostics {
	list, diagnostics := types.ListValue(elementType, elements)
	output.Append(diagnostics...)
	return output
}
func bareHasErrorReturn() {
	list, diagnostics := types.ListValue(elementType, elements)
	if diagnostics.HasError() { return }
}
func responseDiagnosticsReturn() {
	list, diagnostics := types.ListValue(elementType, elements)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() { return }
}
func nilErrorReturn() error {
	list, diagnostics := types.ListValue(elementType, elements)
	if diagnostics.HasError() { return nil }
	return nil
}
func appendThenErase() diagnostics {
	list, diagnostics := types.ListValue(elementType, elements)
	output.Append(diagnostics...)
	output = nil
	return output
}
func appendThenOverwriteElement() diagnostics {
	list, diagnostics := types.ListValue(elementType, elements)
	output.Append(diagnostics...)
	output[0] = nil
	return output
}
func shadowedDiagnostics() diagnostics {
	var diagnostics diagnosticsType
	{
		list, diagnostics := types.ListValue(elementType, elements)
		_ = diagnostics
	}
	if diagnostics.HasError() { return diagnostics }
	return diagnostics
}
func shadowedDestination() diagnostics {
	list, diagnostics := types.ListValue(elementType, elements)
	output.Append(diagnostics...)
	{
		output := replacement
		return output
	}
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
		"ignored ElementsAs diagnostics":                 1,
		"unchecked ElementsAs diagnostics":               3,
		"ignored Object.As diagnostics":                  1,
		"unchecked Object.As diagnostics":                3,
		"discarded ListValue constructor diagnostics":    1,
		"unchecked ListValue constructor diagnostics":    14,
		"discarded SetValueFrom constructor diagnostics": 1,
		"discarded SetValue constructor diagnostics":     1,
		"discarded MapValue constructor diagnostics":     1,
		"unchecked MapValue constructor diagnostics":     2,
		"discarded ObjectValue constructor diagnostics":  1,
		"production SetValueMust constructor":            2,
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
		if general, ok := declaration.(*ast.GenDecl); ok {
			for _, specification := range general.Specs {
				value, valueOK := specification.(*ast.ValueSpec)
				if !valueOK {
					continue
				}
				targets := make([]ast.Expr, len(value.Names))
				for index, name := range value.Names {
					targets[index] = name
				}
				violations = append(violations, collectionAssignmentViolations(filename, "<package>", &ast.BlockStmt{}, targets, value.Values, value.Pos())...)
				ast.Inspect(value, func(node ast.Node) bool {
					if nested, ok := node.(*ast.CallExpr); ok {
						if must := collectionValueMustName(nested); must != "" {
							violations = append(violations, collectionAuditViolation{File: filename, Symbol: "<package>", Kind: "production " + must + " constructor"})
						}
					}
					return true
				})
			}
			continue
		}
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
				violations = append(violations, collectionAssignmentViolations(filename, symbol, function.Body, typed.Lhs, typed.Rhs, typed.Pos())...)
			case *ast.ValueSpec:
				targets := make([]ast.Expr, len(typed.Names))
				for index, name := range typed.Names {
					targets[index] = name
				}
				violations = append(violations, collectionAssignmentViolations(filename, symbol, function.Body, targets, typed.Values, typed.Pos())...)
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

func collectionAssignmentViolations(filename, symbol string, body *ast.BlockStmt, targets, values []ast.Expr, position token.Pos) []collectionAuditViolation {
	var violations []collectionAuditViolation
	if len(values) == 1 {
		if call, ok := values[0].(*ast.CallExpr); ok {
			return collectionCallAssignmentViolations(filename, symbol, body, targets, call, position)
		}
		return nil
	}
	if len(values) != len(targets) {
		return nil
	}
	for index, value := range values {
		if call, ok := value.(*ast.CallExpr); ok {
			violations = append(violations, collectionCallAssignmentViolations(filename, symbol, body, targets[index:index+1], call, position)...)
		}
	}
	return violations
}

func collectionCallAssignmentViolations(filename, symbol string, body *ast.BlockStmt, targets []ast.Expr, call *ast.CallExpr, position token.Pos) []collectionAuditViolation {
	var violations []collectionAuditViolation
	if len(targets) == 1 {
		if kind := ignoredConversionCallKind(call); kind != "" {
			target, identifier := targets[0].(*ast.Ident)
			switch {
			case !identifier:
				violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: strings.Replace(kind, "ignored ", "unchecked ", 1)})
			case target.Name == "_":
				violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: kind})
			case !diagnosticAssignmentSafelyConsumed(body, target, position):
				violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: strings.Replace(kind, "ignored ", "unchecked ", 1)})
			}
		}
	}
	if len(targets) >= 2 {
		if constructor := collectionConstructorName(call); constructor != "" {
			target, identifier := targets[len(targets)-1].(*ast.Ident)
			switch {
			case !identifier:
				violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: "unchecked " + constructor + " constructor diagnostics"})
			case target.Name == "_":
				violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: "discarded " + constructor + " constructor diagnostics"})
			case !diagnosticAssignmentSafelyConsumed(body, target, position):
				violations = append(violations, collectionAuditViolation{File: filename, Symbol: symbol, Kind: "unchecked " + constructor + " constructor diagnostics"})
			}
		}
	}
	return violations
}

func diagnosticAssignmentSafelyConsumed(body *ast.BlockStmt, source *ast.Ident, assignment token.Pos) bool {
	parents := collectionAuditParentMap(body)
	nextAssignment := token.NoPos
	var safeUses []token.Pos
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil || node.Pos() <= assignment {
			return true
		}
		switch typed := node.(type) {
		case *ast.AssignStmt:
			for _, target := range typed.Lhs {
				if identifier, ok := target.(*ast.Ident); ok && collectionAuditSameBinding(identifier, source) {
					if nextAssignment == token.NoPos || typed.Pos() < nextAssignment {
						nextAssignment = typed.Pos()
					}
				}
			}
		case *ast.ReturnStmt:
			for _, result := range typed.Results {
				if identifier, ok := result.(*ast.Ident); ok && collectionAuditSameBinding(identifier, source) {
					safeUses = append(safeUses, typed.Pos())
				}
			}
		case *ast.CallExpr:
			safe := false
			requiresControlFlow := false
			switch function := typed.Fun.(type) {
			case *ast.SelectorExpr:
				if function.Sel.Name == "HasError" {
					if identifier, ok := function.X.(*ast.Ident); ok && collectionAuditSameBinding(identifier, source) {
						safe, requiresControlFlow = true, true
					}
				}
				if function.Sel.Name == "Append" {
					for _, argument := range typed.Args {
						if identifier, ok := argument.(*ast.Ident); ok && collectionAuditSameBinding(identifier, source) {
							safe = collectionAuditAppendedDiagnosticsPropagate(body, function.X, typed.Pos(), source, assignment, parents)
						}
					}
				}
			case *ast.Ident:
				if function.Name == "len" || function.Name == "collectionProjectionError" {
					for _, argument := range typed.Args {
						if identifier, ok := argument.(*ast.Ident); ok && collectionAuditSameBinding(identifier, source) {
							safe, requiresControlFlow = true, true
						}
					}
				}
			}
			if safe && (!requiresControlFlow || collectionDiagnosticCallControlsFailureExit(typed, parents)) {
				safeUses = append(safeUses, typed.Pos())
			}
		}
		return true
	})
	for _, position := range safeUses {
		if position > assignment && (nextAssignment == token.NoPos || position < nextAssignment) {
			return true
		}
	}
	return false
}

func collectionAuditParentMap(root ast.Node) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	stack := []ast.Node{}
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func collectionAuditSameBinding(candidate, source *ast.Ident) bool {
	if candidate.Obj != nil && source.Obj != nil {
		return candidate.Obj == source.Obj
	}
	return candidate.Name == source.Name
}

func collectionAuditAppendedDiagnosticsPropagate(body *ast.BlockStmt, receiver ast.Expr, appendPosition token.Pos, source *ast.Ident, sourceAssignment token.Pos, parents map[ast.Node]ast.Node) bool {
	receiverKey := collectionAuditExpressionKey(receiver)
	if receiverKey == "" {
		return false
	}
	boundary := token.NoPos
	ast.Inspect(body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || assignment.Pos() <= sourceAssignment {
			return true
		}
		for _, target := range assignment.Lhs {
			invalidatesSource := false
			if identifier, ok := target.(*ast.Ident); ok {
				invalidatesSource = collectionAuditSameBinding(identifier, source)
			}
			targetKey := collectionAuditAssignmentRootKey(target)
			invalidatesDestination := assignment.Pos() > appendPosition &&
				(targetKey == receiverKey || (targetKey != "" && strings.HasPrefix(receiverKey, targetKey+".")))
			if invalidatesSource || invalidatesDestination {
				if boundary == token.NoPos || assignment.Pos() < boundary {
					boundary = assignment.Pos()
				}
			}
		}
		return true
	})

	propagated := false
	ast.Inspect(body, func(node ast.Node) bool {
		if propagated || node == nil || node.Pos() <= appendPosition || (boundary != token.NoPos && node.Pos() >= boundary) {
			return !propagated
		}
		switch typed := node.(type) {
		case *ast.ReturnStmt:
			for _, result := range typed.Results {
				if collectionAuditExpressionKey(result) == receiverKey {
					propagated = true
				}
			}
		case *ast.CallExpr:
			selector, ok := typed.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "HasError" && collectionAuditExpressionKey(selector.X) == receiverKey && collectionDiagnosticCallControlsFailureExit(typed, parents) {
				propagated = true
			}
		}
		return !propagated
	})
	return propagated
}

func collectionAuditExpressionKey(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		if typed.Obj != nil {
			return fmt.Sprintf("%p:%s", typed.Obj, typed.Name)
		}
		return typed.Name
	case *ast.SelectorExpr:
		prefix := collectionAuditExpressionKey(typed.X)
		if prefix == "" {
			return ""
		}
		return prefix + "." + typed.Sel.Name
	case *ast.ParenExpr:
		return collectionAuditExpressionKey(typed.X)
	default:
		return ""
	}
}

func collectionAuditAssignmentRootKey(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.IndexExpr:
		return collectionAuditAssignmentRootKey(typed.X)
	case *ast.StarExpr:
		return collectionAuditAssignmentRootKey(typed.X)
	default:
		return collectionAuditExpressionKey(expression)
	}
}

func collectionDiagnosticCallControlsFailureExit(call *ast.CallExpr, parents map[ast.Node]ast.Node) bool {
	var conditional *ast.IfStmt
	for node := ast.Node(call); node != nil; node = parents[node] {
		switch typed := node.(type) {
		case *ast.ReturnStmt:
			return true
		case *ast.IfStmt:
			conditional = typed
			node = nil
		case *ast.ExprStmt:
			return false
		}
		if conditional != nil {
			break
		}
	}
	if conditional == nil {
		return false
	}

	switch function := call.Fun.(type) {
	case *ast.SelectorExpr:
		if function.Sel.Name == "HasError" {
			return collectionAuditPositiveBoolCheck(conditional.Cond, call) &&
				collectionAuditBlockPropagatesFailure(conditional.Body, collectionAuditExpressionKey(function.X))
		}
	case *ast.Ident:
		switch function.Name {
		case "len":
			return collectionAuditPositiveLengthCheck(conditional.Cond, call) && collectionAuditBlockPropagatesFailure(conditional.Body, "")
		case "collectionProjectionError":
			assignment, ok := conditional.Init.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) == 0 || len(assignment.Rhs) != 1 || assignment.Rhs[0] != call {
				return false
			}
			identifier, ok := assignment.Lhs[len(assignment.Lhs)-1].(*ast.Ident)
			return ok && collectionAuditNonNilCheck(conditional.Cond, identifier.Name) && collectionAuditBlockPropagatesFailure(conditional.Body, "")
		}
	}
	return false
}

func collectionAuditBlockPropagatesFailure(block *ast.BlockStmt, checkedContainer string) bool {
	for _, statement := range block.List {
		returned, ok := statement.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		if strings.HasSuffix(checkedContainer, ".Diagnostics") {
			return true
		}
		for _, result := range returned.Results {
			if identifier, ok := result.(*ast.Ident); !ok || identifier.Name != "nil" {
				return true
			}
		}
	}
	return false
}

func collectionAuditPositiveBoolCheck(expression ast.Expr, target *ast.CallExpr) bool {
	switch typed := expression.(type) {
	case *ast.ParenExpr:
		return collectionAuditPositiveBoolCheck(typed.X, target)
	case *ast.CallExpr:
		return typed == target
	case *ast.BinaryExpr:
		if typed.Op == token.LOR {
			return collectionAuditPositiveBoolCheck(typed.X, target) || collectionAuditPositiveBoolCheck(typed.Y, target)
		}
		if typed.Op == token.EQL || typed.Op == token.NEQ {
			if typed.X == target {
				if value, ok := collectionAuditBoolLiteral(typed.Y); ok {
					return (typed.Op == token.EQL && value) || (typed.Op == token.NEQ && !value)
				}
			}
			if typed.Y == target {
				if value, ok := collectionAuditBoolLiteral(typed.X); ok {
					return (typed.Op == token.EQL && value) || (typed.Op == token.NEQ && !value)
				}
			}
		}
	}
	return false
}

func collectionAuditPositiveLengthCheck(expression ast.Expr, target *ast.CallExpr) bool {
	typed, ok := expression.(*ast.BinaryExpr)
	if !ok {
		if parenthesized, parenthesizedOK := expression.(*ast.ParenExpr); parenthesizedOK {
			return collectionAuditPositiveLengthCheck(parenthesized.X, target)
		}
		return false
	}
	if typed.Op == token.LOR {
		return collectionAuditPositiveLengthCheck(typed.X, target) || collectionAuditPositiveLengthCheck(typed.Y, target)
	}
	if typed.X == target && collectionAuditIntegerLiteralIsZero(typed.Y) {
		return typed.Op == token.NEQ || typed.Op == token.GTR
	}
	if typed.Y == target && collectionAuditIntegerLiteralIsZero(typed.X) {
		return typed.Op == token.NEQ || typed.Op == token.LSS
	}
	return false
}

func collectionAuditNonNilCheck(expression ast.Expr, name string) bool {
	typed, ok := expression.(*ast.BinaryExpr)
	if !ok {
		if parenthesized, parenthesizedOK := expression.(*ast.ParenExpr); parenthesizedOK {
			return collectionAuditNonNilCheck(parenthesized.X, name)
		}
		return false
	}
	if typed.Op == token.LOR {
		return collectionAuditNonNilCheck(typed.X, name) || collectionAuditNonNilCheck(typed.Y, name)
	}
	if typed.Op != token.NEQ {
		return false
	}
	left, leftOK := typed.X.(*ast.Ident)
	right, rightOK := typed.Y.(*ast.Ident)
	return (leftOK && left.Name == name && rightOK && right.Name == "nil") ||
		(rightOK && right.Name == name && leftOK && left.Name == "nil")
}

func collectionAuditBoolLiteral(expression ast.Expr) (bool, bool) {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false, false
	}
	switch identifier.Name {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func collectionAuditIntegerLiteralIsZero(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == "0"
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
	case "ListValue", "SetValue", "MapValue", "ObjectValue", "ListValueFrom", "SetValueFrom", "MapValueFrom", "ObjectValueFrom", "NewListValue", "NewSetValue", "NewMapValue", "NewObjectValue":
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
