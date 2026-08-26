package contractapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	pinnedRepository        = "https://github.com/BerriAI/litellm"
	pinnedTag               = "v1.98.0"
	pinnedCommit            = "d8f71d7bdbd7c9873d98293f83d64c6db72847e6"
	pinnedPython            = "3.12.14"
	pinnedUV                = "0.12.6"
	pinnedUVLockSHA256      = "a7cc57875c67de85bbae0f82b834f31fc9d0c029073ef29e0883787a31a985e8"
	pinnedLazyFeatureSHA256 = "a937cdd378769502f22840c501cb992a1fab7d4609c1deb402e81095fa9837ff"
)

var httpMethods = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT", "MethodPatch": "PATCH",
	"MethodDelete": "DELETE", "MethodHead": "HEAD", "MethodOptions": "OPTIONS",
}

var clientRequestMethods = map[string]int{
	"DoRequest": 2, "DoRequestWithResponse": 2, "DoReadWithResponse": 2, "doRequestWithResponse": 2,
	"doRequestWithResponseOptions": 2, "doFreshRequestWithResponse": 2,
}

var helperRequestWrappers = map[string]int{
	"fetchTopLevelListObjects": 2, "fetchEnvelopeListObjects": 2, "readModelDataSourceWithRetry": 2,
	"readPromptDataSourceWithRetry": 2, "probeCredentialEndpoint": 2,
}

var approvedClientTransportInternals = map[string]bool{
	"prepareRequest": true, "executeRequest": true, "executeRequestWithOptions": true,
	"doReadWithResponsePolicy": true,
}

type Evidence struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

type Operation struct {
	Method          string     `json:"method"`
	Path            string     `json:"path"`
	PathParameters  []string   `json:"path_parameters"`
	QueryParameters []string   `json:"query_parameters"`
	Evidence        []Evidence `json:"evidence"`
	pathMode        string
}

type Artifact struct {
	SchemaVersion  int                     `json:"schema_version"`
	UpstreamCommit string                  `json:"upstream_commit"`
	LazyFeatures   []LazyFeatureEvidence   `json:"lazy_features"`
	Routes         []SupplementalOperation `json:"routes"`
}

type LazyFeatureContract struct {
	Name                  string   `json:"name"`
	Module                string   `json:"module"`
	PathPrefixes          []string `json:"path_prefixes"`
	PathSuffixes          []string `json:"path_suffixes"`
	Registration          string   `json:"registration"`
	Attribute             string   `json:"attribute"`
	MountPrefix           string   `json:"mount_prefix"`
	PersistentSwaggerStub bool     `json:"persistent_swagger_stub"`
}

type LazyFeatureEvidence struct {
	LazyFeatureContract
	LiveOperationCount    int `json:"live_operation_count"`
	OpenAPIOperationCount int `json:"openapi_operation_count"`
}

type SupplementalOperation struct {
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	PathParameters  []string `json:"path_parameters"`
	QueryParameters []string `json:"query_parameters"`
	Evidence        string   `json:"evidence,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

type Manifest struct {
	SchemaVersion int `json:"schema_version"`
	Upstream      struct {
		Repository   string `json:"repository"`
		Tag          string `json:"tag"`
		Commit       string `json:"commit"`
		Python       string `json:"python"`
		UV           string `json:"uv"`
		UVLockSHA256 string `json:"uv_lock_sha256"`
	} `json:"upstream"`
	GenerationCommand string `json:"generation_command"`
	OpenAPI           struct {
		Path           string `json:"path"`
		SHA256         string `json:"sha256"`
		PathCount      int    `json:"path_count"`
		OperationCount int    `json:"operation_count"`
	} `json:"openapi"`
	Supplemental struct {
		Path       string `json:"path"`
		SHA256     string `json:"sha256"`
		RouteCount int    `json:"route_count"`
	} `json:"supplemental"`
	RequiredLazyFeatures []LazyFeatureEvidence `json:"required_lazy_features"`
	Operations           []Operation           `json:"provider_operations"`
	ProviderGolden       struct {
		Path           string `json:"path"`
		SHA256         string `json:"sha256"`
		OperationCount int    `json:"operation_count"`
	} `json:"provider_golden"`
	Classification struct {
		Path                    string `json:"path"`
		SHA256                  string `json:"sha256"`
		OperationCount          int    `json:"operation_count"`
		UnsupportedDurableCount int    `json:"unsupported_durable"`
		ExcludedNonDurableCount int    `json:"excluded_non_durable"`
	} `json:"classification"`
}

type ReviewedPins struct {
	SchemaVersion int `json:"schema_version"`
	Upstream      struct {
		Repository   string `json:"repository"`
		Tag          string `json:"tag"`
		Commit       string `json:"commit"`
		Python       string `json:"python"`
		UV           string `json:"uv"`
		UVLockSHA256 string `json:"uv_lock_sha256"`
	} `json:"upstream"`
	Artifacts struct {
		OpenAPI        ArtifactPin `json:"openapi"`
		Supplemental   ArtifactPin `json:"supplemental"`
		Manifest       ArtifactPin `json:"manifest"`
		ProviderGolden ArtifactPin `json:"provider_golden"`
		Classification ArtifactPin `json:"classification"`
	} `json:"artifacts"`
	LazyFeatures []LazyFeatureContract `json:"lazy_features"`
	Categories   []CategoryDefinition  `json:"categories"`
}

type ArtifactPin struct {
	Path                    string `json:"path"`
	SHA256                  string `json:"sha256"`
	PathCount               int    `json:"path_count,omitempty"`
	OperationCount          int    `json:"operation_count,omitempty"`
	UnsupportedDurableCount int    `json:"unsupported_durable_count,omitempty"`
	ExcludedNonDurableCount int    `json:"excluded_non_durable_count,omitempty"`
}

type CategoryDefinition struct {
	ID          string `json:"id"`
	Disposition string `json:"disposition"`
	Issue       string `json:"issue,omitempty"`
	Rationale   string `json:"rationale"`
}

type ReviewedClassification struct {
	SchemaVersion int                   `json:"schema_version"`
	Operations    []ClassifiedOperation `json:"operations"`
}

type ClassifiedOperation struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Category string `json:"category"`
}

type contractOperation struct {
	method      string
	path        string
	pathParams  []string
	queryParams map[string]bool
}

type value struct {
	shapes           []string
	queries          map[string]bool
	pathModes        map[string]bool
	unresolvedQuery  bool
	canonicalBuilder bool
	ok               bool
}

func literalValue(s string) value {
	return value{shapes: []string{s}, queries: map[string]bool{}, pathModes: map[string]bool{}, ok: true}
}
func dynamicValue() value { return literalValue("{}") }

func mergeValues(values ...value) value {
	out := value{queries: map[string]bool{}, pathModes: map[string]bool{}, canonicalBuilder: true, ok: true}
	seen := map[string]bool{}
	hasValue := false
	for _, v := range values {
		if !v.ok {
			out.ok = false
			out.canonicalBuilder = false
			continue
		}
		hasValue = true
		out.canonicalBuilder = out.canonicalBuilder && v.canonicalBuilder
		for _, s := range v.shapes {
			if !seen[s] {
				seen[s] = true
				out.shapes = append(out.shapes, s)
			}
		}
		for q := range v.queries {
			out.queries[q] = true
		}
		for mode := range v.pathModes {
			out.pathModes[mode] = true
		}
		out.unresolvedQuery = out.unresolvedQuery || v.unresolvedQuery
	}
	if !hasValue {
		out.canonicalBuilder = false
	}
	sort.Strings(out.shapes)
	return out
}

type extractor struct {
	root          string
	fset          *token.FileSet
	files         map[string]*ast.File
	funcDecls     map[*types.Func]*ast.FuncDecl
	funcFiles     map[*types.Func]string
	constants     map[string]string
	clientMethods map[*types.Func]*ast.FuncDecl
	typesInfo     *types.Info
}

type callTarget struct {
	name       string
	pathIndex  int
	bound      bool
	clientCall bool
	helperCall bool
}

func ExtractProvider(root string) ([]Operation, error) {
	x := &extractor{
		root: root, fset: token.NewFileSet(), files: map[string]*ast.File{},
		funcDecls: map[*types.Func]*ast.FuncDecl{}, funcFiles: map[*types.Func]string{},
		constants: map[string]string{}, clientMethods: map[*types.Func]*ast.FuncDecl{},
		typesInfo: &types.Info{
			Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{},
			Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{},
			Instances: map[*ast.Ident]types.Instance{},
		},
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		file, parseErr := parser.ParseFile(x.fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, parseErr
		}
		x.files[path] = file
		for _, decl := range file.Decls {
			switch node := decl.(type) {
			case *ast.GenDecl:
				if node.Tok == token.CONST {
					x.collectConstants(node)
				}
			}
		}
	}

	parsedFiles := make([]*ast.File, 0, len(x.files))
	for _, file := range x.files {
		parsedFiles = append(parsedFiles, file)
	}
	sort.Slice(parsedFiles, func(i, j int) bool {
		return x.fset.Position(parsedFiles[i].Pos()).Filename < x.fset.Position(parsedFiles[j].Pos()).Filename
	})
	var typeDiagnostics []string
	config := &types.Config{Importer: sourceImporter(x.fset), Error: func(err error) {
		typeDiagnostics = append(typeDiagnostics, err.Error())
	}}
	_, checkErr := config.Check("github.com/nicholas-cecere/terraform-provider-litellm/internal/provider", x.fset, parsedFiles, x.typesInfo)
	if checkErr != nil || len(typeDiagnostics) != 0 {
		if checkErr != nil && len(typeDiagnostics) == 0 {
			typeDiagnostics = append(typeDiagnostics, checkErr.Error())
		}
		sort.Strings(typeDiagnostics)
		return nil, fmt.Errorf("provider source must be a complete type-correct package:\n%s", strings.Join(typeDiagnostics, "\n"))
	}
	for path, file := range x.files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			function, _ := x.typesInfo.Defs[fn.Name].(*types.Func)
			if function == nil {
				continue
			}
			x.funcDecls[function] = fn
			x.funcFiles[function] = path
			signature := function.Type().(*types.Signature)
			if signature.Recv() != nil && x.isClientType(signature.Recv().Type()) {
				x.clientMethods[function] = fn
			}
		}
	}

	if err := x.validateStrictSourcePolicy(); err != nil {
		return nil, err
	}
	var extracted []Operation
	var problems []string
	for path, file := range x.files {
		base := filepath.Base(path)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			function, _ := x.typesInfo.Defs[fn.Name].(*types.Func)
			if x.clientMethods[function] != nil || exactProviderFunction(function, helperRequestWrappers) {
				continue
			}
			env := x.functionEnv(fn, nil, map[string]bool{})
			aliases := x.functionCallAliases(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := callName(call.Fun)
				pos := x.fset.Position(call.Pos())
				if (name == "listKeys" || name == "listUsers") && len(call.Args) > 2 {
					filters := x.eval(call.Args[2], env, map[string]bool{})
					query := map[string]bool{}
					for key := range filters.queries {
						query[key] = true
					}
					path := "/key/list"
					for _, key := range []string{"page", "size", "return_full_object", "sort_by", "sort_order"} {
						query[key] = true
					}
					if name == "listUsers" {
						path = "/user/list"
						query = map[string]bool{"page": true, "page_size": true}
						for key := range filters.queries {
							query[key] = true
						}
					}
					extracted = append(extracted, Operation{Method: "GET", Path: path, QueryParameters: sortedKeys(query), Evidence: []Evidence{{File: "internal/provider/" + base, Line: pos.Line}}})
					return true
				}
				target, isRequest := x.resolveCallTarget(call, fn, aliases)
				if !isRequest {
					return true
				}
				pathIndex, isClientRequest := target.pathIndex, target.clientCall
				if len(call.Args) <= pathIndex {
					problems = append(problems, fmt.Sprintf("%s:%d: malformed HTTP wrapper call", base, pos.Line))
					return true
				}
				method := "GET"
				if isClientRequest {
					method = x.evalMethod(call.Args[pathIndex-1], env)
				}
				pv := x.eval(call.Args[pathIndex], env, map[string]bool{})
				if method == "" || !pv.ok || len(pv.shapes) == 0 {
					problems = append(problems, fmt.Sprintf("%s:%d: unresolved HTTP method or path", base, pos.Line))
					return true
				}
				pathMode := ""
				for mode := range pv.pathModes {
					if pathMode != "" && pathMode != mode {
						problems = append(problems, fmt.Sprintf("%s:%d: mixed ordinary and capture path builders", base, pos.Line))
						pathMode = "mixed"
						break
					}
					pathMode = mode
				}
				for _, shape := range pv.shapes {
					pathShape, query, unresolvedQuery := splitShape(shape)
					for q := range pv.queries {
						query[q] = true
					}
					if pathShape == "{}" || !strings.HasPrefix(pathShape, "/") || pv.unresolvedQuery || unresolvedQuery {
						problems = append(problems, fmt.Sprintf("%s:%d: unresolved dynamic HTTP path or query name", base, pos.Line))
						continue
					}
					extracted = append(extracted, Operation{Method: method, Path: pathShape, QueryParameters: sortedKeys(query), Evidence: []Evidence{{File: "internal/provider/" + base, Line: pos.Line}}, pathMode: pathMode})
				}
				return true
			})
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	combined := combineOperations(extracted)
	for _, operation := range combined {
		if operation.pathMode == "mixed" {
			return nil, fmt.Errorf("%s %s mixes ordinary and capture path builders", operation.Method, operation.Path)
		}
	}
	return combined, nil
}

func sourceImporter(fset *token.FileSet) types.Importer {
	return importer.ForCompiler(fset, "gc", func(importPath string) (io.ReadCloser, error) {
		command := exec.Command("go", "list", "-export", "-f", "{{.Export}}", importPath)
		command.Env = append(os.Environ(), "GOPROXY=off")
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("resolve import %q: %w", importPath, err)
		}
		exportPath := strings.TrimSpace(string(output))
		if exportPath == "" {
			return nil, fmt.Errorf("resolve import %q: go list returned no export data", importPath)
		}
		return os.Open(exportPath)
	})
}

func (x *extractor) collectConstants(decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		vs := spec.(*ast.ValueSpec)
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			if lit, ok := stringLiteral(vs.Values[i]); ok {
				x.constants[name.Name] = lit
			}
		}
	}
}

func (x *extractor) isClientType(t types.Type) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if pointer, ok := t.(*types.Pointer); ok {
		t = types.Unalias(pointer.Elem())
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Name() == "Client" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "github.com/nicholas-cecere/terraform-provider-litellm/internal/provider"
}

func (x *extractor) selectorHasClientReceiver(selector *ast.SelectorExpr) (bool, bool) {
	if selection := x.typesInfo.Selections[selector]; selection != nil {
		if x.isClientType(selection.Recv()) {
			return true, selection.Kind() == types.MethodVal
		}
	}
	return x.isClientType(x.typesInfo.TypeOf(selector.X)), true
}

func (x *extractor) callHasClientReceiver(call *ast.CallExpr, _ *ast.FuncDecl) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	client, _ := x.selectorHasClientReceiver(selector)
	return client
}

func (x *extractor) targetFromExpr(expr ast.Expr, fn *ast.FuncDecl) (callTarget, bool) {
	switch node := expr.(type) {
	case *ast.SelectorExpr:
		name := node.Sel.Name
		if pathIndex, ok := clientRequestMethods[name]; ok {
			if client, bound := x.selectorHasClientReceiver(node); client {
				if !bound {
					pathIndex++
				}
				return callTarget{name: name, pathIndex: pathIndex, bound: bound, clientCall: true}, true
			}
			fake := &ast.CallExpr{Fun: node}
			if x.callHasClientReceiver(fake, fn) {
				return callTarget{name: name, pathIndex: pathIndex, bound: true, clientCall: true}, true
			}
		}
	case *ast.Ident:
		if pathIndex, ok := helperRequestWrappers[node.Name]; ok && exactProviderFunction(x.typesInfo.ObjectOf(node), helperRequestWrappers) {
			return callTarget{name: node.Name, pathIndex: pathIndex, helperCall: true}, true
		}
	}
	return callTarget{}, false
}

func (x *extractor) functionCallAliases(fn *ast.FuncDecl) map[string]callTarget {
	aliases := map[string]callTarget{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		var left []ast.Expr
		var right []ast.Expr
		switch statement := node.(type) {
		case *ast.AssignStmt:
			left, right = statement.Lhs, statement.Rhs
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, item := range declaration.Specs {
				specification, ok := item.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range specification.Names {
					if i < len(specification.Values) {
						if target, found := x.targetFromExpr(specification.Values[i], fn); found {
							aliases[name.Name] = target
						}
					}
				}
			}
			return true
		default:
			return true
		}
		for i, expression := range left {
			identifier, ok := expression.(*ast.Ident)
			if !ok || i >= len(right) {
				continue
			}
			if target, found := x.targetFromExpr(right[i], fn); found {
				aliases[identifier.Name] = target
			} else if source, ok := right[i].(*ast.Ident); ok {
				if target, found := aliases[source.Name]; found {
					aliases[identifier.Name] = target
				}
			}
		}
		return true
	})
	return aliases
}

func (x *extractor) resolveCallTarget(call *ast.CallExpr, fn *ast.FuncDecl, aliases map[string]callTarget) (callTarget, bool) {
	if identifier, ok := call.Fun.(*ast.Ident); ok {
		if target, found := aliases[identifier.Name]; found {
			return target, true
		}
	}
	return x.targetFromExpr(call.Fun, fn)
}

func (x *extractor) rawHTTPCallName(call *ast.CallExpr) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return callName(call.Fun), false
	}
	name := selector.Sel.Name
	selection := x.typesInfo.Selections[selector]
	object := x.typesInfo.Uses[selector.Sel]
	if object == nil && selection != nil {
		object = selection.Obj()
	}
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "net/http" {
		return name, false
	}
	if selection == nil {
		packageFunctions := map[string]bool{
			"NewRequest": true, "NewRequestWithContext": true, "Get": true,
			"Post": true, "PostForm": true, "Head": true,
		}
		return name, packageFunctions[name]
	}
	receiver := selection.Recv()
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = pointer.Elem()
	}
	named, _ := receiver.(*types.Named)
	clientMethod := named != nil && named.Obj() != nil && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == "Client"
	return name, (clientMethod && map[string]bool{"Get": true, "Post": true, "PostForm": true, "Head": true, "Do": true}[name]) || name == "RoundTrip"
}

func (x *extractor) rawHTTPAliases(fn *ast.FuncDecl) map[string]string {
	aliases := map[string]string{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, left := range assignment.Lhs {
			name, ok := left.(*ast.Ident)
			if !ok || i >= len(assignment.Rhs) {
				continue
			}
			right := assignment.Rhs[i]
			if selector, ok := right.(*ast.SelectorExpr); ok {
				if rawName, raw := x.rawHTTPCallName(&ast.CallExpr{Fun: selector}); raw {
					aliases[name.Name] = rawName
				}
			} else if source, ok := right.(*ast.Ident); ok && aliases[source.Name] != "" {
				aliases[name.Name] = aliases[source.Name]
			}
		}
		return true
	})
	return aliases
}

func isInterfaceType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

func (x *extractor) isClientTransportObject(object types.Object) bool {
	function, ok := object.(*types.Func)
	if !ok || function.Type().(*types.Signature).Recv() == nil {
		return false
	}
	return x.isClientType(function.Type().(*types.Signature).Recv().Type()) && clientRequestMethods[function.Name()] != 0
}

func (x *extractor) isNetHTTPTransportObject(object types.Object) bool {
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "net/http" {
		return false
	}
	function, ok := object.(*types.Func)
	if !ok {
		return false
	}
	if function.Type().(*types.Signature).Recv() == nil {
		return map[string]bool{
			"NewRequest": true, "NewRequestWithContext": true, "Get": true,
			"Post": true, "PostForm": true, "Head": true,
		}[function.Name()]
	}
	receiver := function.Type().(*types.Signature).Recv().Type()
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = pointer.Elem()
	}
	named, _ := receiver.(*types.Named)
	if named == nil || named.Obj() == nil {
		return false
	}
	return function.Name() == "RoundTrip" ||
		(named.Obj().Name() == "Client" && map[string]bool{"Get": true, "Post": true, "PostForm": true, "Head": true, "Do": true}[function.Name()])
}

func (x *extractor) isDirectCall(selector *ast.SelectorExpr, parents map[ast.Node]ast.Node) bool {
	call, ok := parents[selector].(*ast.CallExpr)
	return ok && call.Fun == selector
}

func exactNamedType(t types.Type, packagePath, name string) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if pointer, ok := t.(*types.Pointer); ok {
		t = types.Unalias(pointer.Elem())
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == packagePath && named.Obj().Name() == name
}

func isTypeErasureDestination(t types.Type) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if _, ok := t.(*types.TypeParam); ok {
		return true
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

func originalGenericUnderlying(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	t = types.Unalias(t)
	named, ok := t.(*types.Named)
	if !ok {
		return t.Underlying()
	}
	return named.Origin().Underlying()
}

func (x *extractor) originalErasureDestination(expr ast.Expr) types.Type {
	switch item := expr.(type) {
	case *ast.SelectorExpr:
		if selection := x.typesInfo.Selections[item]; selection != nil && selection.Kind() == types.FieldVal {
			receiver := types.Unalias(x.typesInfo.TypeOf(item.X))
			if pointer, ok := receiver.(*types.Pointer); ok {
				receiver = types.Unalias(pointer.Elem())
			}
			for _, fieldIndex := range selection.Index() {
				structure, ok := originalGenericUnderlying(receiver).(*types.Struct)
				if !ok || fieldIndex < 0 || fieldIndex >= structure.NumFields() {
					return selection.Obj().Type()
				}
				field := structure.Field(fieldIndex)
				receiver = field.Type()
			}
			return receiver
		}
	case *ast.IndexExpr:
		switch container := originalGenericUnderlying(x.typesInfo.TypeOf(item.X)).(type) {
		case *types.Slice:
			return container.Elem()
		case *types.Array:
			return container.Elem()
		case *types.Map:
			return container.Elem()
		}
	}
	return x.typesInfo.TypeOf(expr)
}

func isSensitiveHTTPType(t types.Type) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if pointer, ok := t.(*types.Pointer); ok {
		t = types.Unalias(pointer.Elem())
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "net/http" {
		return false
	}
	return named.Obj().Name() == "Client" || named.Obj().Name() == "Transport" || named.Obj().Name() == "RoundTripper"
}

func clientTransportName(name string) bool {
	if _, ok := clientRequestMethods[name]; ok {
		return true
	}
	return approvedClientTransportInternals[name]
}

func reviewedTransportName(name string) bool {
	if clientTransportName(name) {
		return true
	}
	_, ok := helperRequestWrappers[name]
	return ok
}

func exactProviderFunction(object types.Object, names map[string]int) bool {
	function, ok := object.(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != "github.com/nicholas-cecere/terraform-provider-litellm/internal/provider" || function.Type().(*types.Signature).Recv() != nil {
		return false
	}
	_, ok = names[function.Name()]
	return ok
}

func (x *extractor) exactClientMethodObject(object types.Object) bool {
	function, ok := object.(*types.Func)
	if !ok {
		return false
	}
	signature := function.Type().(*types.Signature)
	return signature.Recv() != nil && x.isClientType(signature.Recv().Type()) && clientTransportName(function.Name())
}

func exactURLValuesType(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "net/url" && named.Obj().Name() == "Values"
}

func exactURLValuesCarrierType(t types.Type) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	for {
		pointer, ok := t.(*types.Pointer)
		if !ok {
			return exactURLValuesType(t)
		}
		t = types.Unalias(pointer.Elem())
	}
}

func exactURLValuesFlow(source, destination types.Type) bool {
	if !exactURLValuesCarrierType(source) || !exactURLValuesCarrierType(destination) {
		return false
	}
	return types.Identical(types.Unalias(source), types.Unalias(destination))
}

func (x *extractor) exactURLValuesMethod(object types.Object, names ...string) bool {
	function, ok := object.(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != "net/url" {
		return false
	}
	signature := function.Type().(*types.Signature)
	if signature.Recv() == nil || !exactNamedType(signature.Recv().Type(), "net/url", "Values") {
		return false
	}
	for _, name := range names {
		if function.Name() == name {
			return true
		}
	}
	return false
}

func exactURLEscapeFunction(object types.Object) (*types.Func, bool) {
	function, ok := object.(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != "net/url" || (function.Name() != "PathEscape" && function.Name() != "QueryEscape") {
		return nil, false
	}
	signature := function.Type().(*types.Signature)
	if signature.Recv() != nil || signature.Params().Len() != 1 || signature.Results().Len() != 1 ||
		!types.Identical(signature.Params().At(0).Type(), types.Typ[types.String]) ||
		!types.Identical(signature.Results().At(0).Type(), types.Typ[types.String]) {
		return nil, false
	}
	return function, true
}

const (
	providerPackagePath          = "github.com/nicholas-cecere/terraform-provider-litellm/internal/provider"
	invalidReviewedEndpointValue = "/.terraform-provider-litellm-invalid-reviewed-endpoint"
)

var reviewedEndpointBuilders = map[string]bool{
	"endpointWithPathSegment":         true,
	"endpointWithPathCapture":         true,
	"endpointWithFallbackPathSegment": true,
	"endpointWithQuery":               true,
}

func exactProviderFreeFunction(object types.Object, names map[string]bool) (*types.Func, bool) {
	function, ok := object.(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || function.Type().(*types.Signature).Recv() != nil || !names[function.Name()] {
		return nil, false
	}
	return function, true
}

func (x *extractor) exactEndpointBuilder(object types.Object) bool {
	function, ok := exactProviderFreeFunction(object, reviewedEndpointBuilders)
	if !ok {
		return false
	}
	signature := function.Type().(*types.Signature)
	if signature.TypeParams() != nil && signature.TypeParams().Len() != 0 {
		return false
	}
	if signature.Results().Len() != 1 || !types.Identical(signature.Results().At(0).Type(), types.Typ[types.String]) {
		return false
	}
	if function.Name() == "endpointWithQuery" {
		return signature.Params().Len() == 2 && types.Identical(signature.Params().At(0).Type(), types.Typ[types.String]) && exactURLValuesType(signature.Params().At(1).Type())
	}
	return signature.Params().Len() == 3 &&
		types.Identical(signature.Params().At(0).Type(), types.Typ[types.String]) &&
		types.Identical(signature.Params().At(1).Type(), types.Typ[types.String]) &&
		types.Identical(signature.Params().At(2).Type(), types.Typ[types.String])
}

func (x *extractor) endpointBuilderParameter(fn *ast.FuncDecl, index int) types.Object {
	if fn.Type.Params == nil {
		return nil
	}
	seen := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if seen == index {
				return x.typesInfo.Defs[name]
			}
			seen++
		}
	}
	return nil
}

func (x *extractor) reviewedEscapeCallAllowed(object types.Object, call *ast.CallExpr, fn *ast.FuncDecl) bool {
	declared := x.typesInfo.Defs[fn.Name]
	if !x.exactEndpointBuilder(declared) || call == nil || call.Fun == nil {
		return false
	}
	if escape, ok := exactURLEscapeFunction(object); ok {
		if escape.Name() != "PathEscape" || fn.Name.Name == "endpointWithQuery" || len(call.Args) != 1 {
			return false
		}
		argument, ok := call.Args[0].(*ast.Ident)
		return ok && x.typesInfo.Uses[argument] == x.endpointBuilderParameter(fn, 1)
	}
	if !x.exactURLValuesMethod(object, "Encode") || fn.Name.Name != "endpointWithQuery" || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && x.typesInfo.Uses[receiver] == x.endpointBuilderParameter(fn, 1)
}

func (x *extractor) exactQueryHelper(object types.Object) bool {
	if x.exactEndpointBuilder(object) && object.Name() == "endpointWithQuery" {
		return true
	}
	_, ok := exactProviderFreeFunction(object, map[string]bool{
		"cloneURLValues": true, "safeListDiagnostic": true,
		"addKnownStringFilter": true, "listKeys": true, "listUsers": true,
	})
	return ok
}

func methodSetExposes(t types.Type, names func(string) bool) string {
	if t == nil {
		return ""
	}
	sets := []*types.MethodSet{types.NewMethodSet(t)}
	if _, pointer := types.Unalias(t).(*types.Pointer); !pointer {
		sets = append(sets, types.NewMethodSet(types.NewPointer(t)))
	}
	for _, set := range sets {
		for index := 0; index < set.Len(); index++ {
			if name := set.At(index).Obj().Name(); names(name) {
				return name
			}
		}
	}
	return ""
}

func (x *extractor) functionHasProviderReceiver(fn *ast.FuncDecl, receiver string) bool {
	if fn == nil {
		return false
	}
	function, _ := x.typesInfo.Defs[fn.Name].(*types.Func)
	if function == nil {
		return false
	}
	signature := function.Type().(*types.Signature)
	return signature.Recv() != nil && exactNamedType(signature.Recv().Type(), providerPackagePath, receiver)
}

func (x *extractor) isReviewedHTTPImplementation(fn *ast.FuncDecl) bool {
	if x.functionHasProviderReceiver(fn, "Client") && approvedClientTransportInternals[fn.Name.Name] {
		return true
	}
	return x.functionHasProviderReceiver(fn, "LiteLLMProvider") && fn.Name.Name == "Configure"
}

func (x *extractor) isFrameworkProviderDataAssertion(assertion *ast.TypeAssertExpr, fn *ast.FuncDecl) bool {
	star, ok := assertion.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	clientName, ok := star.X.(*ast.Ident)
	if !ok || clientName.Name != "Client" || !x.isClientType(x.typesInfo.TypeOf(assertion.Type)) || fn.Name.Name != "Configure" {
		return false
	}
	selector, ok := assertion.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ProviderData" {
		return false
	}
	request, ok := selector.X.(*ast.Ident)
	if !ok || request.Name != "req" {
		return false
	}
	requestType := x.typesInfo.TypeOf(request)
	return exactNamedType(requestType, "github.com/hashicorp/terraform-plugin-framework/resource", "ConfigureRequest") ||
		exactNamedType(requestType, "github.com/hashicorp/terraform-plugin-framework/datasource", "ConfigureRequest")
}

func (x *extractor) isReviewedHTTPTransportAssertion(assertion *ast.TypeAssertExpr, fn *ast.FuncDecl) bool {
	if !x.functionHasProviderReceiver(fn, "Client") || fn.Name.Name != "executeRequestWithOptions" {
		return false
	}
	star, ok := assertion.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := star.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Transport" || !exactNamedType(x.typesInfo.TypeOf(assertion.Type), "net/http", "Transport") {
		return false
	}
	transport, ok := assertion.X.(*ast.Ident)
	return ok && transport.Name == "transport" && exactNamedType(x.typesInfo.TypeOf(transport), "net/http", "RoundTripper")
}

func (x *extractor) isFrameworkProviderDataPublication(left, right ast.Expr, fn *ast.FuncDecl) bool {
	if !x.functionHasProviderReceiver(fn, "LiteLLMProvider") || fn.Name.Name != "Configure" {
		return false
	}
	client, ok := right.(*ast.Ident)
	if !ok || client.Name != "client" || !x.isClientType(x.typesInfo.TypeOf(client)) {
		return false
	}
	selector, ok := left.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "DataSourceData" && selector.Sel.Name != "ResourceData") {
		return false
	}
	response, ok := selector.X.(*ast.Ident)
	return ok && response.Name == "resp" && exactNamedType(x.typesInfo.TypeOf(response), "github.com/hashicorp/terraform-plugin-framework/provider", "ConfigureResponse")
}

func (x *extractor) reflectDynamicDispatch(object types.Object) bool {
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "reflect" {
		return false
	}
	return map[string]bool{"Method": true, "MethodByName": true, "Call": true, "CallSlice": true, "MakeFunc": true}[object.Name()]
}

func sameCallableSignature(left, right *types.Signature) bool {
	if left == nil || right == nil || left.Variadic() != right.Variadic() {
		return false
	}
	leftTypeParams, rightTypeParams := left.TypeParams(), right.TypeParams()
	if leftTypeParams != nil && leftTypeParams.Len() != 0 || rightTypeParams != nil && rightTypeParams.Len() != 0 {
		return false
	}
	return types.Identical(left.Params(), right.Params()) && types.Identical(left.Results(), right.Results())
}

func (x *extractor) endpointReachabilityClosed(body *ast.BlockStmt, visiting map[*types.Func]bool) (bool, bool) {
	if body == nil {
		return false, false
	}
	reaches, closed := false, true
	ast.Inspect(body, func(node ast.Node) bool {
		if reaches || !closed {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if x.typesInfo.Types[call.Fun].IsType() {
			return true
		}
		object := calledFunctionObject(x.typesInfo, call.Fun)
		if x.exactEndpointBuilder(object) {
			reaches = true
			return false
		}
		if _, builtin := object.(*types.Builtin); builtin {
			return true
		}
		function, exact := object.(*types.Func)
		if !exact {
			// Function variables, closures, and unresolved selectors are dynamic
			// targets. Their endpoint reachability cannot be proven closed.
			closed = false
			return false
		}
		signature := function.Type().(*types.Signature)
		if signatureUsesTypeParameters(signature) || signature.Recv() != nil && isInterfaceType(signature.Recv().Type()) {
			closed = false
			return false
		}
		if function.Pkg() == nil || function.Pkg().Path() != providerPackagePath {
			return true
		}
		declaration := x.funcDeclForObject(function)
		if declaration == nil {
			closed = false
			return false
		}
		if visiting[function] {
			return true
		}
		visiting[function] = true
		calleeReaches, calleeClosed := x.endpointReachabilityClosed(declaration.Body, visiting)
		delete(visiting, function)
		reaches = reaches || calleeReaches
		closed = closed && calleeClosed
		return !reaches && closed
	})
	return reaches, closed
}

func (x *extractor) interfaceEndpointDispatchCollision(function *types.Func) bool {
	if function == nil {
		return false
	}
	signature := function.Type().(*types.Signature)
	if signature.Recv() == nil || !isInterfaceType(signature.Recv().Type()) || signature.Results().Len() != 1 || !types.Identical(signature.Results().At(0).Type(), types.Typ[types.String]) {
		return false
	}
	hasStringInput := false
	for index := 0; index < signature.Params().Len(); index++ {
		hasStringInput = hasStringInput || types.Identical(signature.Params().At(index).Type(), types.Typ[types.String])
	}
	if !hasStringInput {
		return false
	}
	for candidate, declaration := range x.funcDecls {
		candidateSignature := candidate.Type().(*types.Signature)
		if candidate == function || candidate.Name() != function.Name() || candidateSignature.Recv() == nil || isInterfaceType(candidateSignature.Recv().Type()) || !sameCallableSignature(signature, candidateSignature) {
			continue
		}
		// Inspect the complete statically resolved local call graph. A concrete
		// method can otherwise hide its endpoint builder behind one or more free
		// function or method wrappers. funcDecls is complete before validation,
		// so this is independent of source declaration order. Unknown dynamic
		// targets fail closed because their reachability cannot be proven.
		reaches, closed := x.endpointReachabilityClosed(declaration.Body, map[*types.Func]bool{candidate: true})
		if reaches || !closed {
			return true
		}
	}
	return false
}

func (x *extractor) reviewedFreeFunctionDeclaration(fn *ast.FuncDecl, names map[string]bool) bool {
	if fn == nil {
		return false
	}
	_, exact := exactProviderFreeFunction(x.typesInfo.Defs[fn.Name], names)
	return exact
}

func (x *extractor) reviewedPaginationDeclaration(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	function, _ := x.typesInfo.Defs[fn.Name].(*types.Func)
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || !reviewedPaginationFunctions[function.Name()] {
		return false
	}
	signature := function.Type().(*types.Signature)
	if function.Name() == "findExistingUserByExactEmail" {
		return signature.Recv() != nil && exactNamedType(signature.Recv().Type(), providerPackagePath, "UserResource")
	}
	return signature.Recv() == nil
}

func (x *extractor) functionParameterObject(fn *ast.FuncDecl, object types.Object) bool {
	if fn == nil || fn.Type.Params == nil || object == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if x.typesInfo.Defs[name] == object {
				return true
			}
		}
	}
	return false
}

func (x *extractor) variableSources(fn *ast.FuncDecl, object types.Object) []ast.Expr {
	var sources []ast.Expr
	if fn == nil || fn.Body == nil || object == nil {
		return sources
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.AssignStmt:
			for index, left := range item.Lhs {
				identifier, ok := left.(*ast.Ident)
				if !ok || (x.typesInfo.Defs[identifier] != object && x.typesInfo.Uses[identifier] != object) {
					continue
				}
				if len(item.Rhs) == len(item.Lhs) {
					sources = append(sources, item.Rhs[index])
				} else if len(item.Rhs) == 1 {
					sources = append(sources, item.Rhs[0])
				}
			}
		case *ast.DeclStmt:
			declaration, _ := item.Decl.(*ast.GenDecl)
			if declaration == nil {
				return true
			}
			for _, specification := range declaration.Specs {
				values, _ := specification.(*ast.ValueSpec)
				if values == nil || len(values.Names) != len(values.Values) {
					continue
				}
				for index, name := range values.Names {
					if x.typesInfo.Defs[name] == object {
						sources = append(sources, values.Values[index])
					}
				}
			}
		}
		return true
	})
	return sources
}

func exactEmptyInterface(t types.Type) bool {
	iface, ok := types.Unalias(t).Underlying().(*types.Interface)
	return ok && iface.NumMethods() == 0 && iface.NumEmbeddeds() == 0
}

func exactTerraformValueString(object types.Object) bool {
	function, ok := object.(*types.Func)
	if !ok || function.Name() != "ValueString" || function.Pkg() == nil || function.Pkg().Path() != "github.com/hashicorp/terraform-plugin-framework/types/basetypes" {
		return false
	}
	signature := function.Type().(*types.Signature)
	return signature.Recv() != nil && exactNamedType(signature.Recv().Type(), "github.com/hashicorp/terraform-plugin-framework/types/basetypes", "StringValue") &&
		signature.Params().Len() == 0 && signature.Results().Len() == 1 && types.Identical(signature.Results().At(0).Type(), types.Typ[types.String])
}

var reviewedQueryValueHelpers = map[string]bool{
	"cloneURLValues": true, "addKnownStringFilter": true,
	"keyListFilters": true, "userListFilters": true, "teamListFilters": true,
	"organizationListFilters": true, "modelListFilters": true,
}

var reviewedPaginationFunctions = map[string]bool{
	"listKeys": true, "listUsers": true, "listJWTKeyMappings": true,
	"listUnifiedAccessGroupKeys": true, "findExistingUserByExactEmail": true,
}

// These hashes pin a closed semantic dependency proof for the small set of
// raw-identity helpers and promptEnvironment. Any implementation or dependency
// change must be independently reviewed before its new proof is admitted here.
var reviewedRawIdentityProofs = map[string]string{
	"keyDataSourceLookup":          "66b45bdf6345c6fd53c26d179d19b4aaf2d4e93603482bd47fe4d19f6ca2b409",
	"keyLookupIdentifier":          "bfd8cead959c92f655df4cebf8816b216b3517b2b5d5ea5b57c08b8ad3e7a773",
	"keyBlockStateIdentity":        "e2d298e1bd668b2b7e25c1037bf00a41f193b5ba89b1d72584914114fa3fb0ca",
	"batchTeamID":                  "c3e1cc482481411c1551448ae66a86a7b1f1ab15541157598ecd4b6c079b1884",
	"teamMemberConfiguredIdentity": "edb6d37deb5a402facf3f29e8f9371fb641c0c8bfdb5075b93fdf103a442a923",
}

var reviewedPromptEnvironmentProof = "6402f03748d72e305b05e40a59fa8c787eb81ab8abf735a3e86a5d03715e275f"

func canonicalTypeString(t types.Type) string {
	if t == nil {
		return "<nil>"
	}
	return types.TypeString(t, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	})
}

func canonicalObjectString(object types.Object) string {
	if object == nil || reflect.ValueOf(object).Kind() == reflect.Pointer && reflect.ValueOf(object).IsNil() {
		return "<nil>"
	}
	packagePath := ""
	if object.Pkg() != nil {
		packagePath = object.Pkg().Path()
	}
	value := ""
	if object, ok := object.(*types.Const); ok {
		// A constant's name and type do not prove its semantic value. ExactString
		// is stable for all constant kinds and makes every used constant value a
		// reviewed part of the proof.
		value = "|VALUE:" + object.Val().ExactString()
	}
	return fmt.Sprintf("%T|%s|%s|%s%s", object, packagePath, object.Name(), canonicalTypeString(object.Type()), value)
}

func exactApprovedRawProofExternalFunction(function *types.Func) bool {
	if function == nil || function.Pkg() == nil {
		return false
	}
	approved := map[string]map[string]bool{
		"crypto/sha256": {"Sum256": true},
		"encoding/hex":  {"DecodeString": true},
		"errors":        {"New": true},
		"fmt":           {"Errorf": true, "Sprintf": true},
		"strings":       {"HasPrefix": true, "ToLower": true, "TrimPrefix": true},
	}
	return approved[function.Pkg().Path()][function.Name()]
}

func exactApprovedRawProofMethod(function *types.Func) bool {
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != "github.com/hashicorp/terraform-plugin-framework/types/basetypes" {
		return false
	}
	signature := function.Type().(*types.Signature)
	return signature.Recv() != nil && exactNamedType(signature.Recv().Type(), "github.com/hashicorp/terraform-plugin-framework/types/basetypes", "StringValue") &&
		map[string]bool{"IsNull": true, "IsUnknown": true, "ValueString": true}[function.Name()]
}

func signatureUsesTypeParameters(signature *types.Signature) bool {
	if signature == nil {
		return true
	}
	return signature.TypeParams() != nil && signature.TypeParams().Len() != 0 || signature.RecvTypeParams() != nil && signature.RecvTypeParams().Len() != 0
}

// canonicalRawIdentityProof constructs a proof only when the helper's entire
// semantic dependency closure is available. It intentionally accepts a small
// pure language: parameters, locals, field reads, literals, constants (with
// values), exact conversions/builtins, exact approved external pure calls, and
// recursively proven package-local free functions. Everything whose runtime
// meaning can live elsewhere fails closed.
func (x *extractor) canonicalRawIdentityProof(function *types.Func, visiting map[*types.Func]bool) (string, bool) {
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath {
		return "", false
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() != nil || signatureUsesTypeParameters(signature) || visiting[function] {
		return "", false
	}
	declaration := x.funcDeclForObject(function)
	if declaration == nil || declaration.Body == nil || declaration.Type.TypeParams != nil {
		return "", false
	}
	visiting[function] = true
	defer delete(visiting, function)

	var canonical strings.Builder
	if err := format.Node(&canonical, x.fset, declaration); err != nil {
		return "", false
	}
	fmt.Fprintf(&canonical, "\nDECL:%s\n", canonicalObjectString(function))
	valid := true
	ast.Inspect(declaration, func(node ast.Node) bool {
		if !valid {
			return false
		}
		switch item := node.(type) {
		case *ast.FuncLit:
			// Function values and closures have bodies and captures that cannot be
			// represented as an exact statically called free-function dependency.
			valid = false
			return false
		case *ast.SelectorExpr:
			selection := x.typesInfo.Selections[item]
			if selection == nil && x.typesInfo.Uses[item.Sel] == nil {
				valid = false
				return false
			}
		case *ast.Ident:
			object := x.typesInfo.ObjectOf(item)
			fmt.Fprintf(&canonical, "IDENT:%s:%s\n", item.Name, canonicalObjectString(object))
			if _, instantiated := x.typesInfo.Instances[item]; instantiated {
				valid = false
				return false
			}
			if object == nil {
				// nil, true, false, and the blank identifier have no types.Object.
				if item.Name != "nil" && item.Name != "true" && item.Name != "false" && item.Name != "_" {
					valid = false
				}
				return valid
			}
			if object.Pkg() != nil && (object.Pkg().Path() == "reflect" || object.Pkg().Path() == "unsafe") {
				valid = false
				return false
			}
			switch object := object.(type) {
			case *types.Var:
				// Fields, parameters, results, and locals are represented by the
				// typed declaration. Package variables can be initialized or mutated
				// by arbitrary package initialization and are never proof inputs.
				if object.Pkg() != nil && object.Parent() == object.Pkg().Scope() {
					valid = false
					return false
				}
			case *types.TypeName:
				if _, typeParameter := object.Type().(*types.TypeParam); typeParameter {
					valid = false
					return false
				}
			}
		}
		if expression, ok := node.(ast.Expr); ok {
			fmt.Fprintf(&canonical, "EXPR:%T:%s\n", expression, canonicalTypeString(x.typesInfo.TypeOf(expression)))
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if x.typesInfo.Types[call.Fun].IsType() {
			// Type conversions are fully represented by their typed AST. Generic
			// instantiations and type parameters were rejected above.
			fmt.Fprintf(&canonical, "CONVERSION:%s\n", canonicalTypeString(x.typesInfo.TypeOf(call.Fun)))
			return true
		}
		object := calledFunctionObject(x.typesInfo, call.Fun)
		if builtin, ok := object.(*types.Builtin); ok {
			if builtin.Name() != "len" {
				valid = false
				return false
			}
			fmt.Fprintf(&canonical, "BUILTIN:%s\n", canonicalObjectString(builtin))
			return true
		}
		callee, ok := object.(*types.Func)
		if !ok {
			// Includes function variables, closures, and unresolved selectors.
			valid = false
			return false
		}
		calleeSignature, _ := callee.Type().(*types.Signature)
		if signatureUsesTypeParameters(calleeSignature) {
			// Generic callees and instantiations are outside the proof language.
			valid = false
			return false
		}
		fmt.Fprintf(&canonical, "CALL:%s\n", canonicalObjectString(callee))
		if calleeSignature.Recv() != nil {
			if !exactApprovedRawProofMethod(callee) {
				valid = false
				return false
			}
			return true
		}
		if callee.Pkg() != nil && callee.Pkg().Path() == providerPackagePath {
			proof, closed := x.canonicalRawIdentityProof(callee, visiting)
			if !closed {
				valid = false
				return false
			}
			fmt.Fprintf(&canonical, "CALLEE:%s:%s\n", canonicalObjectString(callee), proof)
			return true
		}
		if !exactApprovedRawProofExternalFunction(callee) {
			valid = false
			return false
		}
		return true
	})
	if !valid {
		return "", false
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(digest[:]), true
}

func (x *extractor) reviewedRawIdentityHelperValid(function *types.Func) bool {
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || function.Type().(*types.Signature).Recv() != nil {
		return false
	}
	expected, reviewed := reviewedRawIdentityProofs[function.Name()]
	if !reviewed {
		return false
	}
	proof, valid := x.canonicalRawIdentityProof(function, map[*types.Func]bool{})
	return valid && proof == expected
}

func (x *extractor) reviewedPromptEnvironmentValid(function *types.Func) bool {
	if function == nil || function.Name() != "promptEnvironment" || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath {
		return false
	}
	proof, valid := x.canonicalRawIdentityProof(function, map[*types.Func]bool{})
	return valid && proof == reviewedPromptEnvironmentProof
}

func (x *extractor) exactLocalAliases(fn *ast.FuncDecl, object types.Object) map[types.Object]bool {
	aliases := map[types.Object]bool{object: true}
	if fn == nil || fn.Body == nil || object == nil {
		return aliases
	}
	changed := true
	for changed {
		changed = false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
				return true
			}
			for index, left := range assignment.Lhs {
				leftIdentifier, leftOK := left.(*ast.Ident)
				rightIdentifier, rightOK := assignment.Rhs[index].(*ast.Ident)
				if !leftOK || !rightOK {
					continue
				}
				leftObject := x.typesInfo.ObjectOf(leftIdentifier)
				rightObject := x.typesInfo.ObjectOf(rightIdentifier)
				if leftObject == nil || rightObject == nil || !types.Identical(leftObject.Type(), rightObject.Type()) {
					continue
				}
				if aliases[leftObject] && !aliases[rightObject] {
					aliases[rightObject] = true
					changed = true
				}
				if aliases[rightObject] && !aliases[leftObject] {
					aliases[leftObject] = true
					changed = true
				}
			}
			return true
		})
	}
	return aliases
}

func (x *extractor) collectionAliasesEscape(fn *ast.FuncDecl, aliases map[types.Object]bool) bool {
	if fn == nil || fn.Body == nil {
		return true
	}
	parents := astParentMap(fn.Body)
	escaped := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if escaped {
			return false
		}
		identifier, ok := node.(*ast.Ident)
		if !ok || !aliases[x.typesInfo.ObjectOf(identifier)] {
			return true
		}
		parent := parents[identifier]
		switch item := parent.(type) {
		case *ast.CallExpr:
			builtin, _ := calledFunctionObject(x.typesInfo, item.Fun).(*types.Builtin)
			if builtin != nil {
				switch builtin.Name() {
				case "append":
					if len(item.Args) != 0 && item.Args[0] == identifier {
						return true
					}
				case "len", "cap", "delete", "clear":
					return true
				}
			}
			escaped = true
		case *ast.CompositeLit, *ast.ReturnStmt, *ast.UnaryExpr, *ast.StarExpr, *ast.SliceExpr:
			escaped = true
		case *ast.SelectorExpr:
			selection := x.typesInfo.Selections[item]
			if selection != nil && (selection.Kind() == types.MethodVal || selection.Kind() == types.FieldVal) {
				escaped = true
			}
		case *ast.AssignStmt:
			for index, source := range item.Rhs {
				if source != identifier {
					continue
				}
				if index >= len(item.Lhs) {
					escaped = true
					break
				}
				left, direct := item.Lhs[index].(*ast.Ident)
				if !direct || !aliases[x.typesInfo.ObjectOf(left)] {
					escaped = true
				}
			}
		}
		return !escaped
	})
	if escaped {
		return true
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		for alias := range aliases {
			if expressionUsesObject(x.typesInfo, literal.Body, alias) != 0 {
				escaped = true
				return false
			}
		}
		return !escaped
	})
	return escaped
}

func (x *extractor) rawStringSequenceProvenance(expr ast.Expr, fn *ast.FuncDecl, seen map[types.Object]bool) bool {
	switch item := expr.(type) {
	case *ast.ParenExpr:
		return x.rawStringSequenceProvenance(item.X, fn, seen)
	case *ast.CompositeLit:
		for _, element := range item.Elts {
			value := ast.Expr(element)
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				value = pair.Value
			}
			if !x.rawStringProvenance(value, fn, map[types.Object]bool{}) {
				return false
			}
		}
		return true
	case *ast.Ident:
		object := x.typesInfo.ObjectOf(item)
		if object == nil {
			return false
		}
		aliases := x.exactLocalAliases(fn, object)
		hasRawSource := false
		for alias := range aliases {
			if x.functionParameterObject(fn, alias) && len(x.variableSources(fn, alias)) == 0 {
				hasRawSource = true
			}
			if seen[alias] {
				continue
			}
			seen[alias] = true
			for _, source := range x.variableSources(fn, alias) {
				if identifier, ok := source.(*ast.Ident); ok && aliases[x.typesInfo.ObjectOf(identifier)] {
					continue
				}
				hasRawSource = true
				if !x.rawStringSequenceProvenance(source, fn, seen) {
					return false
				}
			}
		}
		if !hasRawSource || x.collectionAliasesEscape(fn, aliases) {
			return false
		}
		valid := true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
				return true
			}
			for index, left := range assignment.Lhs {
				indexed, ok := left.(*ast.IndexExpr)
				base, direct := func() (*ast.Ident, bool) {
					if !ok {
						return nil, false
					}
					identifier, yes := indexed.X.(*ast.Ident)
					return identifier, yes
				}()
				if direct && aliases[x.typesInfo.ObjectOf(base)] && !x.rawStringProvenance(assignment.Rhs[index], fn, map[types.Object]bool{}) {
					valid = false
				}
			}
			return valid
		})
		return valid
	case *ast.CallExpr:
		if len(item.Args) >= 1 {
			if builtin, ok := calledFunctionObject(x.typesInfo, item.Fun).(*types.Builtin); ok {
				switch builtin.Name() {
				case "make":
					return len(item.Args) <= 2
				case "append":
					if !x.rawStringSequenceProvenance(item.Args[0], fn, seen) {
						return false
					}
					for index, argument := range item.Args[1:] {
						if item.Ellipsis.IsValid() && index == len(item.Args[1:])-1 {
							if !x.rawStringSequenceProvenance(argument, fn, map[types.Object]bool{}) {
								return false
							}
						} else if !x.rawStringProvenance(argument, fn, map[types.Object]bool{}) {
							return false
						}
					}
					return true
				}
			}
		}
	}
	return false
}

func (x *extractor) rawStringMapProvenance(expr ast.Expr, keyProvenance bool, fn *ast.FuncDecl, seen map[types.Object]bool) bool {
	switch item := expr.(type) {
	case *ast.ParenExpr:
		return x.rawStringMapProvenance(item.X, keyProvenance, fn, seen)
	case *ast.CompositeLit:
		for _, element := range item.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				return false
			}
			candidate := pair.Value
			if keyProvenance {
				candidate = pair.Key
			}
			if !x.rawStringProvenance(candidate, fn, map[types.Object]bool{}) {
				return false
			}
		}
		return true
	case *ast.Ident:
		object := x.typesInfo.ObjectOf(item)
		if object == nil {
			return false
		}
		aliases := x.exactLocalAliases(fn, object)
		hasRawSource := false
		for alias := range aliases {
			if x.functionParameterObject(fn, alias) && len(x.variableSources(fn, alias)) == 0 {
				hasRawSource = true
			}
			if seen[alias] {
				continue
			}
			seen[alias] = true
			for _, source := range x.variableSources(fn, alias) {
				if identifier, ok := source.(*ast.Ident); ok && aliases[x.typesInfo.ObjectOf(identifier)] {
					continue
				}
				hasRawSource = true
				if !x.rawStringMapProvenance(source, keyProvenance, fn, seen) {
					return false
				}
			}
		}
		if !hasRawSource || x.collectionAliasesEscape(fn, aliases) {
			return false
		}
		valid := true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
				return true
			}
			for index, left := range assignment.Lhs {
				indexed, ok := left.(*ast.IndexExpr)
				base, direct := func() (*ast.Ident, bool) {
					if !ok {
						return nil, false
					}
					identifier, yes := indexed.X.(*ast.Ident)
					return identifier, yes
				}()
				if !direct || !aliases[x.typesInfo.ObjectOf(base)] {
					continue
				}
				candidate := assignment.Rhs[index]
				if keyProvenance {
					candidate = indexed.Index
				}
				if !x.rawStringProvenance(candidate, fn, map[types.Object]bool{}) {
					valid = false
				}
			}
			return valid
		})
		return valid
	case *ast.CallExpr:
		builtin, _ := calledFunctionObject(x.typesInfo, item.Fun).(*types.Builtin)
		return builtin != nil && builtin.Name() == "make" && len(item.Args) <= 2
	}
	return false
}

func (x *extractor) rawRangeStringProvenance(statement *ast.RangeStmt, object types.Object, fn *ast.FuncDecl, seen map[types.Object]bool) bool {
	if statement == nil || object == nil {
		return false
	}
	key, _ := statement.Key.(*ast.Ident)
	value, _ := statement.Value.(*ast.Ident)
	isKey := key != nil && x.typesInfo.ObjectOf(key) == object
	isValue := value != nil && x.typesInfo.ObjectOf(value) == object
	if !isKey && !isValue {
		return false
	}
	switch container := types.Unalias(x.typesInfo.TypeOf(statement.X)).Underlying().(type) {
	case *types.Slice:
		return isValue && types.Identical(container.Elem(), types.Typ[types.String]) && x.rawStringSequenceProvenance(statement.X, fn, seen)
	case *types.Array:
		return isValue && types.Identical(container.Elem(), types.Typ[types.String]) && x.rawStringSequenceProvenance(statement.X, fn, seen)
	case *types.Map:
		if isKey {
			return types.Identical(container.Key(), types.Typ[types.String]) && x.rawStringMapProvenance(statement.X, true, fn, seen)
		}
		return types.Identical(container.Elem(), types.Typ[types.String]) && x.rawStringMapProvenance(statement.X, false, fn, seen)
	}
	return false
}

func (x *extractor) rawStringProvenance(expr ast.Expr, fn *ast.FuncDecl, seen map[types.Object]bool) bool {
	switch item := expr.(type) {
	case *ast.BasicLit:
		return item.Kind == token.STRING
	case *ast.ParenExpr:
		return x.rawStringProvenance(item.X, fn, seen)
	case *ast.SelectorExpr:
		if !types.Identical(x.typesInfo.TypeOf(item), types.Typ[types.String]) {
			return false
		}
		root, ok := item.X.(*ast.Ident)
		if !ok {
			return false
		}
		for _, source := range x.variableSources(fn, x.typesInfo.ObjectOf(root)) {
			call, ok := source.(*ast.CallExpr)
			if !ok {
				return false
			}
			function, _ := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func)
			if !x.reviewedRawIdentityHelperValid(function) {
				return false
			}
		}
		return len(x.variableSources(fn, x.typesInfo.ObjectOf(root))) > 0
	case *ast.Ident:
		object := x.typesInfo.ObjectOf(item)
		if object == nil {
			return false
		}
		if constant, ok := object.(*types.Const); ok {
			return constant.Type() != nil && types.Identical(constant.Type(), types.Typ[types.String])
		}
		if x.functionParameterObject(fn, object) {
			return types.Identical(object.Type(), types.Typ[types.String]) && len(x.variableSources(fn, object)) == 0
		}
		var rangeStatement *ast.RangeStmt
		if fn != nil && fn.Body != nil {
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				statement, ok := node.(*ast.RangeStmt)
				if !ok {
					return true
				}
				key, _ := statement.Key.(*ast.Ident)
				value, _ := statement.Value.(*ast.Ident)
				if key != nil && x.typesInfo.ObjectOf(key) == object || value != nil && x.typesInfo.ObjectOf(value) == object {
					rangeStatement = statement
					return false
				}
				return true
			})
		}
		if rangeStatement != nil {
			return x.rawRangeStringProvenance(rangeStatement, object, fn, seen)
		}
		if seen[object] {
			return false
		}
		seen[object] = true
		sources := x.variableSources(fn, object)
		if len(sources) == 0 {
			return false
		}
		for _, source := range sources {
			if !x.rawStringProvenance(source, fn, seen) {
				return false
			}
		}
		return true
	case *ast.TypeAssertExpr:
		// A direct string assertion from a decoded API object is the reviewed
		// raw API identity form. Calls and transforms around the assertion are
		// deliberately not accepted.
		if !types.Identical(x.typesInfo.TypeOf(item.Type), types.Typ[types.String]) {
			return false
		}
		indexed, ok := item.X.(*ast.IndexExpr)
		if !ok {
			return false
		}
		mapping, ok := types.Unalias(x.typesInfo.TypeOf(indexed.X)).(*types.Map)
		if !ok || !types.Identical(mapping.Key(), types.Typ[types.String]) || !exactEmptyInterface(mapping.Elem()) {
			return false
		}
		root, ok := indexed.X.(*ast.Ident)
		if !ok {
			return false
		}
		rootObject := x.typesInfo.ObjectOf(root)
		for _, source := range x.variableSources(fn, rootObject) {
			call, ok := source.(*ast.CallExpr)
			if !ok {
				return false
			}
			function, _ := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func)
			if function == nil || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || function.Name() != "unwrapObjectEnvelope" {
				return false
			}
		}
		locallyWritten := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, left := range assignment.Lhs {
				target, ok := left.(*ast.IndexExpr)
				base, direct := func() (*ast.Ident, bool) {
					if !ok {
						return nil, false
					}
					identifier, yes := target.X.(*ast.Ident)
					return identifier, yes
				}()
				if direct && x.typesInfo.ObjectOf(base) == rootObject {
					locallyWritten = true
				}
			}
			return !locallyWritten
		})
		return !locallyWritten
	case *ast.CallExpr:
		called := calledFunctionObject(x.typesInfo, item.Fun)
		if exactTerraformValueString(called) && len(item.Args) == 0 {
			return true
		}
		if x.reviewedFreeFunctionDeclaration(fn, map[string]bool{"addKnownStringFilter": true}) && len(item.Args) == 0 {
			if selector, ok := item.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "ValueString" {
				if receiver, ok := selector.X.(*ast.Ident); ok && x.functionParameterObject(fn, x.typesInfo.ObjectOf(receiver)) {
					return true
				}
			}
		}
		function, _ := called.(*types.Func)
		if function == nil {
			return false
		}
		if x.reviewedRawIdentityHelperValid(function) {
			for _, argument := range item.Args {
				if types.Identical(x.typesInfo.TypeOf(argument), types.Typ[types.String]) && !x.rawStringProvenance(argument, fn, map[types.Object]bool{}) {
					return false
				}
			}
			return true
		}
		if function.Pkg() != nil && function.Pkg().Path() == "fmt" && function.Name() == "Sprintf" {
			format, literal := func() (string, bool) {
				if len(item.Args) == 0 {
					return "", false
				}
				return stringLiteral(item.Args[0])
			}()
			if format == "%s.v%d" && literal && len(item.Args) == 3 && x.reviewedFreeFunctionDeclaration(fn, map[string]bool{"promptPath": true}) {
				promptID, promptDirect := item.Args[1].(*ast.Ident)
				versionPointer, versionStar := item.Args[2].(*ast.StarExpr)
				version, versionDirect := func() (*ast.Ident, bool) {
					if !versionStar {
						return nil, false
					}
					identifier, ok := versionPointer.X.(*ast.Ident)
					return identifier, ok
				}()
				return promptDirect && x.typesInfo.ObjectOf(promptID) == x.endpointBuilderParameter(fn, 0) && versionDirect && x.typesInfo.ObjectOf(version) == x.endpointBuilderParameter(fn, 1)
			}
			if format == "%d" && literal && len(item.Args) == 2 && x.reviewedPaginationDeclaration(fn) {
				return true
			}
			return false
		}
		if function.Pkg() != nil && function.Pkg().Path() == "strconv" && function.Name() == "Itoa" && len(item.Args) == 1 && x.reviewedPaginationDeclaration(fn) {
			return true
		}
		if len(item.Args) == 1 && x.reviewedPromptEnvironmentValid(function) {
			return x.rawStringProvenance(item.Args[0], fn, map[types.Object]bool{})
		}
	}
	return false
}

func (x *extractor) rawStringSliceProvenance(expr ast.Expr, fn *ast.FuncDecl) bool {
	if literal, ok := expr.(*ast.CompositeLit); ok {
		for _, leaf := range literal.Elts {
			if !x.rawStringProvenance(leaf, fn, map[types.Object]bool{}) {
				return false
			}
		}
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || fn == nil || fn.Name.Name != "cloneURLValues" || len(call.Args) != 2 || !call.Ellipsis.IsValid() {
		return false
	}
	builtin, _ := calledFunctionObject(x.typesInfo, call.Fun).(*types.Builtin)
	entries, ok := call.Args[1].(*ast.Ident)
	if builtin == nil || builtin.Name() != "append" || !ok {
		return false
	}
	entryObject := x.typesInfo.ObjectOf(entries)
	valid := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.RangeStmt)
		if !ok {
			return true
		}
		value, _ := statement.Value.(*ast.Ident)
		if value != nil && x.typesInfo.ObjectOf(value) == entryObject && exactURLValuesCarrierType(x.typesInfo.TypeOf(statement.X)) {
			valid = true
		}
		return true
	})
	return valid
}

func (x *extractor) rawURLValuesProvenance(expr ast.Expr, fn *ast.FuncDecl, seen map[types.Object]bool) bool {
	switch item := expr.(type) {
	case *ast.ParenExpr:
		return x.rawURLValuesProvenance(item.X, fn, seen)
	case *ast.StarExpr:
		return x.rawURLValuesProvenance(item.X, fn, seen)
	case *ast.CompositeLit:
		if !exactURLValuesCarrierType(x.typesInfo.TypeOf(item)) {
			return false
		}
		valid := true
		ast.Inspect(item, func(node ast.Node) bool {
			if !valid {
				return false
			}
			if nested, ok := node.(*ast.CompositeLit); ok && nested != item && exactURLValuesCarrierType(x.typesInfo.TypeOf(nested)) {
				return true
			}
			return true
		})
		for _, element := range item.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				valid = false
				continue
			}
			if !x.rawStringSliceProvenance(pair.Value, fn) {
				valid = false
			}
		}
		return valid
	case *ast.Ident:
		object := x.typesInfo.ObjectOf(item)
		if object == nil || !exactURLValuesCarrierType(object.Type()) {
			return false
		}
		if x.functionParameterObject(fn, object) {
			return true
		}
		if seen[object] {
			return true
		}
		seen[object] = true
		sources := x.variableSources(fn, object)
		if len(sources) == 0 {
			return false
		}
		for _, source := range sources {
			if !x.rawURLValuesProvenance(source, fn, seen) {
				return false
			}
		}
		valid := true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && len(call.Args) >= 2 && x.exactURLValuesMethod(calledFunctionObject(x.typesInfo, selector), "Set", "Add") {
				receiver, direct := selector.X.(*ast.Ident)
				if direct && x.typesInfo.ObjectOf(receiver) == object && !x.rawStringProvenance(call.Args[1], fn, map[types.Object]bool{}) {
					valid = false
				}
			}
			if function, ok := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func); ok && function.Pkg() != nil && function.Pkg().Path() == providerPackagePath && function.Name() == "addKnownStringFilter" && len(call.Args) == 3 {
				argument, direct := call.Args[0].(*ast.Ident)
				if direct && x.typesInfo.ObjectOf(argument) == object {
					method, ok := call.Args[2].(*ast.Ident)
					if !ok || !x.functionParameterObject(fn, x.typesInfo.ObjectOf(method)) && !exactNamedType(x.typesInfo.TypeOf(method), "github.com/hashicorp/terraform-plugin-framework/types/basetypes", "StringValue") {
						valid = false
					}
				}
			}
			return valid
		})
		return valid
	case *ast.CallExpr:
		function, _ := calledFunctionObject(x.typesInfo, item.Fun).(*types.Func)
		if _, exact := exactProviderFreeFunction(function, reviewedQueryValueHelpers); !exact {
			return false
		}
		if function.Name() == "cloneURLValues" {
			return len(item.Args) == 1 && x.rawURLValuesProvenance(item.Args[0], fn, map[types.Object]bool{})
		}
		for _, argument := range item.Args {
			if types.Identical(x.typesInfo.TypeOf(argument), types.Typ[types.String]) && !x.rawStringProvenance(argument, fn, map[types.Object]bool{}) {
				return false
			}
		}
		return true
	}
	return false
}

type reviewedEndpointCallable struct {
	params *ast.FieldList
	body   *ast.BlockStmt
}

func (x *extractor) bodyDispatchesEndpoint(body *ast.BlockStmt, visiting map[*types.Func]bool) bool {
	if body == nil {
		return false
	}
	dispatches := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		called := calledFunctionObject(x.typesInfo, call.Fun)
		if x.exactEndpointBuilder(called) {
			dispatches = true
			return false
		}
		function, ok := called.(*types.Func)
		if !ok || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || visiting[function] {
			return true
		}
		callee := x.funcDeclForObject(function)
		if callee == nil {
			return true
		}
		visiting[function] = true
		if x.bodyDispatchesEndpoint(callee.Body, visiting) {
			dispatches = true
		}
		delete(visiting, function)
		return !dispatches
	})
	return dispatches
}

func (x *extractor) funcDeclForObject(function *types.Func) *ast.FuncDecl {
	if function == nil {
		return nil
	}
	return x.funcDecls[function]
}

func (x *extractor) endpointCallableForExpr(expr ast.Expr, fn *ast.FuncDecl, seen map[types.Object]bool) (reviewedEndpointCallable, bool) {
	switch item := expr.(type) {
	case *ast.ParenExpr:
		return x.endpointCallableForExpr(item.X, fn, seen)
	case *ast.FuncLit:
		if x.bodyDispatchesEndpoint(item.Body, map[*types.Func]bool{}) {
			return reviewedEndpointCallable{params: item.Type.Params, body: item.Body}, true
		}
	case *ast.SelectorExpr:
		function, _ := calledFunctionObject(x.typesInfo, item).(*types.Func)
		declaration := x.funcDeclForObject(function)
		if declaration != nil && x.bodyDispatchesEndpoint(declaration.Body, map[*types.Func]bool{function: true}) {
			return reviewedEndpointCallable{params: declaration.Type.Params, body: declaration.Body}, true
		}
	case *ast.Ident:
		object := x.typesInfo.ObjectOf(item)
		if function, ok := object.(*types.Func); ok && function.Pkg() != nil && function.Pkg().Path() == providerPackagePath {
			declaration := x.funcDeclForObject(function)
			if declaration != nil && x.bodyDispatchesEndpoint(declaration.Body, map[*types.Func]bool{function: true}) {
				return reviewedEndpointCallable{params: declaration.Type.Params, body: declaration.Body}, true
			}
			return reviewedEndpointCallable{}, false
		}
		if object == nil || seen[object] {
			return reviewedEndpointCallable{}, false
		}
		seen[object] = true
		var resolved reviewedEndpointCallable
		found := false
		for _, source := range x.variableSources(fn, object) {
			candidate, ok := x.endpointCallableForExpr(source, fn, seen)
			if !ok {
				return reviewedEndpointCallable{}, false
			}
			if found && (candidate.params != resolved.params || candidate.body != resolved.body) {
				return reviewedEndpointCallable{}, false
			}
			resolved, found = candidate, true
		}
		return resolved, found
	}
	return reviewedEndpointCallable{}, false
}

func (x *extractor) functionParameterAt(fn *ast.FuncDecl, index int) types.Object {
	if fn == nil || fn.Type.Params == nil || index < 0 {
		return nil
	}
	current := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if current == index {
				return x.typesInfo.Defs[name]
			}
			current++
		}
	}
	return nil
}

func (x *extractor) higherOrderParameterConsumedDirectly(fn *ast.FuncDecl, parameter types.Object, visiting map[types.Object]bool) bool {
	if fn == nil || fn.Body == nil || parameter == nil || visiting[parameter] {
		return false
	}
	visiting[parameter] = true
	defer delete(visiting, parameter)
	parents := astParentMap(fn.Body)
	valid, consumed := true, false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if !valid {
			return false
		}
		identifier, ok := node.(*ast.Ident)
		if !ok || x.typesInfo.Uses[identifier] != parameter {
			return true
		}
		for ancestor := parents[identifier]; ancestor != nil && ancestor != fn.Body; ancestor = parents[ancestor] {
			if _, nested := ancestor.(*ast.FuncLit); nested {
				valid = false
				return false
			}
		}
		call, ok := parents[identifier].(*ast.CallExpr)
		if !ok {
			valid = false
			return false
		}
		if call.Fun == identifier {
			consumed = true
			return true
		}
		argumentIndex := -1
		for index, argument := range call.Args {
			if argument == identifier {
				argumentIndex = index
				break
			}
		}
		function, direct := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func)
		callee := x.funcDeclForObject(function)
		if argumentIndex < 0 || !direct || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || callee == nil {
			valid = false
			return false
		}
		forwarded := x.functionParameterAt(callee, argumentIndex)
		if !x.higherOrderParameterConsumedDirectly(callee, forwarded, visiting) {
			valid = false
			return false
		}
		consumed = true
		return true
	})
	return valid && consumed
}

func (x *extractor) reviewedHigherOrderConsumer(function *types.Func, argumentIndex int) bool {
	declaration := x.funcDeclForObject(function)
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || declaration == nil {
		return false
	}
	parameter := x.functionParameterAt(declaration, argumentIndex)
	if parameter == nil {
		return false
	}
	if _, callable := types.Unalias(parameter.Type()).Underlying().(*types.Signature); !callable {
		return false
	}
	return x.higherOrderParameterConsumedDirectly(declaration, parameter, map[types.Object]bool{})
}

func (x *extractor) rawStringCollectionProvenance(expr ast.Expr, parameterType types.Type, fn *ast.FuncDecl) bool {
	switch container := types.Unalias(parameterType).Underlying().(type) {
	case *types.Slice:
		return types.Identical(container.Elem(), types.Typ[types.String]) && x.rawStringSequenceProvenance(expr, fn, map[types.Object]bool{})
	case *types.Array:
		return types.Identical(container.Elem(), types.Typ[types.String]) && x.rawStringSequenceProvenance(expr, fn, map[types.Object]bool{})
	case *types.Map:
		valid := true
		if types.Identical(container.Key(), types.Typ[types.String]) {
			valid = x.rawStringMapProvenance(expr, true, fn, map[types.Object]bool{})
		}
		if valid && types.Identical(container.Elem(), types.Typ[types.String]) {
			valid = x.rawStringMapProvenance(expr, false, fn, map[types.Object]bool{})
		}
		return valid
	}
	return false
}

func (x *extractor) bodyAliasObjects(body *ast.BlockStmt, object types.Object) map[types.Object]bool {
	aliases := map[types.Object]bool{object: true}
	if body == nil || object == nil {
		return aliases
	}
	changed := true
	for changed {
		changed = false
		ast.Inspect(body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
				return true
			}
			for index, left := range assignment.Lhs {
				leftIdentifier, leftOK := left.(*ast.Ident)
				rightIdentifier, rightOK := assignment.Rhs[index].(*ast.Ident)
				if !leftOK || !rightOK {
					continue
				}
				leftObject, rightObject := x.typesInfo.ObjectOf(leftIdentifier), x.typesInfo.ObjectOf(rightIdentifier)
				if aliases[leftObject] && !aliases[rightObject] {
					aliases[rightObject] = true
					changed = true
				}
				if aliases[rightObject] && !aliases[leftObject] {
					aliases[leftObject] = true
					changed = true
				}
			}
			return true
		})
	}
	return aliases
}

func (x *extractor) collectionParameterFeedsEndpoint(callable reviewedEndpointCallable, parameter types.Object) bool {
	parameterAliases := x.bodyAliasObjects(callable.body, parameter)
	feeds := false
	ast.Inspect(callable.body, func(node ast.Node) bool {
		statement, ok := node.(*ast.RangeStmt)
		if !ok {
			return true
		}
		container, direct := statement.X.(*ast.Ident)
		if !direct || !parameterAliases[x.typesInfo.ObjectOf(container)] {
			return true
		}
		for _, ranged := range []ast.Expr{statement.Key, statement.Value} {
			identifier, ok := ranged.(*ast.Ident)
			if !ok || identifier.Name == "_" {
				continue
			}
			rangeAliases := x.bodyAliasObjects(callable.body, x.typesInfo.ObjectOf(identifier))
			ast.Inspect(callable.body, func(child ast.Node) bool {
				call, ok := child.(*ast.CallExpr)
				if !ok || !x.exactEndpointBuilder(calledFunctionObject(x.typesInfo, call.Fun)) || len(call.Args) < 2 {
					return true
				}
				for alias := range rangeAliases {
					if expressionUsesObject(x.typesInfo, call.Args[1], alias) > 0 {
						feeds = true
					}
				}
				return !feeds
			})
			if feeds {
				return false
			}
		}
		return !feeds
	})
	return feeds
}

func (x *extractor) endpointCallableArgumentsValid(callable reviewedEndpointCallable, arguments []ast.Expr, fn *ast.FuncDecl) bool {
	if callable.params == nil || callable.body == nil {
		return false
	}
	argumentIndex := 0
	for _, field := range callable.params.List {
		for _, name := range field.Names {
			if argumentIndex >= len(arguments) {
				return false
			}
			argument := arguments[argumentIndex]
			argumentIndex++
			parameter := x.typesInfo.Defs[name]
			if parameter == nil || expressionUsesObject(x.typesInfo, callable.body, parameter) == 0 {
				continue
			}
			switch {
			case types.Identical(parameter.Type(), types.Typ[types.String]):
				if !x.rawStringProvenance(argument, fn, map[types.Object]bool{}) {
					return false
				}
			case exactURLValuesCarrierType(parameter.Type()):
				if !x.rawURLValuesProvenance(argument, fn, map[types.Object]bool{}) {
					return false
				}
			case methodSetExposes(parameter.Type(), func(name string) bool { return name == "ValueString" }) != "":
				// Exact Terraform values retain their raw identity until the reviewed
				// ValueString call inside the wrapper.
				if !exactNamedType(parameter.Type(), "github.com/hashicorp/terraform-plugin-framework/types/basetypes", "StringValue") {
					return false
				}
			default:
				underlying := types.Unalias(parameter.Type()).Underlying()
				switch underlying.(type) {
				case *types.Slice, *types.Array, *types.Map:
					if x.collectionParameterFeedsEndpoint(callable, parameter) && !x.rawStringCollectionProvenance(argument, parameter.Type(), fn) {
						return false
					}
				}
			}
		}
	}
	return true
}

func (x *extractor) functionDirectlyUsesEndpoint(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	uses := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && x.exactEndpointBuilder(calledFunctionObject(x.typesInfo, call.Fun)) {
			uses = true
			return false
		}
		return true
	})
	return uses
}

func astParentMap(root ast.Node) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if child != nil && child != node {
				if _, exists := parents[child]; !exists {
					parents[child] = node
				}
				return false
			}
			return true
		})
		return true
	})
	return parents
}

func expressionUsesObject(info *types.Info, expression ast.Node, object types.Object) int {
	uses := 0
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && info.ObjectOf(identifier) == object {
			uses++
		}
		return true
	})
	return uses
}

func (x *extractor) exactEmptyValuesReturn(statement *ast.ReturnStmt, fn *ast.FuncDecl, parents map[ast.Node]ast.Node) bool {
	if len(statement.Results) != 1 {
		return false
	}
	path, ok := statement.Results[0].(*ast.Ident)
	if !ok || x.typesInfo.ObjectOf(path) != x.endpointBuilderParameter(fn, 0) {
		return false
	}
	block, ok := parents[statement].(*ast.BlockStmt)
	if !ok {
		return false
	}
	branch, ok := parents[block].(*ast.IfStmt)
	if !ok || branch.Body != block || branch.Else != nil {
		return false
	}
	comparison, ok := branch.Cond.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return false
	}
	zero, ok := comparison.Y.(*ast.BasicLit)
	if !ok || zero.Kind != token.INT || zero.Value != "0" {
		return false
	}
	length, ok := comparison.X.(*ast.CallExpr)
	if !ok || len(length.Args) != 1 {
		return false
	}
	builtin, ok := calledFunctionObject(x.typesInfo, length.Fun).(*types.Builtin)
	values, direct := length.Args[0].(*ast.Ident)
	return ok && builtin.Name() == "len" && direct && x.typesInfo.ObjectOf(values) == x.endpointBuilderParameter(fn, 1)
}

func (x *extractor) exactInvalidEndpointReturn(statement *ast.ReturnStmt) bool {
	if statement == nil || len(statement.Results) != 1 {
		return false
	}
	identifier, ok := statement.Results[0].(*ast.Ident)
	if !ok || identifier.Name != "invalidReviewedEndpoint" {
		return false
	}
	constantObject, ok := x.typesInfo.ObjectOf(identifier).(*types.Const)
	return ok && constantObject.Pkg() != nil && constantObject.Pkg().Path() == providerPackagePath && constant.StringVal(constantObject.Val()) == "/.terraform-provider-litellm-invalid-reviewed-endpoint"
}

func exactBoolLiteral(expression ast.Expr, want bool) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == strconv.FormatBool(want)
}

func (x *extractor) exactStringComparison(expression ast.Expr, object types.Object, want string) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return false
	}
	identifier, direct := comparison.X.(*ast.Ident)
	literal, exact := stringLiteral(comparison.Y)
	return direct && x.typesInfo.ObjectOf(identifier) == object && exact && literal == want
}

func (x *extractor) reviewedContainsDotPathComponentValid(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil || fn.Name.Name != "containsDotPathComponent" || len(fn.Body.List) != 2 || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 {
		return false
	}
	parameter := x.typesInfo.Defs[fn.Type.Params.List[0].Names[0]]
	loop, ok := fn.Body.List[0].(*ast.RangeStmt)
	if !ok || loop.Tok != token.DEFINE || len(loop.Body.List) != 1 {
		return false
	}
	blank, keyOK := loop.Key.(*ast.Ident)
	component, valueOK := loop.Value.(*ast.Ident)
	if !keyOK || blank.Name != "_" || !valueOK {
		return false
	}
	componentObject := x.typesInfo.Defs[component]
	split, ok := loop.X.(*ast.CallExpr)
	if !ok || len(split.Args) != 2 {
		return false
	}
	function, _ := calledFunctionObject(x.typesInfo, split.Fun).(*types.Func)
	value, direct := split.Args[0].(*ast.Ident)
	separator, literal := stringLiteral(split.Args[1])
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != "strings" || function.Name() != "Split" || !direct || x.typesInfo.ObjectOf(value) != parameter || !literal || separator != "/" {
		return false
	}
	branch, ok := loop.Body.List[0].(*ast.IfStmt)
	if !ok || branch.Else != nil || len(branch.Body.List) != 1 {
		return false
	}
	disjunction, ok := branch.Cond.(*ast.BinaryExpr)
	if !ok || disjunction.Op != token.LOR {
		return false
	}
	comparisons := map[string]bool{}
	for _, candidate := range []ast.Expr{disjunction.X, disjunction.Y} {
		for _, expected := range []string{".", ".."} {
			if x.exactStringComparison(candidate, componentObject, expected) {
				comparisons[expected] = true
			}
		}
	}
	returnedTrue, ok := branch.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnedTrue.Results) != 1 || !exactBoolLiteral(returnedTrue.Results[0], true) || !comparisons["."] || !comparisons[".."] {
		return false
	}
	returnedFalse, ok := fn.Body.List[1].(*ast.ReturnStmt)
	return ok && len(returnedFalse.Results) == 1 && exactBoolLiteral(returnedFalse.Results[0], false)
}

func (x *extractor) reviewedHardenDotSegmentValid(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil || fn.Name.Name != "hardenDotSegment" || len(fn.Body.List) != 1 || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 {
		return false
	}
	parameter := x.typesInfo.Defs[fn.Type.Params.List[0].Names[0]]
	switchStatement, ok := fn.Body.List[0].(*ast.SwitchStmt)
	if !ok || switchStatement.Init != nil {
		return false
	}
	tag, direct := switchStatement.Tag.(*ast.Ident)
	if !direct || x.typesInfo.ObjectOf(tag) != parameter || len(switchStatement.Body.List) != 3 {
		return false
	}
	seen := map[string]bool{}
	for _, statement := range switchStatement.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok || len(clause.Body) != 1 {
			return false
		}
		returned, ok := clause.Body[0].(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			return false
		}
		if len(clause.List) == 0 {
			identifier, ok := returned.Results[0].(*ast.Ident)
			if !ok || x.typesInfo.ObjectOf(identifier) != parameter {
				return false
			}
			seen["default"] = true
			continue
		}
		if len(clause.List) != 1 {
			return false
		}
		input, inputLiteral := stringLiteral(clause.List[0])
		output, outputLiteral := stringLiteral(returned.Results[0])
		if !inputLiteral || !outputLiteral || input == "." && output != "%2E" || input == ".." && output != "%2E%2E" || input != "." && input != ".." {
			return false
		}
		seen[input] = true
	}
	return seen["."] && seen[".."] && seen["default"]
}

func (x *extractor) reviewedPathGuardReturn(fn *ast.FuncDecl) *ast.ReturnStmt {
	if fn.Name.Name == "endpointWithFallbackPathSegment" {
		return nil
	}
	var found *ast.ReturnStmt
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok || branch.Else != nil || len(branch.Body.List) != 1 {
			return true
		}
		returned, ok := branch.Body.List[0].(*ast.ReturnStmt)
		if !ok || !x.exactInvalidEndpointReturn(returned) {
			return true
		}
		call, ok := branch.Cond.(*ast.CallExpr)
		if !ok {
			return true
		}
		function, _ := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func)
		if function == nil || len(call.Args) == 0 {
			return true
		}
		value, direct := call.Args[0].(*ast.Ident)
		if !direct || x.typesInfo.ObjectOf(value) != x.endpointBuilderParameter(fn, 1) {
			return true
		}
		switch fn.Name.Name {
		case "endpointWithPathSegment":
			separator, literal := func() (string, bool) {
				if len(call.Args) != 2 {
					return "", false
				}
				return stringLiteral(call.Args[1])
			}()
			if function.Pkg() != nil && function.Pkg().Path() == "strings" && function.Name() == "Contains" && literal && separator == "/" {
				found = returned
			}
		case "endpointWithPathCapture":
			if function.Pkg() != nil && function.Pkg().Path() == providerPackagePath && function.Name() == "containsDotPathComponent" && len(call.Args) == 1 && x.reviewedContainsDotPathComponentValid(x.funcDeclForObject(function)) {
				found = returned
			}
		}
		return found == nil
	})
	return found
}

func (x *extractor) reviewedQueryInvalidPropagationReturn(fn *ast.FuncDecl) *ast.ReturnStmt {
	var found *ast.ReturnStmt
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok || branch.Else != nil || len(branch.Body.List) != 1 {
			return true
		}
		returned, ok := branch.Body.List[0].(*ast.ReturnStmt)
		if !ok || !x.exactInvalidEndpointReturn(returned) {
			return true
		}
		call, ok := branch.Cond.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		function, _ := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func)
		path, direct := call.Args[0].(*ast.Ident)
		prefixIdentifier, prefixDirect := call.Args[1].(*ast.Ident)
		prefixObject, _ := x.typesInfo.ObjectOf(prefixIdentifier).(*types.Const)
		if function != nil && function.Pkg() != nil && function.Pkg().Path() == "strings" && function.Name() == "HasPrefix" &&
			direct && x.typesInfo.ObjectOf(path) == x.endpointBuilderParameter(fn, 0) && prefixDirect && prefixObject != nil &&
			prefixObject.Name() == "invalidReviewedEndpoint" && x.constants[prefixObject.Name()] == invalidReviewedEndpointValue {
			found = returned
		}
		return found == nil
	})
	return found
}

func (x *extractor) reviewedQueryDelimiterGuardPresent(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		call, ok := branch.Cond.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		function, _ := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func)
		path, direct := call.Args[0].(*ast.Ident)
		delimiters, literal := stringLiteral(call.Args[1])
		if function == nil || function.Pkg() == nil || function.Pkg().Path() != "strings" || function.Name() != "ContainsAny" || !direct || x.typesInfo.ObjectOf(path) != x.endpointBuilderParameter(fn, 0) || !literal || delimiters != "?#" {
			return true
		}
		for _, statement := range branch.Body.List {
			expression, ok := statement.(*ast.ExprStmt)
			panicCall, called := func() (*ast.CallExpr, bool) {
				if !ok {
					return nil, false
				}
				value, yes := expression.X.(*ast.CallExpr)
				return value, yes
			}()
			if !called || len(panicCall.Args) != 1 {
				continue
			}
			builtin, _ := calledFunctionObject(x.typesInfo, panicCall.Fun).(*types.Builtin)
			message, exact := stringLiteral(panicCall.Args[0])
			if builtin != nil && builtin.Name() == "panic" && exact && message == "endpoint path must not contain a query or fragment" {
				found = true
			}
		}
		return !found
	})
	return found
}

func (x *extractor) reviewedBuilderBodyValid(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	parents := astParentMap(fn.Body)
	var escapeCall *ast.CallExpr
	escapes := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && x.reviewedEscapeCallAllowed(calledFunctionObject(x.typesInfo, call.Fun), call, fn) {
			escapes++
			escapeCall = call
		}
		return true
	})
	if escapes != 1 || escapeCall == nil {
		return false
	}

	if fn.Name.Name == "endpointWithQuery" {
		invalidReturn := x.reviewedQueryInvalidPropagationReturn(fn)
		if invalidReturn == nil || !x.reviewedQueryDelimiterGuardPresent(fn) {
			return false
		}
		if _, ok := parents[escapeCall].(*ast.BinaryExpr); !ok {
			return false
		}
		for node := ast.Node(escapeCall); node != nil; node = parents[node] {
			if _, ok := node.(*ast.ReturnStmt); ok {
				break
			}
			if node != escapeCall {
				switch node.(type) {
				case *ast.BinaryExpr, *ast.ParenExpr:
				default:
					return false
				}
			}
		}
		valid := true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			statement, ok := node.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			containsEscape := false
			ast.Inspect(statement, func(child ast.Node) bool {
				containsEscape = containsEscape || child == escapeCall
				return true
			})
			if !containsEscape {
				if statement != invalidReturn && !x.exactEmptyValuesReturn(statement, fn, parents) {
					valid = false
				}
				return true
			}
			if len(statement.Results) != 1 || expressionUsesObject(x.typesInfo, statement.Results[0], x.endpointBuilderParameter(fn, 0)) != 1 {
				valid = false
				return true
			}
			questionMarks := 0
			ast.Inspect(statement.Results[0], func(child ast.Node) bool {
				switch expression := child.(type) {
				case *ast.BasicLit:
					value, exact := stringLiteral(expression)
					if !exact || value != "?" {
						valid = false
					} else {
						questionMarks++
					}
				case *ast.BinaryExpr:
					if expression.Op != token.ADD {
						valid = false
					}
				case *ast.CallExpr:
					if expression != escapeCall {
						valid = false
					}
				case *ast.Ident:
					if variable, ok := x.typesInfo.ObjectOf(expression).(*types.Var); ok && variable != x.endpointBuilderParameter(fn, 0) && variable != x.endpointBuilderParameter(fn, 1) {
						valid = false
					}
				}
				return true
			})
			if questionMarks != 1 {
				valid = false
			}
			return true
		})
		return valid
	}

	guardReturn := x.reviewedPathGuardReturn(fn)
	if fn.Name.Name != "endpointWithFallbackPathSegment" && guardReturn == nil {
		return false
	}
	assignment, ok := parents[escapeCall].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 || assignment.Rhs[0] != escapeCall {
		return false
	}
	escaped, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	escapedObject := x.typesInfo.ObjectOf(escaped)
	if escapedObject == nil {
		return false
	}
	uses := 0
	valid := true
	dynamicParameter := x.endpointBuilderParameter(fn, 1)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.AssignStmt:
			for _, left := range item.Lhs {
				identifier, direct := left.(*ast.Ident)
				if direct && x.typesInfo.ObjectOf(identifier) == escapedObject && item != assignment {
					valid = false
				}
			}
		case *ast.Ident:
			if x.typesInfo.ObjectOf(item) == escapedObject && item != escaped {
				uses++
				parent := parents[item]
				if fn.Name.Name == "endpointWithPathCapture" {
					if _, ok := parent.(*ast.BinaryExpr); !ok {
						valid = false
					}
				} else {
					call, ok := parent.(*ast.CallExpr)
					if !ok || len(call.Args) != 1 || call.Args[0] != item {
						valid = false
					} else {
						function, _ := calledFunctionObject(x.typesInfo, call.Fun).(*types.Func)
						if function == nil || function.Pkg() == nil || function.Pkg().Path() != providerPackagePath || function.Name() != "hardenDotSegment" || !x.reviewedHardenDotSegmentValid(x.funcDeclForObject(function)) {
							valid = false
						}
					}
				}
			}
		case *ast.ReturnStmt:
			if len(item.Results) != 1 {
				valid = false
				break
			}
			if x.exactInvalidEndpointReturn(item) {
				if item != guardReturn {
					valid = false
				}
				break
			}
			if expressionUsesObject(x.typesInfo, item.Results[0], escapedObject) != 1 || expressionUsesObject(x.typesInfo, item.Results[0], dynamicParameter) != 0 ||
				expressionUsesObject(x.typesInfo, item.Results[0], x.endpointBuilderParameter(fn, 0)) != 1 || expressionUsesObject(x.typesInfo, item.Results[0], x.endpointBuilderParameter(fn, 2)) != 1 {
				valid = false
			}
			ast.Inspect(item.Results[0], func(child ast.Node) bool {
				switch expression := child.(type) {
				case *ast.BasicLit:
					valid = false
				case *ast.BinaryExpr:
					if expression.Op != token.ADD {
						valid = false
					}
				case *ast.Ident:
					if variable, ok := x.typesInfo.ObjectOf(expression).(*types.Var); ok && variable != escapedObject && variable != x.endpointBuilderParameter(fn, 0) && variable != x.endpointBuilderParameter(fn, 2) {
						valid = false
					}
				}
				return true
			})
		}
		return true
	})
	return valid && uses == 1
}

// validateStrictSourcePolicy intentionally enforces a closed syntactic/type
// policy. Its provenance analysis follows only exact assignments and reviewed
// endpoint/query helpers; transport names, raw dispatch names, type erasure,
// escaping, and query mutation remain reserved to exact reviewed forms.
func (x *extractor) validateStrictSourcePolicy() error {
	var problems []string
	add := func(path string, node ast.Node, message string) {
		position := x.fset.Position(node.Pos())
		problems = append(problems, fmt.Sprintf("%s:%d: %s", filepath.Base(path), position.Line, message))
	}
	isSensitive := func(t types.Type) bool { return x.isClientType(t) || isSensitiveHTTPType(t) }
	checkQueryStorageAt := func(path string, source ast.Expr, destination types.Type, nested bool) {
		sourceType := x.typesInfo.TypeOf(source)
		if !exactURLValuesCarrierType(sourceType) {
			return
		}
		// url.Values is a map. Assignment to a true url.Values alias is the
		// same statically reviewed type, but every other destination can retain
		// and mutate the same map while hiding its keys from this analyzer.
		if !nested && exactURLValuesFlow(sourceType, destination) {
			return
		}
		add(path, source, "url.Values backing map may not be stored in a non-exact type or container")
	}
	checkCompositeQueryStorageAt := func(path string, item *ast.CompositeLit) {
		switch composite := originalGenericUnderlying(x.typesInfo.TypeOf(item)).(type) {
		case *types.Slice:
			for _, element := range item.Elts {
				checkQueryStorageAt(path, element, composite.Elem(), true)
			}
		case *types.Array:
			for _, element := range item.Elts {
				checkQueryStorageAt(path, element, composite.Elem(), true)
			}
		case *types.Map:
			for _, element := range item.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					checkQueryStorageAt(path, pair.Value, composite.Elem(), true)
				}
			}
		case *types.Struct:
			for index, element := range item.Elts {
				valueExpression := ast.Expr(element)
				fieldIndex := index
				if pair, keyed := element.(*ast.KeyValueExpr); keyed {
					valueExpression = pair.Value
					fieldIndex = -1
					if name, ok := pair.Key.(*ast.Ident); ok {
						for candidate := 0; candidate < composite.NumFields(); candidate++ {
							if composite.Field(candidate).Name() == name.Name {
								fieldIndex = candidate
								break
							}
						}
					}
				}
				if fieldIndex >= 0 && fieldIndex < composite.NumFields() {
					checkQueryStorageAt(path, valueExpression, composite.Field(fieldIndex).Type(), true)
				}
			}
		}
	}

	for path, file := range x.files {
		// Reserve transport method names at declaration time, including methods
		// acquired from embedded interfaces and generic constraints.
		ast.Inspect(file, func(node ast.Node) bool {
			switch item := node.(type) {
			case *ast.FuncDecl:
				object, _ := x.typesInfo.Defs[item.Name].(*types.Func)
				if reviewedEndpointBuilders[item.Name.Name] && !x.exactEndpointBuilder(object) {
					add(path, item, "reviewed endpoint builder names may only be declared with the exact provider signature")
				}
				if x.exactEndpointBuilder(object) && item.Body != nil && !x.reviewedBuilderBodyValid(item) {
					add(path, item, "reviewed endpoint builder implementation must use its one exact escape result exactly once in every successful return without discard, reassignment, or post-processing")
				}
				if item.Name.Name == "containsDotPathComponent" && !x.reviewedContainsDotPathComponentValid(item) {
					add(path, item, "reviewed dot-component guard must retain its exact split and component rejection semantics")
				}
				if item.Name.Name == "hardenDotSegment" && !x.reviewedHardenDotSegmentValid(item) {
					add(path, item, "reviewed dot-segment hardening helper must retain its exact canonical mapping semantics")
				}
				if clientTransportName(item.Name.Name) && !x.exactClientMethodObject(object) {
					add(path, item, "reviewed Client transport names may only be declared as exact Client methods")
				}
				if _, helper := helperRequestWrappers[item.Name.Name]; helper && !exactProviderFunction(object, helperRequestWrappers) {
					add(path, item, "reviewed transport wrapper names may only be declared as exact provider functions")
				}
				if item.Name.Name == "Do" || item.Name.Name == "RoundTrip" {
					add(path, item, "raw HTTP Do/RoundTrip declarations are forbidden")
				}
			case *ast.SelectorExpr:
				if reviewedEndpointBuilders[item.Sel.Name] {
					add(path, item, "reviewed endpoint builders require exact direct package-local calls")
				}
			case *ast.TypeSpec:
				object, _ := x.typesInfo.Defs[item.Name].(*types.TypeName)
				if object == nil {
					break
				}
				if name := methodSetExposes(object.Type(), reviewedTransportName); name != "" && !x.isClientType(object.Type()) {
					add(path, item, "declaration exposes reviewed transport name "+name+" through an interface, constraint, alias, or embedding")
				}
				if name := methodSetExposes(object.Type(), func(name string) bool { return reviewedEndpointBuilders[name] }); name != "" {
					add(path, item, "declaration exposes reviewed endpoint builder name "+name+" through an interface, constraint, alias, or embedding")
				}
				if name := methodSetExposes(object.Type(), func(name string) bool { return name == "Do" || name == "RoundTrip" }); name != "" {
					add(path, item, "declaration exposes raw HTTP "+name+" through an interface, constraint, alias, or embedding")
				}
			case *ast.InterfaceType:
				if name := methodSetExposes(x.typesInfo.TypeOf(item), reviewedTransportName); name != "" {
					add(path, item, "interface or generic constraint exposes reviewed transport name "+name)
				}
				if name := methodSetExposes(x.typesInfo.TypeOf(item), func(name string) bool { return reviewedEndpointBuilders[name] }); name != "" {
					add(path, item, "interface or generic constraint exposes reviewed endpoint builder name "+name)
				}
				if name := methodSetExposes(x.typesInfo.TypeOf(item), func(name string) bool { return name == "Do" || name == "RoundTrip" }); name != "" {
					add(path, item, "interface or generic constraint exposes raw HTTP "+name)
				}
			case *ast.ValueSpec:
				for index, source := range item.Values {
					if _, callable := x.endpointCallableForExpr(source, nil, map[types.Object]bool{}); callable {
						add(path, source, "endpoint-dispatch wrappers may not be stored in package-level declarations")
					}
					if index < len(item.Names) {
						destination := x.typesInfo.TypeOf(item.Names[index])
						if isSensitive(x.typesInfo.TypeOf(source)) && isTypeErasureDestination(destination) {
							add(path, source, "Client or HTTP transport may not be stored in a package-level interface or type parameter")
						}
						checkQueryStorageAt(path, source, destination, false)
					}
					ast.Inspect(source, func(child ast.Node) bool {
						switch expression := child.(type) {
						case *ast.Ident:
							object := x.typesInfo.Uses[expression]
							if x.exactEndpointBuilder(object) {
								add(path, expression, "reviewed endpoint builder may not be stored or invoked in a package-level declaration")
							}
							if _, callable := x.endpointCallableForExpr(expression, nil, map[types.Object]bool{}); callable {
								add(path, expression, "endpoint-dispatch wrappers may not be stored in package-level declarations")
							}
							if _, escape := exactURLEscapeFunction(object); escape || x.exactURLValuesMethod(object, "Encode") {
								add(path, expression, "URL escape functions and url.Values.Encode are forbidden in package-level declarations")
							}
						case *ast.SelectorExpr:
							object := x.typesInfo.Uses[expression.Sel]
							if selection := x.typesInfo.Selections[expression]; object == nil && selection != nil {
								object = selection.Obj()
							}
							if _, escape := exactURLEscapeFunction(object); escape || x.exactURLValuesMethod(object, "Encode") {
								add(path, expression, "URL escape functions and url.Values.Encode are forbidden in package-level declarations")
							}
							if reviewedTransportName(expression.Sel.Name) || expression.Sel.Name == "Do" || expression.Sel.Name == "RoundTrip" || x.isNetHTTPTransportObject(object) || x.reflectDynamicDispatch(object) {
								add(path, expression, "transport and reflective method values are forbidden in package-level declarations")
							}
						case *ast.AssignStmt:
							for assignmentIndex, right := range expression.Rhs {
								if assignmentIndex >= len(expression.Lhs) {
									continue
								}
								left := expression.Lhs[assignmentIndex]
								_, indexed := left.(*ast.IndexExpr)
								selector, selected := left.(*ast.SelectorExpr)
								nested := indexed || (selected && x.typesInfo.Selections[selector] != nil && x.typesInfo.Selections[selector].Kind() == types.FieldVal)
								checkQueryStorageAt(path, right, x.originalErasureDestination(left), nested)
							}
						case *ast.SendStmt:
							channel, _ := types.Unalias(x.typesInfo.TypeOf(expression.Chan)).(*types.Chan)
							if channel != nil {
								checkQueryStorageAt(path, expression.Value, channel.Elem(), true)
							}
						case *ast.DeclStmt:
							general, _ := expression.Decl.(*ast.GenDecl)
							if general != nil {
								for _, specification := range general.Specs {
									values, _ := specification.(*ast.ValueSpec)
									if values == nil {
										continue
									}
									for valueIndex, right := range values.Values {
										if valueIndex < len(values.Names) {
											checkQueryStorageAt(path, right, x.typesInfo.TypeOf(values.Names[valueIndex]), false)
										}
									}
								}
							}
						case *ast.CallExpr:
							called := calledFunctionObject(x.typesInfo, expression.Fun)
							isConversion := len(expression.Args) == 1 && x.typesInfo.Types[expression.Fun].IsType()
							exactIdentity := isConversion && exactURLValuesFlow(x.typesInfo.TypeOf(expression.Args[0]), x.typesInfo.TypeOf(expression))
							if isConversion {
								checkQueryStorageAt(path, expression.Args[0], x.typesInfo.TypeOf(expression), false)
							}
							signature, _ := x.typesInfo.TypeOf(expression.Fun).(*types.Signature)
							if function, ok := called.(*types.Func); ok {
								declared := function.Type().(*types.Signature)
								if declared.TypeParams() != nil && declared.TypeParams().Len() != 0 {
									signature = declared
								}
							}
							if signature != nil {
								for argumentIndex, argument := range expression.Args {
									parameterIndex := argumentIndex
									if signature.Variadic() && parameterIndex >= signature.Params().Len()-1 {
										parameterIndex = signature.Params().Len() - 1
									}
									if parameterIndex >= 0 && parameterIndex < signature.Params().Len() {
										parameterType := signature.Params().At(parameterIndex).Type()
										if signature.Variadic() && parameterIndex == signature.Params().Len()-1 {
											if slice, ok := types.Unalias(parameterType).(*types.Slice); ok {
												parameterType = slice.Elem()
											}
										}
										checkQueryStorageAt(path, argument, parameterType, false)
									}
								}
							}
							for _, argument := range expression.Args {
								if !exactURLValuesCarrierType(x.typesInfo.TypeOf(argument)) {
									continue
								}
								if builtin, ok := called.(*types.Builtin); ok {
									if builtin.Name() == "append" {
										checkQueryStorageAt(path, argument, x.typesInfo.TypeOf(argument), true)
									}
									continue
								}
								if !exactIdentity && !x.exactQueryHelper(called) {
									add(path, argument, "url.Values may only be passed to an exact reviewed query helper")
								}
							}
						case *ast.FuncLit:
							if _, callable := x.endpointCallableForExpr(expression, nil, map[types.Object]bool{}); callable {
								add(path, expression, "endpoint-dispatch closures may not be stored in package-level declarations")
							}
							signature, _ := x.typesInfo.TypeOf(expression.Type).(*types.Signature)
							if signature != nil {
								ast.Inspect(expression.Body, func(descendant ast.Node) bool {
									if nested, ok := descendant.(*ast.FuncLit); ok && nested != expression {
										return false
									}
									returned, ok := descendant.(*ast.ReturnStmt)
									if !ok {
										return true
									}
									for resultIndex, result := range returned.Results {
										if resultIndex < signature.Results().Len() {
											checkQueryStorageAt(path, result, signature.Results().At(resultIndex).Type(), false)
										}
									}
									return true
								})
							}
						case *ast.TypeAssertExpr:
							if exactURLValuesCarrierType(x.typesInfo.TypeOf(expression.Type)) {
								add(path, expression, "url.Values may not be recovered from an interface")
							}
						case *ast.CaseClause:
							for _, caseType := range expression.List {
								if exactURLValuesCarrierType(x.typesInfo.TypeOf(caseType)) {
									add(path, caseType, "url.Values type-switch recovery is forbidden")
								}
							}
						case *ast.CompositeLit:
							checkCompositeQueryStorageAt(path, expression)
						}
						return true
					})
				}
			}
			return true
		})

		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			parents := map[ast.Node]ast.Node{}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if node == nil {
					return false
				}
				ast.Inspect(node, func(child ast.Node) bool {
					if child != nil && child != node {
						if _, exists := parents[child]; !exists {
							parents[child] = node
						}
						return false
					}
					return true
				})
				return true
			})

			checkErasure := func(source ast.Expr, destination types.Type, message string, left ast.Expr) {
				if !isSensitive(x.typesInfo.TypeOf(source)) || !isTypeErasureDestination(destination) {
					return
				}
				if left != nil && x.isFrameworkProviderDataPublication(left, source, fn) {
					return
				}
				if isSensitiveHTTPType(x.typesInfo.TypeOf(source)) && x.isReviewedHTTPImplementation(fn) {
					return
				}
				add(path, source, message)
			}
			checkQueryStorage := func(source ast.Expr, destination types.Type, nested bool) {
				checkQueryStorageAt(path, source, destination, nested)
			}

			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch item := node.(type) {
				case *ast.TypeAssertExpr:
					if x.isClientType(x.typesInfo.TypeOf(item.Type)) && !x.isFrameworkProviderDataAssertion(item, fn) {
						add(path, item, "Client may only be recovered from framework ProviderData by an exact Configure assertion")
					}
					if isSensitiveHTTPType(x.typesInfo.TypeOf(item.Type)) && !x.isReviewedHTTPTransportAssertion(item, fn) {
						add(path, item, "HTTP transport may only be recovered from an interface by the exact reviewed transport assertion")
					}
					if exactURLValuesCarrierType(x.typesInfo.TypeOf(item.Type)) {
						add(path, item, "url.Values may not be recovered from an interface")
					}
				case *ast.CaseClause:
					for _, expression := range item.List {
						if x.isClientType(x.typesInfo.TypeOf(expression)) || isSensitiveHTTPType(x.typesInfo.TypeOf(expression)) {
							add(path, expression, "Client and HTTP transport type-switch recovery is forbidden")
						}
						if exactURLValuesCarrierType(x.typesInfo.TypeOf(expression)) {
							add(path, expression, "url.Values type-switch recovery is forbidden")
						}
					}
				case *ast.FuncLit:
					signature, _ := x.typesInfo.TypeOf(item.Type).(*types.Signature)
					if signature == nil {
						break
					}
					ast.Inspect(item.Body, func(child ast.Node) bool {
						if nested, ok := child.(*ast.FuncLit); ok && nested != item {
							return false
						}
						returned, ok := child.(*ast.ReturnStmt)
						if !ok {
							return true
						}
						for index, source := range returned.Results {
							if index < signature.Results().Len() {
								destination := signature.Results().At(index).Type()
								checkErasure(source, destination, "returning Client as an interface is forbidden; HTTP transports may not be returned as an interface or type parameter", nil)
								checkQueryStorage(source, destination, false)
							}
						}
						return true
					})
				case *ast.SelectorExpr:
					selection := x.typesInfo.Selections[item]
					object := x.typesInfo.Uses[item.Sel]
					if object == nil && selection != nil {
						object = selection.Obj()
					}
					direct := x.isDirectCall(item, parents)
					if _, escape := exactURLEscapeFunction(object); escape || x.exactURLValuesMethod(object, "Encode") {
						call, calledDirectly := parents[item].(*ast.CallExpr)
						if !calledDirectly || call.Fun != item || !x.reviewedEscapeCallAllowed(object, call, fn) {
							add(path, item, "URL escape functions and url.Values.Encode are restricted to one direct call in the exact reviewed endpoint builder internals")
						}
					}
					if reviewedTransportName(item.Sel.Name) {
						exactClient := selection != nil && selection.Kind() == types.MethodVal && x.isClientType(selection.Recv()) && x.exactClientMethodObject(selection.Obj())
						if !exactClient {
							add(path, item, "reviewed Client transport selector must resolve to the exact Client object; interface/generic dispatch and embedding and promotion are forbidden")
						} else if !direct {
							add(path, item, "Client transport methods may not be used as values or method expressions")
						}
					}
					if item.Sel.Name == "Do" || item.Sel.Name == "RoundTrip" || x.isNetHTTPTransportObject(object) {
						approvedRequest := direct && x.functionHasProviderReceiver(fn, "Client") && fn.Name.Name == "prepareRequest" && object != nil && object.Name() == "NewRequestWithContext" && selection == nil
						approvedDo := direct && x.isReviewedHTTPImplementation(fn) && fn.Name.Name == "executeRequestWithOptions" && object != nil && object.Name() == "Do" &&
							selection != nil && selection.Kind() == types.MethodVal && exactNamedType(selection.Recv(), "net/http", "Client")
						approved := approvedRequest || approvedDo
						if !approved {
							add(path, item, "raw net/http transport reference is not an exact reviewed Client implementation call")
						}
					}
					if x.exactURLValuesMethod(object, "Set", "Add") {
						call, called := parents[item].(*ast.CallExpr)
						if !called || call.Fun != item || selection == nil || selection.Kind() != types.MethodVal {
							add(path, item, "url.Values Set/Add may not be used as a bound method value or method expression")
						} else if len(call.Args) == 0 {
							add(path, item, "dynamic url.Values Set/Add key is forbidden")
						} else if _, literal := stringLiteral(call.Args[0]); !literal && fn.Name.Name != "addKnownStringFilter" {
							add(path, call.Args[0], "dynamic url.Values Set/Add key is forbidden")
						}
					}
					if x.reflectDynamicDispatch(object) {
						add(path, item, "reflection-based method lookup or invocation is forbidden in provider code")
					}
				case *ast.Ident:
					object := x.typesInfo.Uses[item]
					if object == nil {
						break
					}
					parentSelector, selectorIdentifier := parents[item].(*ast.SelectorExpr)
					if !selectorIdentifier || parentSelector.Sel != item {
						if _, escape := exactURLEscapeFunction(object); escape || x.exactURLValuesMethod(object, "Encode") {
							call, calledDirectly := parents[item].(*ast.CallExpr)
							if !calledDirectly || call.Fun != item || !x.reviewedEscapeCallAllowed(object, call, fn) {
								add(path, item, "URL escape functions and url.Values.Encode are restricted to one direct call in the exact reviewed endpoint builder internals")
							}
						}
					}
					if _, helper := helperRequestWrappers[item.Name]; helper && exactProviderFunction(object, helperRequestWrappers) {
						call, direct := parents[item].(*ast.CallExpr)
						if !direct || call.Fun != item {
							add(path, item, "reviewed transport wrapper may not be used as a value")
						}
					}
					if x.exactQueryHelper(object) {
						call, direct := parents[item].(*ast.CallExpr)
						if !direct || call.Fun != item {
							add(path, item, "reviewed query helper may not be used as a value")
						}
					}
					if x.exactEndpointBuilder(object) {
						call, direct := parents[item].(*ast.CallExpr)
						if !direct || call.Fun != item {
							add(path, item, "reviewed endpoint builder may only be called directly")
						}
					}
					if (x.isNetHTTPTransportObject(object) || x.reflectDynamicDispatch(object)) && (!selectorIdentifier || parentSelector.Sel != item) {
						add(path, item, "raw net/http transport or reflective dispatch function may not be used as a value")
					}
				case *ast.SendStmt:
					channel, _ := types.Unalias(x.typesInfo.TypeOf(item.Chan)).(*types.Chan)
					if channel != nil {
						checkQueryStorage(item.Value, channel.Elem(), true)
					}
				case *ast.AssignStmt:
					for index, source := range item.Rhs {
						if index < len(item.Lhs) {
							left := item.Lhs[index]
							if _, callable := x.endpointCallableForExpr(source, fn, map[types.Object]bool{}); callable {
								_, directAlias := left.(*ast.Ident)
								if !directAlias || !types.Identical(x.typesInfo.TypeOf(left), x.typesInfo.TypeOf(source)) {
									add(path, source, "endpoint-dispatch wrappers may only flow through exact local function aliases")
								}
							}
							if indexed, ok := left.(*ast.IndexExpr); ok && exactURLValuesCarrierType(x.typesInfo.TypeOf(indexed.X)) && !x.rawStringSliceProvenance(source, fn) {
								add(path, source, "url.Values assigned leaves must retain approved raw-input provenance")
							}
							destination := x.originalErasureDestination(left)
							checkErasure(source, destination, "Client-to-interface assignment is forbidden; HTTP transports may not be assigned to an interface or type parameter", left)
							_, indexed := left.(*ast.IndexExpr)
							selector, selected := left.(*ast.SelectorExpr)
							nested := indexed || (selected && x.typesInfo.Selections[selector] != nil && x.typesInfo.Selections[selector].Kind() == types.FieldVal)
							checkQueryStorage(source, destination, nested)
						}
					}
				case *ast.DeclStmt:
					general, ok := item.Decl.(*ast.GenDecl)
					if !ok {
						break
					}
					for _, specification := range general.Specs {
						values, ok := specification.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for index, source := range values.Values {
							if index < len(values.Names) {
								destination := x.typesInfo.TypeOf(values.Names[index])
								if _, callable := x.endpointCallableForExpr(source, fn, map[types.Object]bool{}); callable && !types.Identical(destination, x.typesInfo.TypeOf(source)) {
									add(path, source, "endpoint-dispatch wrappers may only flow through exact local function aliases")
								}
								checkErasure(source, destination, "Client-to-interface assignment is forbidden; HTTP transports may not be stored in an interface or type parameter", values.Names[index])
								checkQueryStorage(source, destination, false)
							}
						}
					}
				case *ast.ReturnStmt:
					for _, source := range item.Results {
						if _, callable := x.endpointCallableForExpr(source, fn, map[types.Object]bool{}); callable {
							add(path, source, "endpoint-dispatch wrappers may not be returned or stored indirectly")
						}
					}
					function, _ := x.typesInfo.Defs[fn.Name].(*types.Func)
					if function != nil {
						signature := function.Type().(*types.Signature)
						for index, source := range item.Results {
							if index < signature.Results().Len() {
								destination := signature.Results().At(index).Type()
								checkErasure(source, destination, "returning Client as an interface is forbidden; HTTP transports may not be returned as an interface or type parameter", nil)
								checkQueryStorage(source, destination, false)
							}
						}
					}
				case *ast.CallExpr:
					called := calledFunctionObject(x.typesInfo, item.Fun)
					resolved, proven := x.functionValue(item.Fun, fn, map[types.Object]bool{})
					if proven && x.interfaceEndpointDispatchCollision(resolved) {
						add(path, item, "interface dynamic string dispatch is forbidden unless an exact concrete callable is proven")
					}
					if x.exactEndpointBuilder(called) {
						if fn.Type.TypeParams != nil && len(fn.Type.TypeParams.List) != 0 {
							add(path, item, "reviewed endpoint builders may not be called from generic functions")
						}
						switch called.Name() {
						case "endpointWithPathSegment", "endpointWithPathCapture", "endpointWithFallbackPathSegment":
							if len(item.Args) != 3 {
								add(path, item, "reviewed path builder requires three arguments")
								break
							}
							if !x.rawStringProvenance(item.Args[1], fn, map[types.Object]bool{}) {
								add(path, item.Args[1], "reviewed path builder dynamic argument must retain approved raw-input provenance")
							}
							prefix, prefixLiteral := stringLiteral(item.Args[0])
							suffix, suffixLiteral := stringLiteral(item.Args[2])
							if !prefixLiteral || !suffixLiteral || !strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix+suffix, "?#{}") {
								add(path, item, "reviewed path builder requires literal unqueried prefix and suffix boundaries")
							}
							if called.Name() == "endpointWithFallbackPathSegment" && (prefix != "/fallback/" || suffix != "") {
								add(path, item, "fallback slash exception is restricted to the exact /fallback/{model} path")
							}
						case "endpointWithQuery":
							if len(item.Args) != 2 {
								add(path, item, "reviewed query builder requires two arguments")
								break
							}
							if !x.rawURLValuesProvenance(item.Args[1], fn, map[types.Object]bool{}) {
								add(path, item.Args[1], "reviewed query builder values must retain approved raw-input provenance")
							}
							pathValue := x.eval(item.Args[0], x.functionEnv(fn, nil, map[string]bool{}), map[string]bool{})
							valid := pathValue.ok && len(pathValue.shapes) > 0 && !pathValue.unresolvedQuery
							for _, shape := range pathValue.shapes {
								pathShape, query, unresolved := splitShape(shape)
								if !strings.HasPrefix(pathShape, "/") || len(query) != 0 || unresolved || strings.Contains(pathShape, "#") {
									valid = false
								}
							}
							if !valid {
								add(path, item.Args[0], "reviewed query builder requires a statically unqueried endpoint path")
							}
						}
					}
					if function, ok := called.(*types.Func); ok && function.Pkg() != nil && function.Pkg().Path() == providerPackagePath {
						callee := x.funcDeclForObject(function)
						if callee != nil && x.functionDirectlyUsesEndpoint(callee) {
							callable := reviewedEndpointCallable{params: callee.Type.Params, body: callee.Body}
							if !x.endpointCallableArgumentsValid(callable, item.Args, fn) {
								add(path, item, "approved endpoint helper arguments must retain raw-input provenance")
							}
						}
					}
					if _, directFunction := called.(*types.Func); !directFunction {
						if callable, ok := x.endpointCallableForExpr(item.Fun, fn, map[types.Object]bool{}); ok && !x.endpointCallableArgumentsValid(callable, item.Args, fn) {
							add(path, item, "aliased endpoint-dispatch wrapper arguments must retain approved raw-input provenance")
						}
					}
					for argumentIndex, argument := range item.Args {
						if _, ok := x.endpointCallableForExpr(argument, fn, map[types.Object]bool{}); ok {
							function, _ := called.(*types.Func)
							if !x.reviewedHigherOrderConsumer(function, argumentIndex) {
								add(path, argument, "endpoint-dispatch wrappers may not be passed through higher-order or interface storage")
							}
						}
					}
					if x.exactURLValuesMethod(called, "Set", "Add") && len(item.Args) >= 2 && !x.rawStringProvenance(item.Args[1], fn, map[types.Object]bool{}) {
						add(path, item.Args[1], "url.Values Set/Add value must retain approved raw-input provenance")
					}
					if x.exactQueryHelper(called) {
						for _, argument := range item.Args {
							if exactURLValuesCarrierType(x.typesInfo.TypeOf(argument)) && !x.rawURLValuesProvenance(argument, fn, map[types.Object]bool{}) {
								add(path, argument, "approved query helper argument must retain raw-input provenance")
							}
						}
					}
					isConversion := len(item.Args) == 1 && x.typesInfo.Types[item.Fun].IsType()
					urlValuesConversion := isConversion && exactURLValuesCarrierType(x.typesInfo.TypeOf(item))
					exactURLValuesIdentity := urlValuesConversion && exactURLValuesFlow(x.typesInfo.TypeOf(item.Args[0]), x.typesInfo.TypeOf(item))
					if isConversion {
						checkQueryStorage(item.Args[0], x.typesInfo.TypeOf(item), false)
					}
					if urlValuesConversion && !exactURLValuesIdentity {
						// A conversion from a map, defined type, pointer, or type parameter can
						// hide dynamic-key mutation before the value regains url.Values methods.
						// Only an identity conversion from the exact analyzed url.Values type
						// (including a true alias) preserves the query provenance we inspected.
						add(path, item, "non-identity conversion to exact url.Values is forbidden")
					}
					if reviewedTransportName(callName(item.Fun)) {
						selector, selected := item.Fun.(*ast.SelectorExpr)
						exactClient := selected && x.typesInfo.Selections[selector] != nil && x.typesInfo.Selections[selector].Kind() == types.MethodVal &&
							x.isClientType(x.typesInfo.Selections[selector].Recv()) && x.exactClientMethodObject(x.typesInfo.Selections[selector].Obj())
						exactHelper := !selected && exactProviderFunction(called, helperRequestWrappers)
						if !exactClient && !exactHelper {
							add(path, item, "reviewed transport call does not resolve to an exact Client method or reviewed wrapper")
						} else if approvedClientTransportInternals[callName(item.Fun)] && !x.functionHasProviderReceiver(fn, "Client") {
							add(path, item, "internal Client transport method called outside Client implementation")
						}
					}
					if callName(item.Fun) == "Do" || callName(item.Fun) == "RoundTrip" {
						selector, selected := item.Fun.(*ast.SelectorExpr)
						selection := x.typesInfo.Selections[selector]
						approved := selected && selection != nil && selection.Kind() == types.MethodVal && called != nil && called.Name() == "Do" &&
							exactNamedType(selection.Recv(), "net/http", "Client") && x.isReviewedHTTPImplementation(fn) && fn.Name.Name == "executeRequestWithOptions"
						if !approved {
							add(path, item, "raw Do/RoundTrip dispatch is forbidden outside the exact reviewed Client implementation")
						}
					}
					if len(item.Args) == 1 && x.typesInfo.Types[item.Fun].IsType() {
						checkErasure(item.Args[0], x.typesInfo.TypeOf(item), "Client-to-interface conversion is forbidden; HTTP transports may not be converted to an interface or type parameter", nil)
					}
					signature, _ := x.typesInfo.TypeOf(item.Fun).(*types.Signature)
					// Inspect the declaration signature for generic calls. The inferred
					// instance replaces T with the argument type and would otherwise hide
					// the type-erasing boundary itself.
					if function, ok := called.(*types.Func); ok {
						declared := function.Type().(*types.Signature)
						if declared.TypeParams() != nil && declared.TypeParams().Len() != 0 {
							signature = declared
						}
					}
					if signature != nil {
						for index, argument := range item.Args {
							parameterIndex := index
							if signature.Variadic() && parameterIndex >= signature.Params().Len()-1 {
								parameterIndex = signature.Params().Len() - 1
							}
							if parameterIndex >= 0 && parameterIndex < signature.Params().Len() {
								parameterType := signature.Params().At(parameterIndex).Type()
								if signature.Variadic() && parameterIndex == signature.Params().Len()-1 {
									if slice, ok := types.Unalias(parameterType).(*types.Slice); ok {
										parameterType = slice.Elem()
									}
								}
								checkErasure(argument, parameterType, "passing Client to an interface parameter is forbidden; HTTP transports may not be passed to an interface or type parameter", nil)
								checkQueryStorage(argument, parameterType, false)
							}
						}
					}
					if builtin, ok := called.(*types.Builtin); ok && builtin.Name() == "append" && len(item.Args) > 1 {
						for _, argument := range item.Args[1:] {
							checkQueryStorage(argument, x.typesInfo.TypeOf(argument), true)
						}
					}
					for _, argument := range item.Args {
						if !x.isURLValuesExpr(argument) {
							continue
						}
						if _, builtin := called.(*types.Builtin); builtin {
							continue
						}
						if exactURLValuesIdentity {
							continue
						}
						if !x.exactQueryHelper(called) {
							add(path, argument, "url.Values may only be passed to an exact reviewed query helper")
						}
					}
					if called != nil && called.Name() == "addKnownStringFilter" {
						if !x.exactQueryHelper(called) || len(item.Args) < 2 {
							add(path, item, "unresolved reviewed query helper call")
						} else if _, literal := stringLiteral(item.Args[1]); !literal {
							add(path, item.Args[1], "reviewed query helper requires a literal key")
						}
					}
				case *ast.IndexExpr:
					if x.isURLValuesExpr(item.X) {
						if _, literal := stringLiteral(item.Index); !literal && fn.Name.Name != "cloneURLValues" {
							add(path, item.Index, "dynamic url.Values index key is forbidden")
						}
					}
				case *ast.CompositeLit:
					for _, element := range item.Elts {
						source := ast.Expr(element)
						if pair, ok := element.(*ast.KeyValueExpr); ok {
							source = pair.Value
						}
						if _, callable := x.endpointCallableForExpr(source, fn, map[types.Object]bool{}); callable {
							add(path, source, "endpoint-dispatch wrappers may not be stored in composites")
						}
					}
					checkCompositeQueryStorageAt(path, item)
					if x.isURLValuesExpr(item) {
						for _, element := range item.Elts {
							if pair, ok := element.(*ast.KeyValueExpr); ok {
								if _, literal := stringLiteral(pair.Key); !literal {
									add(path, pair.Key, "dynamic url.Values literal key is forbidden")
								}
							}
						}
					}
					underlying := x.typesInfo.TypeOf(item)
					if underlying == nil {
						break
					}
					// Use the generic origin's fields/elements. Instantiation replaces T
					// with *http.Client (for example), which must not erase the fact that
					// the storage boundary was a type parameter.
					switch composite := originalGenericUnderlying(underlying).(type) {
					case *types.Slice:
						for _, element := range item.Elts {
							checkErasure(element, composite.Elem(), "Client or HTTP transport may not be stored in an interface collection", nil)
						}
					case *types.Map:
						for _, element := range item.Elts {
							if pair, ok := element.(*ast.KeyValueExpr); ok {
								checkErasure(pair.Value, composite.Elem(), "Client or HTTP transport may not be stored in an interface collection", nil)
							}
						}
					case *types.Struct:
						for index, element := range item.Elts {
							valueExpression := ast.Expr(element)
							fieldIndex := index
							if pair, keyed := element.(*ast.KeyValueExpr); keyed {
								valueExpression = pair.Value
								fieldIndex = -1
								if name, ok := pair.Key.(*ast.Ident); ok {
									for candidate := 0; candidate < composite.NumFields(); candidate++ {
										if composite.Field(candidate).Name() == name.Name {
											fieldIndex = candidate
											break
										}
									}
								}
							}
							if fieldIndex >= 0 && fieldIndex < composite.NumFields() {
								fieldType := composite.Field(fieldIndex).Type()
								checkErasure(valueExpression, fieldType, "storing Client in an interface field is forbidden; HTTP transports may not be stored in an interface or type-parameter field", nil)
							}
						}
					}
				}
				return true
			})

			if x.functionHasProviderReceiver(fn, "Client") && !clientTransportName(fn.Name.Name) {
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					for _, argument := range call.Args {
						if x.isClientType(x.typesInfo.TypeOf(argument)) {
							add(path, call, "unknown Client HTTP abstraction "+fn.Name.Name)
							return false
						}
					}
					selector, selected := call.Fun.(*ast.SelectorExpr)
					selection := x.typesInfo.Selections[selector]
					if selected && selection != nil && x.exactClientMethodObject(selection.Obj()) {
						add(path, call, "unknown Client HTTP abstraction "+fn.Name.Name)
						return false
					}
					return true
				})
			}
		}
	}
	if len(problems) != 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func (x *extractor) validateLegacyStrictSourcePolicy() error {
	var problems []string
	for path, file := range x.files {
		parents := map[ast.Node]ast.Node{}
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				return false
			}
			ast.Inspect(node, func(child ast.Node) bool {
				if child != nil && child != node {
					if _, exists := parents[child]; !exists {
						parents[child] = node
					}
					return false
				}
				return true
			})
			return true
		})
		base := filepath.Base(path)
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch item := node.(type) {
				case *ast.SelectorExpr:
					selection := x.typesInfo.Selections[item]
					object := x.typesInfo.Uses[item.Sel]
					if object == nil && selection != nil {
						object = selection.Obj()
					}
					position := x.fset.Position(item.Pos())
					if x.isNetHTTPTransportObject(object) {
						approved := x.isDirectCall(item, parents) && x.functionHasProviderReceiver(fn, "Client") &&
							((fn.Name.Name == "prepareRequest" && object.Name() == "NewRequestWithContext") ||
								(fn.Name.Name == "executeRequestWithOptions" && object.Name() == "Do"))
						if !approved {
							problems = append(problems, fmt.Sprintf("%s:%d: raw net/http transport reference is not an exact reviewed Client implementation call", base, position.Line))
						}
					}
					if selection != nil && x.isClientTransportObject(selection.Obj()) {
						if !x.isClientType(selection.Recv()) {
							problems = append(problems, fmt.Sprintf("%s:%d: Client transport calls require an exact Client receiver; embedding and promotion are forbidden", base, position.Line))
						} else if !x.isDirectCall(item, parents) {
							problems = append(problems, fmt.Sprintf("%s:%d: Client transport methods may not be used as values", base, position.Line))
						}
					}
				case *ast.AssignStmt:
					for index, right := range item.Rhs {
						if index < len(item.Lhs) && x.isClientType(x.typesInfo.TypeOf(right)) && isInterfaceType(x.typesInfo.TypeOf(item.Lhs[index])) {
							position := x.fset.Position(right.Pos())
							problems = append(problems, fmt.Sprintf("%s:%d: Client-to-interface assignment is forbidden", base, position.Line))
						}
					}
				case *ast.DeclStmt:
					general, ok := item.Decl.(*ast.GenDecl)
					if !ok {
						break
					}
					for _, specification := range general.Specs {
						values, ok := specification.(*ast.ValueSpec)
						if !ok || values.Type == nil || !isInterfaceType(x.typesInfo.TypeOf(values.Type)) {
							continue
						}
						for _, right := range values.Values {
							if x.isClientType(x.typesInfo.TypeOf(right)) {
								position := x.fset.Position(right.Pos())
								problems = append(problems, fmt.Sprintf("%s:%d: Client-to-interface assignment is forbidden", base, position.Line))
							}
						}
					}
				case *ast.ReturnStmt:
					function, _ := x.typesInfo.Defs[fn.Name].(*types.Func)
					if function != nil {
						signature := function.Type().(*types.Signature)
						for index, result := range item.Results {
							if index < signature.Results().Len() && x.isClientType(x.typesInfo.TypeOf(result)) && isInterfaceType(signature.Results().At(index).Type()) {
								position := x.fset.Position(result.Pos())
								problems = append(problems, fmt.Sprintf("%s:%d: returning Client as an interface is forbidden", base, position.Line))
							}
						}
					}
				case *ast.CallExpr:
					if len(item.Args) == 1 && x.typesInfo.Types[item.Fun].IsType() && isInterfaceType(x.typesInfo.TypeOf(item)) && x.isClientType(x.typesInfo.TypeOf(item.Args[0])) {
						position := x.fset.Position(item.Pos())
						problems = append(problems, fmt.Sprintf("%s:%d: Client-to-interface conversion is forbidden", base, position.Line))
					}
					signature, _ := x.typesInfo.TypeOf(item.Fun).(*types.Signature)
					if signature != nil {
						for index, argument := range item.Args {
							parameterIndex := index
							if signature.Variadic() && parameterIndex >= signature.Params().Len()-1 {
								parameterIndex = signature.Params().Len() - 1
							}
							var parameterType types.Type
							if parameterIndex >= 0 && parameterIndex < signature.Params().Len() {
								parameterType = signature.Params().At(parameterIndex).Type()
								if signature.Variadic() && parameterIndex == signature.Params().Len()-1 {
									parameterType = parameterType.(*types.Slice).Elem()
								}
							}
							if x.isClientType(x.typesInfo.TypeOf(argument)) && isInterfaceType(parameterType) {
								position := x.fset.Position(argument.Pos())
								problems = append(problems, fmt.Sprintf("%s:%d: passing Client to an interface parameter is forbidden", base, position.Line))
							}
						}
					}
					queryHelpers := map[string]bool{
						"endpointWithQuery": true, "cloneURLValues": true, "safeListDiagnostic": true,
						"addKnownStringFilter": true, "listKeys": true, "listUsers": true,
					}
					for _, argument := range item.Args {
						if !x.isURLValuesExpr(argument) {
							continue
						}
						object := calledFunctionObject(x.typesInfo, item.Fun)
						if _, builtin := object.(*types.Builtin); builtin {
							continue
						}
						_, isFunction := object.(*types.Func)
						approved := isFunction && object.Pkg() != nil && object.Pkg().Path() == "github.com/nicholas-cecere/terraform-provider-litellm/internal/provider" && queryHelpers[object.Name()]
						if !approved {
							position := x.fset.Position(argument.Pos())
							problems = append(problems, fmt.Sprintf("%s:%d: url.Values may only be passed to an exact reviewed query helper", base, position.Line))
						}
					}
					if callName(item.Fun) == "addKnownStringFilter" {
						object := calledFunctionObject(x.typesInfo, item.Fun)
						if object == nil || object.Pkg() == nil || object.Pkg().Path() != "github.com/nicholas-cecere/terraform-provider-litellm/internal/provider" || len(item.Args) < 2 {
							position := x.fset.Position(item.Pos())
							problems = append(problems, fmt.Sprintf("%s:%d: unresolved reviewed query helper call", base, position.Line))
						} else if _, literal := stringLiteral(item.Args[1]); !literal {
							position := x.fset.Position(item.Args[1].Pos())
							problems = append(problems, fmt.Sprintf("%s:%d: reviewed query helper requires a literal key", base, position.Line))
						}
					}
					selector, ok := item.Fun.(*ast.SelectorExpr)
					if ok && (selector.Sel.Name == "Set" || selector.Sel.Name == "Add") && x.isURLValuesExpr(selector.X) && len(item.Args) > 0 {
						if _, literal := stringLiteral(item.Args[0]); !literal && fn.Name.Name != "addKnownStringFilter" {
							position := x.fset.Position(item.Args[0].Pos())
							problems = append(problems, fmt.Sprintf("%s:%d: dynamic url.Values Set/Add key is forbidden", base, position.Line))
						}
					}
				case *ast.CompositeLit:
					switch compositeType := x.typesInfo.TypeOf(item).Underlying().(type) {
					case *types.Slice:
						if isInterfaceType(compositeType.Elem()) {
							for _, element := range item.Elts {
								if x.isClientType(x.typesInfo.TypeOf(element)) {
									position := x.fset.Position(element.Pos())
									problems = append(problems, fmt.Sprintf("%s:%d: storing Client in an interface collection is forbidden", base, position.Line))
								}
							}
						}
					case *types.Map:
						if isInterfaceType(compositeType.Elem()) {
							for _, element := range item.Elts {
								if pair, ok := element.(*ast.KeyValueExpr); ok && x.isClientType(x.typesInfo.TypeOf(pair.Value)) {
									position := x.fset.Position(pair.Value.Pos())
									problems = append(problems, fmt.Sprintf("%s:%d: storing Client in an interface collection is forbidden", base, position.Line))
								}
							}
						}
					case *types.Struct:
						for index, element := range item.Elts {
							valueExpression := ast.Expr(element)
							fieldIndex := index
							if pair, keyed := element.(*ast.KeyValueExpr); keyed {
								valueExpression = pair.Value
								fieldIndex = -1
								if name, ok := pair.Key.(*ast.Ident); ok {
									for candidate := 0; candidate < compositeType.NumFields(); candidate++ {
										if compositeType.Field(candidate).Name() == name.Name {
											fieldIndex = candidate
											break
										}
									}
								}
							}
							if fieldIndex >= 0 && fieldIndex < compositeType.NumFields() && isInterfaceType(compositeType.Field(fieldIndex).Type()) && x.isClientType(x.typesInfo.TypeOf(valueExpression)) {
								position := x.fset.Position(valueExpression.Pos())
								problems = append(problems, fmt.Sprintf("%s:%d: storing Client in an interface field is forbidden", base, position.Line))
							}
						}
					}
					if x.isURLValuesExpr(item) {
						for _, element := range item.Elts {
							if pair, ok := element.(*ast.KeyValueExpr); ok {
								if _, literal := stringLiteral(pair.Key); !literal {
									position := x.fset.Position(pair.Key.Pos())
									problems = append(problems, fmt.Sprintf("%s:%d: dynamic url.Values literal key is forbidden", base, position.Line))
								}
							}
						}
					}
				case *ast.Ident:
					object := x.typesInfo.Uses[item]
					parentSelector, selectorIdentifier := parents[item].(*ast.SelectorExpr)
					if x.isNetHTTPTransportObject(object) && (!selectorIdentifier || parentSelector.Sel != item) {
						position := x.fset.Position(item.Pos())
						problems = append(problems, fmt.Sprintf("%s:%d: raw net/http transport reference is not an exact reviewed Client implementation call", base, position.Line))
					}
					function, ok := object.(*types.Func)
					if ok && function.Pkg() != nil && function.Pkg().Path() == "github.com/nicholas-cecere/terraform-provider-litellm/internal/provider" && function.Name() == "addKnownStringFilter" {
						call, direct := parents[item].(*ast.CallExpr)
						if !direct || call.Fun != item {
							position := x.fset.Position(item.Pos())
							problems = append(problems, fmt.Sprintf("%s:%d: reviewed query helper may not be used as a value", base, position.Line))
						}
					}
				}
				return true
			})
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				assignment, ok := node.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, left := range assignment.Lhs {
					index, ok := left.(*ast.IndexExpr)
					if !ok || !x.isURLValuesExpr(index.X) {
						continue
					}
					if _, literal := stringLiteral(index.Index); !literal && fn.Name.Name != "cloneURLValues" {
						position := x.fset.Position(index.Index.Pos())
						problems = append(problems, fmt.Sprintf("%s:%d: dynamic url.Values index key is forbidden", base, position.Line))
					}
				}
				return true
			})
		}
	}
	if len(problems) != 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func calledFunctionObject(info *types.Info, expression ast.Expr) types.Object {
	switch item := expression.(type) {
	case *ast.Ident:
		return info.Uses[item]
	case *ast.SelectorExpr:
		if selection := info.Selections[item]; selection != nil {
			return selection.Obj()
		}
		return info.Uses[item.Sel]
	default:
		return nil
	}
}

func (x *extractor) functionValue(expr ast.Expr, fn *ast.FuncDecl, seen map[types.Object]bool) (*types.Func, bool) {
	switch item := expr.(type) {
	case *ast.ParenExpr:
		return x.functionValue(item.X, fn, seen)
	case *ast.Ident, *ast.SelectorExpr:
		object := calledFunctionObject(x.typesInfo, expr)
		if function, ok := object.(*types.Func); ok {
			return function, true
		}
		if object == nil || seen[object] {
			return nil, false
		}
		seen[object] = true
		var resolved *types.Func
		found := false
		for _, source := range x.variableSources(fn, object) {
			candidate, ok := x.functionValue(source, fn, seen)
			if !ok || found && candidate != resolved {
				return nil, false
			}
			resolved, found = candidate, true
		}
		return resolved, found
	}
	return nil, false
}

func (x *extractor) validateClientTransport() error {
	freeFunctions := map[*types.Func]*ast.FuncDecl{}
	for function, fn := range x.funcDecls {
		if function.Type().(*types.Signature).Recv() == nil {
			freeFunctions[function] = fn
		}
	}
	freeDirect := map[*types.Func]bool{}
	freeEdges := map[*types.Func]map[*types.Func]bool{}
	for function, fn := range freeFunctions {
		freeEdges[function] = map[*types.Func]bool{}
		aliases := x.functionCallAliases(fn)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			called, proven := x.functionValue(call.Fun, fn, map[types.Object]bool{})
			if proven && (x.isNetHTTPTransportObject(called) || x.exactClientMethodObject(called) || exactProviderFunction(called, helperRequestWrappers)) {
				freeDirect[function] = true
			}
			if target, found := x.resolveCallTarget(call, fn, aliases); found && target.clientCall {
				freeDirect[function] = true
			}
			if freeFunctions[called] != nil {
				freeEdges[function][called] = true
			}
			return true
		})
	}
	freeReaches := map[*types.Func]bool{}
	changed := true
	for changed {
		changed = false
		for function := range freeFunctions {
			if freeReaches[function] {
				continue
			}
			if freeDirect[function] {
				freeReaches[function] = true
				changed = true
				continue
			}
			for called := range freeEdges[function] {
				if freeReaches[called] {
					freeReaches[function] = true
					changed = true
					break
				}
			}
		}
	}

	direct := map[*types.Func]bool{}
	edges := map[*types.Func]map[*types.Func]bool{}
	for method, fn := range x.clientMethods {
		edges[method] = map[*types.Func]bool{}
		aliases := x.functionCallAliases(fn)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			called, proven := x.functionValue(call.Fun, fn, map[types.Object]bool{})
			if proven && (x.isNetHTTPTransportObject(called) || x.exactClientMethodObject(called) || exactProviderFunction(called, helperRequestWrappers) || freeReaches[called]) {
				direct[method] = true
			}
			if target, found := x.resolveCallTarget(call, fn, aliases); found && target.clientCall {
				direct[method] = true
			}
			if x.clientMethods[called] != nil {
				edges[method][called] = true
			}
			return true
		})
	}
	reachesTransport := map[*types.Func]bool{}
	changed = true
	for changed {
		changed = false
		for method := range x.clientMethods {
			if reachesTransport[method] {
				continue
			}
			if direct[method] {
				reachesTransport[method] = true
				changed = true
				continue
			}
			for called := range edges[method] {
				if reachesTransport[called] {
					reachesTransport[method] = true
					changed = true
					break
				}
			}
		}
	}
	var problems []string
	for method := range reachesTransport {
		name := method.Name()
		_, requestApproved := clientRequestMethods[name]
		if !requestApproved && !approvedClientTransportInternals[name] {
			position := x.fset.Position(x.clientMethods[method].Pos())
			problems = append(problems, fmt.Sprintf("%s:%d: unknown Client HTTP abstraction %s", filepath.Base(x.funcFiles[method]), position.Line, name))
		}
	}
	for path, file := range x.files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			declared, _ := x.typesInfo.Defs[fn.Name].(*types.Func)
			if x.clientMethods[declared] != nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				called, proven := x.functionValue(call.Fun, fn, map[types.Object]bool{})
				if proven && x.exactClientMethodObject(called) && approvedClientTransportInternals[called.Name()] {
					position := x.fset.Position(call.Pos())
					problems = append(problems, fmt.Sprintf("%s:%d: internal Client transport method called outside Client implementation", filepath.Base(path), position.Line))
				}
				return true
			})
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func dereferencedIdent(expr ast.Expr) (*ast.Ident, bool) {
	for {
		switch item := expr.(type) {
		case *ast.Ident:
			return item, true
		case *ast.ParenExpr:
			expr = item.X
		case *ast.StarExpr:
			expr = item.X
		default:
			return nil, false
		}
	}
}

func (x *extractor) functionEnv(fn *ast.FuncDecl, bindings map[string]value, stack map[string]bool) map[string]value {
	env := map[string]value{}
	for k, v := range bindings {
		env[k] = v
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			for _, n := range field.Names {
				if _, ok := env[n.Name]; !ok {
					env[n.Name] = dynamicValue()
				}
			}
		}
	}
	// Fixed point handles endpoint aliases and helpers declared before or after query mutation.
	for pass := 0; pass < 4; pass++ {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range node.Lhs {
					if index, ok := lhs.(*ast.IndexExpr); ok {
						id, identifier := dereferencedIdent(index.X)
						if !identifier || !x.isURLValuesExpr(index.X) {
							continue
						}
						v := env[id.Name]
						if v.queries == nil {
							v = literalValue("")
						}
						if query, literal := stringLiteral(index.Index); literal {
							v.queries[query] = true
						} else {
							v.unresolvedQuery = true
						}
						env[id.Name] = v
						continue
					}
					id, ok := lhs.(*ast.Ident)
					if !ok || i >= len(node.Rhs) {
						continue
					}
					v := x.eval(node.Rhs[i], env, stack)
					if v.ok {
						if prior, exists := env[id.Name]; exists {
							env[id.Name] = mergeValues(prior, v)
						} else {
							env[id.Name] = v
						}
					}
				}
			case *ast.DeclStmt:
				gen, ok := node.Decl.(*ast.GenDecl)
				if !ok {
					break
				}
				for _, s := range gen.Specs {
					vs := s.(*ast.ValueSpec)
					for i, n := range vs.Names {
						if i < len(vs.Values) {
							v := x.eval(vs.Values[i], env, stack)
							if v.ok {
								if prior, exists := env[n.Name]; exists {
									env[n.Name] = mergeValues(prior, v)
								} else {
									env[n.Name] = v
								}
							}
						}
					}
				}
			case *ast.RangeStmt:
				id, ok := node.Value.(*ast.Ident)
				literal, literalOK := node.X.(*ast.CompositeLit)
				if ok && literalOK {
					var alternatives []value
					for _, element := range literal.Elts {
						if v := x.eval(element, env, stack); v.ok {
							alternatives = append(alternatives, v)
						}
					}
					if len(alternatives) > 0 {
						env[id.Name] = mergeValues(alternatives...)
					}
				}
			case *ast.CallExpr:
				name := callName(node.Fun)
				if (name == "Set" || name == "Add") && len(node.Args) > 0 {
					if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
						if id, ok := dereferencedIdent(sel.X); ok {
							v := env[id.Name]
							if v.queries == nil {
								v = literalValue("")
							}
							if q, ok := stringLiteral(node.Args[0]); ok {
								v.queries[q] = true
							} else {
								v.unresolvedQuery = true
							}
							env[id.Name] = v
						}
					}
				}
				if name == "addKnownStringFilter" && len(node.Args) > 1 {
					if id, ok := node.Args[0].(*ast.Ident); ok {
						if q, ok := stringLiteral(node.Args[1]); ok {
							v := env[id.Name]
							if v.queries == nil {
								v = literalValue("")
							}
							v.queries[q] = true
							env[id.Name] = v
						}
					}
				}
			}
			return true
		})
	}
	return env
}

func (x *extractor) evalMethod(expr ast.Expr, env map[string]value) string {
	if s, ok := stringLiteral(expr); ok {
		return strings.ToUpper(s)
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return httpMethods[sel.Sel.Name]
	}
	if id, ok := expr.(*ast.Ident); ok {
		if v, yes := env[id.Name]; yes && len(v.shapes) == 1 {
			return strings.ToUpper(v.shapes[0])
		}
	}
	return ""
}

func (x *extractor) eval(expr ast.Expr, env map[string]value, stack map[string]bool) value {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind == token.STRING {
			s, err := strconv.Unquote(node.Value)
			if err == nil {
				return literalValue(s)
			}
		}
		return dynamicValue()
	case *ast.Ident:
		if v, ok := env[node.Name]; ok {
			return v
		}
		if s, ok := x.constants[node.Name]; ok {
			return literalValue(s)
		}
	case *ast.ParenExpr:
		return x.eval(node.X, env, stack)
	case *ast.StarExpr:
		return x.eval(node.X, env, stack)
	case *ast.BinaryExpr:
		if node.Op == token.ADD {
			a, b := x.eval(node.X, env, stack), x.eval(node.Y, env, stack)
			if !a.ok || !b.ok || a.canonicalBuilder || b.canonicalBuilder {
				return value{}
			}
			out := value{queries: map[string]bool{}, pathModes: map[string]bool{}, ok: true}
			for _, as := range a.shapes {
				for _, bs := range b.shapes {
					out.shapes = append(out.shapes, as+bs)
				}
			}
			for q := range a.queries {
				out.queries[q] = true
			}
			for q := range b.queries {
				out.queries[q] = true
			}
			for mode := range a.pathModes {
				out.pathModes[mode] = true
			}
			for mode := range b.pathModes {
				out.pathModes[mode] = true
			}
			out.unresolvedQuery = a.unresolvedQuery || b.unresolvedQuery
			return out
		}
	case *ast.CompositeLit:
		if x.isURLValuesExpr(node) {
			v := literalValue("")
			for _, elt := range node.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if q, literal := stringLiteral(kv.Key); literal {
						v.queries[q] = true
					} else {
						v.unresolvedQuery = true
					}
				}
			}
			return v
		}
	case *ast.CallExpr:
		name := callName(node.Fun)
		called := calledFunctionObject(x.typesInfo, node.Fun)
		if reviewedEndpointBuilders[name] && !x.exactEndpointBuilder(called) {
			return value{}
		}
		switch name {
		case "ReplaceAll":
			if len(node.Args) > 0 {
				input := x.eval(node.Args[0], env, stack)
				if input.canonicalBuilder {
					return value{}
				}
				return input
			}
			return value{}
		case "PathEscape", "QueryEscape", "Encode", "Sprintf":
			if name == "Sprintf" {
				return x.evalSprintf(node, env, stack)
			}
			if name == "Encode" {
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
					return x.eval(sel.X, env, stack)
				}
			}
			return dynamicValue()
		case "ValueString", "String", "Itoa", "FormatInt":
			return dynamicValue()
		case "endpointWithPathSegment", "endpointWithPathCapture", "endpointWithFallbackPathSegment":
			if len(node.Args) != 3 {
				return value{}
			}
			prefix, prefixLiteral := stringLiteral(node.Args[0])
			suffix, suffixLiteral := stringLiteral(node.Args[2])
			if !prefixLiteral || !suffixLiteral || !strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix+suffix, "?#{}") {
				return value{}
			}
			out := mergeValues(
				literalValue(prefix+"{}"+suffix),
				literalValue(prefix+"%2E"+suffix),
				literalValue(prefix+"%2E%2E"+suffix),
			)
			if name == "endpointWithPathCapture" {
				out.pathModes["capture"] = true
			} else {
				out.pathModes["ordinary"] = true
			}
			out.canonicalBuilder = true
			return out
		case "endpointWithQuery":
			if len(node.Args) != 2 {
				return value{}
			}
			p, q := x.eval(node.Args[0], env, stack), x.eval(node.Args[1], env, stack)
			if !p.ok || !q.ok || p.unresolvedQuery || len(p.queries) != 0 {
				return value{}
			}
			for _, shape := range p.shapes {
				pathShape, query, unresolved := splitShape(shape)
				if !strings.HasPrefix(pathShape, "/") || len(query) != 0 || unresolved || strings.Contains(pathShape, "#") {
					return value{}
				}
			}
			for key := range q.queries {
				p.queries[key] = true
			}
			p.unresolvedQuery = p.unresolvedQuery || q.unresolvedQuery
			p.canonicalBuilder = true
			return p
		}
		function, _ := called.(*types.Func)
		if fn := x.funcDeclForObject(function); fn != nil && function.Type().(*types.Signature).Recv() == nil && approvedURLHelper(function.Name()) && !stack[function.Name()] {
			bindings := map[string]value{}
			idx := 0
			if fn.Type.Params != nil {
				for _, f := range fn.Type.Params.List {
					for _, n := range f.Names {
						if idx < len(node.Args) {
							bindings[n.Name] = x.eval(node.Args[idx], env, stack)
						}
						idx++
					}
				}
			}
			next := copySet(stack)
			next[function.Name()] = true
			local := x.functionEnv(fn, bindings, next)
			var returns []value
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				r, ok := n.(*ast.ReturnStmt)
				if ok && len(r.Results) > 0 {
					v := x.eval(r.Results[0], local, next)
					if v.ok {
						returns = append(returns, v)
					}
				}
				return true
			})
			if len(returns) > 0 {
				return mergeValues(returns...)
			}
		}
		// Parameter/state accessors are dynamic values, not executable path builders.
		if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
			if _, ok := sel.X.(*ast.SelectorExpr); ok {
				return dynamicValue()
			}
		}
	}
	return value{}
}

func (x *extractor) evalSprintf(call *ast.CallExpr, env map[string]value, stack map[string]bool) value {
	if len(call.Args) == 0 {
		return value{}
	}
	format, ok := stringLiteral(call.Args[0])
	if !ok {
		return value{}
	}
	// Endpoint formats only need string/integer substitutions; preserve each as one segment value.
	result := format
	for i := 1; i < len(call.Args); i++ {
		v := x.eval(call.Args[i], env, stack)
		if !v.ok || v.canonicalBuilder {
			return value{}
		}
		replacement := "{}"
		if len(v.shapes) == 1 && v.shapes[0] != "" {
			replacement = v.shapes[0]
		}
		start := strings.Index(result, "%")
		if start < 0 {
			break
		}
		end := start + 1
		for end < len(result) && strings.ContainsRune("+#- 0123456789.", rune(result[end])) {
			end++
		}
		if end < len(result) {
			end++
		}
		result = result[:start] + replacement + result[end:]
	}
	return literalValue(strings.ReplaceAll(result, "%%", "%"))
}

func (x *extractor) detectRawHTTP() error {
	var bad []string
	for path, file := range x.files {
		base := filepath.Base(path)
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			rawAliases := x.rawHTTPAliases(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, rawHTTP := x.rawHTTPCallName(call)
				if !rawHTTP {
					name = rawAliases[callName(call.Fun)]
					rawHTTP = name != ""
				}
				if !rawHTTP {
					return true
				}
				approved := x.functionHasProviderReceiver(fn, "Client") && ((fn.Name.Name == "prepareRequest" && name == "NewRequestWithContext") || (fn.Name.Name == "executeRequestWithOptions" && name == "Do"))
				if !approved {
					pos := x.fset.Position(call.Pos())
					bad = append(bad, fmt.Sprintf("%s:%d: raw HTTP request construction is not approved", base, pos.Line))
				}
				return true
			})
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return errors.New(strings.Join(bad, "\n"))
	}
	return nil
}

func combineOperations(in []Operation) []Operation {
	by := map[string]*Operation{}
	for _, op := range in {
		key := op.Method + " " + op.Path
		existing := by[key]
		if existing == nil {
			copy := op
			by[key] = &copy
			continue
		}
		existing.QueryParameters = union(existing.QueryParameters, op.QueryParameters)
		if existing.pathMode == "" {
			existing.pathMode = op.pathMode
		} else if op.pathMode != "" && existing.pathMode != op.pathMode {
			existing.pathMode = "mixed"
		}
		existing.Evidence = append(existing.Evidence, op.Evidence...)
	}
	out := make([]Operation, 0, len(by))
	for _, op := range by {
		seenEvidence := map[string]bool{}
		uniqueEvidence := make([]Evidence, 0, len(op.Evidence))
		for _, evidence := range op.Evidence {
			key := evidence.File + ":" + strconv.Itoa(evidence.Line)
			if !seenEvidence[key] {
				seenEvidence[key] = true
				uniqueEvidence = append(uniqueEvidence, evidence)
			}
		}
		op.Evidence = uniqueEvidence
		sort.Slice(op.Evidence, func(i, j int) bool {
			if op.Evidence[i].File == op.Evidence[j].File {
				return op.Evidence[i].Line < op.Evidence[j].Line
			}
			return op.Evidence[i].File < op.Evidence[j].File
		})
		out = append(out, *op)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func splitShape(shape string) (string, map[string]bool, bool) {
	query := map[string]bool{}
	unresolved := false
	parts := strings.SplitN(shape, "?", 2)
	if len(parts) == 2 {
		for _, piece := range strings.Split(parts[1], "&") {
			name := strings.SplitN(piece, "=", 2)[0]
			if decoded, err := url.QueryUnescape(name); err == nil && decoded != "" {
				if strings.Contains(decoded, "{}") {
					unresolved = true
				} else {
					query[decoded] = true
				}
			}
		}
	}
	return parts[0], query, unresolved
}

func approvedURLHelper(name string) bool {
	return strings.HasSuffix(name, "Endpoint") || strings.HasSuffix(name, "Path") || strings.HasSuffix(name, "Filters")
}

func selectorRootName(expr ast.Expr) string {
	for {
		switch node := expr.(type) {
		case *ast.Ident:
			return node.Name
		case *ast.SelectorExpr:
			expr = node.X
		default:
			return ""
		}
	}
}

func callName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		return n.Sel.Name
	}
	return ""
}
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v := constant.MakeFromLiteral(lit.Value, token.STRING, 0)
	if v.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(v), true
}
func (x *extractor) isURLValuesExpr(expr ast.Expr) bool {
	t := types.Unalias(x.typesInfo.TypeOf(expr))
	for {
		pointer, ok := t.(*types.Pointer)
		if !ok {
			break
		}
		t = types.Unalias(pointer.Elem())
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Name() == "Values" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "net/url"
}
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func union(a, b []string) []string {
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	for _, s := range b {
		m[s] = true
	}
	return sortedKeys(m)
}
func copySet(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func LoadContracts(openapiPath, supplementalPath string) (map[string][]contractOperation, int, int, error) {
	data, err := os.ReadFile(openapiPath)
	if err != nil {
		return nil, 0, 0, err
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(data, &doc); err != nil {
		return nil, 0, 0, err
	}
	contracts := map[string][]contractOperation{}
	seen := map[string]contractOperation{}
	addContract := func(operation contractOperation) error {
		key := operation.method + " " + operation.path
		if prior, exists := seen[key]; exists {
			if strings.Join(prior.pathParams, ",") != strings.Join(operation.pathParams, ",") || strings.Join(sortedKeys(prior.queryParams), ",") != strings.Join(sortedKeys(operation.queryParams), ",") {
				return fmt.Errorf("conflicting duplicate API contract for %s", key)
			}
			return nil
		}
		seen[key] = operation
		contracts[operation.method] = append(contracts[operation.method], operation)
		return nil
	}
	type apiParameter struct {
		Name string `json:"name"`
		In   string `json:"in"`
	}
	count := 0
	for path, item := range doc.Paths {
		var inherited []apiParameter
		if raw, exists := item["parameters"]; exists {
			if err := json.Unmarshal(raw, &inherited); err != nil {
				return nil, 0, 0, fmt.Errorf("decode OpenAPI path parameters for %s: %w", path, err)
			}
		}
		for method, raw := range item {
			upper := strings.ToUpper(method)
			if _, ok := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true, "TRACE": true}[upper]; !ok {
				continue
			}
			var op struct {
				Parameters []apiParameter `json:"parameters"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, 0, 0, fmt.Errorf("decode OpenAPI operation %s %s: %w", upper, path, err)
			}
			c := contractOperation{method: upper, path: path, queryParams: map[string]bool{}}
			for _, p := range append(inherited, op.Parameters...) {
				if p.In == "path" {
					c.pathParams = append(c.pathParams, p.Name)
				} else if p.In == "query" {
					c.queryParams[p.Name] = true
				}
			}
			sort.Strings(c.pathParams)
			if err := addContract(c); err != nil {
				return nil, 0, 0, err
			}
			count++
		}
	}
	suppData, err := os.ReadFile(supplementalPath)
	if err != nil {
		return nil, 0, 0, err
	}
	var artifact Artifact
	if err = json.Unmarshal(suppData, &artifact); err != nil {
		return nil, 0, 0, err
	}
	for _, op := range artifact.Routes {
		operation := contractOperation{method: op.Method, path: op.Path, pathParams: op.PathParameters, queryParams: sliceSet(op.QueryParameters)}
		if err := addContract(operation); err != nil {
			return nil, 0, 0, err
		}
	}
	return contracts, len(doc.Paths), count, nil
}

func ResolveOperations(extracted []Operation, contracts map[string][]contractOperation) ([]Operation, error) {
	var out []Operation
	var problems []string
	captureRoutes := map[string]bool{
		"GET /credentials/by_name/{credential_name}": true,
		"DELETE /credentials/{credential_name}":      true,
		"PATCH /credentials/{credential_name}":       true,
	}
	for _, op := range extracted {
		matches := []contractOperation{}
		for _, candidate := range contracts[op.Method] {
			if shapeMatches(op.Path, candidate.path) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) != 1 {
			location := "unknown location"
			if len(op.Evidence) > 0 {
				location = fmt.Sprintf("%s:%d", op.Evidence[0].File, op.Evidence[0].Line)
			}
			problems = append(problems, fmt.Sprintf("%s %s at %s: expected one API contract match, found %d", op.Method, op.Path, location, len(matches)))
			continue
		}
		match := matches[0]
		for _, q := range op.QueryParameters {
			if !match.queryParams[q] {
				problems = append(problems, fmt.Sprintf("%s %s: query parameter %q is absent from API contract", op.Method, match.path, q))
			}
		}
		op.Path = match.path
		op.PathParameters = append([]string{}, match.pathParams...)
		expectedMode := ""
		if len(match.pathParams) != 0 {
			expectedMode = "ordinary"
			if captureRoutes[op.Method+" "+match.path] {
				expectedMode = "capture"
			}
		}
		if op.pathMode != expectedMode {
			problems = append(problems, fmt.Sprintf("%s %s: path builder mode %q does not match reviewed mode %q", op.Method, match.path, op.pathMode, expectedMode))
		}
		out = append(out, op)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return combineOperations(out), nil
}

func shapeMatches(shape, contract string) bool {
	a, b := strings.Split(strings.Trim(shape, "/"), "/"), strings.Split(strings.Trim(contract, "/"), "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		dynamic := a[i] == "{}" || strings.Contains(a[i], "{}") || a[i] == "%2E" || a[i] == "%2E%2E"
		templ := strings.HasPrefix(b[i], "{") && strings.HasSuffix(b[i], "}")
		if dynamic != templ {
			if dynamic && templ {
				continue
			}
			return false
		}
		if !dynamic && a[i] != b[i] {
			return false
		}
	}
	return true
}

func Verify(repoRoot string) error {
	pinsPath := filepath.Join(repoRoot, "internal/contract/reviewed-pins.json")
	var pins ReviewedPins
	if err := readJSONFile(pinsPath, &pins); err != nil {
		return err
	}
	if pins.SchemaVersion != 1 || pins.Upstream.Repository != pinnedRepository || pins.Upstream.Tag != pinnedTag || pins.Upstream.Commit != pinnedCommit || pins.Upstream.Python != pinnedPython || pins.Upstream.UV != pinnedUV || pins.Upstream.UVLockSHA256 != pinnedUVLockSHA256 {
		return errors.New("reviewed pin provenance differs from the compiled contract verifier")
	}
	pinList := []ArtifactPin{pins.Artifacts.OpenAPI, pins.Artifacts.Supplemental, pins.Artifacts.Manifest, pins.Artifacts.ProviderGolden, pins.Artifacts.Classification}
	for _, pin := range pinList {
		if !safeManifestPath(pin.Path) || len(pin.SHA256) != sha256.Size*2 {
			return fmt.Errorf("reviewed artifact pin is invalid for %q", pin.Path)
		}
		if err := verifyChecksum(filepath.Join(repoRoot, pin.Path), pin.SHA256); err != nil {
			return err
		}
	}

	manifestPath := filepath.Join(repoRoot, pins.Artifacts.Manifest.Path)
	var manifest Manifest
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return err
	}
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema version %d", manifest.SchemaVersion)
	}
	if manifest.Upstream.Repository != pinnedRepository || manifest.Upstream.Tag != pinnedTag || manifest.Upstream.Commit != pinnedCommit || manifest.Upstream.Python != pinnedPython || manifest.Upstream.UV != pinnedUV || manifest.Upstream.UVLockSHA256 != pinnedUVLockSHA256 {
		return errors.New("manifest upstream provenance differs from the reviewed pin")
	}
	if strings.Contains(manifest.GenerationCommand, "/tmp/") || strings.Contains(manifest.GenerationCommand, "/Users/") {
		return errors.New("manifest generation command contains a host-specific path")
	}
	if manifest.OpenAPI.Path != pins.Artifacts.OpenAPI.Path || manifest.Supplemental.Path != pins.Artifacts.Supplemental.Path || manifest.ProviderGolden.Path != pins.Artifacts.ProviderGolden.Path || manifest.Classification.Path != pins.Artifacts.Classification.Path {
		return errors.New("generated manifest artifact paths differ from reviewed pins")
	}
	if manifest.OpenAPI.SHA256 != pins.Artifacts.OpenAPI.SHA256 || manifest.Supplemental.SHA256 != pins.Artifacts.Supplemental.SHA256 || manifest.ProviderGolden.SHA256 != pins.Artifacts.ProviderGolden.SHA256 || manifest.Classification.SHA256 != pins.Artifacts.Classification.SHA256 {
		return errors.New("generated manifest checksums differ from reviewed pins")
	}

	openapiPath := filepath.Join(repoRoot, pins.Artifacts.OpenAPI.Path)
	suppPath := filepath.Join(repoRoot, pins.Artifacts.Supplemental.Path)
	var versionDocument struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := readJSONFile(openapiPath, &versionDocument); err != nil || versionDocument.Info.Version != "1.98.0" {
		return errors.New("OpenAPI info.version differs from the reviewed 1.98.0 pin")
	}
	contracts, pathCount, operationCount, err := LoadContracts(openapiPath, suppPath)
	if err != nil {
		return err
	}
	if pathCount != pins.Artifacts.OpenAPI.PathCount || operationCount != pins.Artifacts.OpenAPI.OperationCount || pathCount != manifest.OpenAPI.PathCount || operationCount != manifest.OpenAPI.OperationCount {
		return fmt.Errorf("OpenAPI counts differ from reviewed pins: paths=%d operations=%d", pathCount, operationCount)
	}
	var supplemental Artifact
	if err := readJSONFile(suppPath, &supplemental); err != nil {
		return err
	}
	if supplemental.SchemaVersion != 2 || supplemental.UpstreamCommit != pinnedCommit || len(supplemental.Routes) != pins.Artifacts.Supplemental.OperationCount || len(supplemental.Routes) != manifest.Supplemental.RouteCount {
		return errors.New("supplemental artifact metadata differs from reviewed pins")
	}
	if err := validateLazyFeatureEvidence(supplemental.LazyFeatures, manifest.RequiredLazyFeatures, pins.LazyFeatures); err != nil {
		return err
	}

	extracted, err := ExtractProvider(filepath.Join(repoRoot, "internal/provider"))
	if err != nil {
		return err
	}
	resolved, err := ResolveOperations(extracted, contracts)
	if err != nil {
		return err
	}
	var golden []Operation
	if err := readJSONFile(filepath.Join(repoRoot, pins.Artifacts.ProviderGolden.Path), &golden); err != nil {
		return err
	}
	if len(golden) != pins.Artifacts.ProviderGolden.OperationCount || len(golden) != manifest.ProviderGolden.OperationCount {
		return errors.New("provider-operation golden count differs from reviewed pins")
	}
	if diff := compareInventory(resolved, golden); diff != "" {
		return fmt.Errorf("provider-operation golden mismatch:\n%s", diff)
	}
	if diff := compareInventory(golden, manifest.Operations); diff != "" {
		return fmt.Errorf("generated manifest provider inventory mismatch:\n%s", diff)
	}

	var classification ReviewedClassification
	if err := readJSONFile(filepath.Join(repoRoot, pins.Artifacts.Classification.Path), &classification); err != nil {
		return err
	}
	if classification.SchemaVersion != 1 || len(classification.Operations) != pins.Artifacts.Classification.OperationCount || len(classification.Operations) != manifest.Classification.OperationCount {
		return errors.New("reviewed operation classification count differs from pins")
	}
	if manifest.Classification.UnsupportedDurableCount != pins.Artifacts.Classification.UnsupportedDurableCount || manifest.Classification.ExcludedNonDurableCount != pins.Artifacts.Classification.ExcludedNonDurableCount {
		return errors.New("generated manifest classification counts differ from reviewed pins")
	}
	if err := validateReview(manifest, pins, classification, contracts, resolved); err != nil {
		return err
	}
	return nil
}

func compareInventory(actual, want []Operation) string {
	var problems []string
	normalize := func(label string, ops []Operation) map[string]Operation {
		m := map[string]Operation{}
		for _, op := range ops {
			key := op.Method + " " + op.Path
			if _, exists := m[key]; exists {
				problems = append(problems, "duplicate "+label+" provider operation: "+key)
			}
			m[key] = op
		}
		return m
	}
	a, w := normalize("extracted", actual), normalize("manifest", want)
	for k, av := range a {
		wv, ok := w[k]
		if !ok {
			problems = append(problems, "provider operation missing from manifest: "+k)
			continue
		}
		if strings.Join(av.PathParameters, ",") != strings.Join(wv.PathParameters, ",") || strings.Join(av.QueryParameters, ",") != strings.Join(wv.QueryParameters, ",") {
			problems = append(problems, "manifest parameters stale for "+k)
			continue
		}
		actualEvidence, _ := json.Marshal(av.Evidence)
		wantedEvidence, _ := json.Marshal(wv.Evidence)
		if string(actualEvidence) != string(wantedEvidence) {
			problems = append(problems, "manifest evidence stale for "+k)
		}
	}
	for k := range w {
		if _, ok := a[k]; !ok {
			problems = append(problems, "stale manifest provider operation: "+k)
		}
	}
	sort.Strings(problems)
	return strings.Join(problems, "\n")
}

func safeManifestPath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func validateLazyFeatureEvidence(supplemental, manifest []LazyFeatureEvidence, reviewed []LazyFeatureContract) error {
	if len(reviewed) != 33 || len(supplemental) != len(reviewed) || !reflect.DeepEqual(supplemental, manifest) {
		return errors.New("complete lazy feature evidence differs between reviewed and generated artifacts")
	}
	encoded, err := json.Marshal(reviewed)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != pinnedLazyFeatureSHA256 {
		return errors.New("reviewed lazy feature mounting contract differs from the compiled pin")
	}
	seenNames, seenModules := map[string]bool{}, map[string]bool{}
	for index, evidence := range supplemental {
		if !reflect.DeepEqual(evidence.LazyFeatureContract, reviewed[index]) {
			return fmt.Errorf("generated lazy feature contract differs for index %d", index)
		}
		if seenNames[evidence.Name] || seenModules[evidence.Module] {
			return fmt.Errorf("duplicate lazy feature definition %q", evidence.Name)
		}
		seenNames[evidence.Name], seenModules[evidence.Module] = true, true
		if evidence.LiveOperationCount < 1 {
			return fmt.Errorf("lazy feature %q has zero live routes", evidence.Name)
		}
		if evidence.OpenAPIOperationCount < 1 && evidence.Name != "mcp_byok_oauth" {
			return fmt.Errorf("lazy feature %q has zero generated OpenAPI routes", evidence.Name)
		}
		if evidence.Name == "mcp_byok_oauth" && evidence.OpenAPIOperationCount != 0 {
			return errors.New("reviewed hidden-only mcp_byok_oauth exception changed")
		}
	}
	return nil
}

func validateReview(manifest Manifest, pins ReviewedPins, classification ReviewedClassification, contracts map[string][]contractOperation, supported []Operation) error {
	for _, operation := range manifest.Operations {
		if len(operation.Evidence) == 0 {
			return fmt.Errorf("provider operation %s %s lacks code evidence", operation.Method, operation.Path)
		}
		for _, evidence := range operation.Evidence {
			if !strings.HasPrefix(evidence.File, "internal/provider/") || evidence.Line < 1 {
				return fmt.Errorf("provider operation %s %s has invalid code evidence", operation.Method, operation.Path)
			}
		}
	}

	categories := map[string]CategoryDefinition{}
	for _, category := range pins.Categories {
		if category.ID == "" || category.Rationale == "" {
			return errors.New("reviewed classification category lacks an ID or rationale")
		}
		if _, exists := categories[category.ID]; exists {
			return fmt.Errorf("reviewed classification category %q is duplicated", category.ID)
		}
		if category.Disposition != "unsupported_durable" && category.Disposition != "excluded_non_durable" {
			return fmt.Errorf("reviewed classification category %q has invalid disposition", category.ID)
		}
		if category.Disposition == "unsupported_durable" && category.Issue == "" {
			return fmt.Errorf("durable classification category %q lacks an issue", category.ID)
		}
		if category.Disposition == "excluded_non_durable" && category.Issue != "" {
			return fmt.Errorf("non-durable classification category %q must not claim a resource issue", category.ID)
		}
		categories[category.ID] = category
	}
	requiredDurable := map[string]string{
		"credential_inventory": "#248", "vector_store_management": "#249",
		"pass_through_configuration": "#251", "policy_version_attachment_management": "#252",
		"mcp_toolset_management": "#207", "customer_end_user_management": "#207", "scim_directory_management": "#207",
		"global_proxy_configuration": "#207",
	}
	for id, issue := range requiredDurable {
		category, ok := categories[id]
		if !ok || category.Disposition != "unsupported_durable" || category.Issue != issue {
			return fmt.Errorf("required durable category %q must be reviewed against %s", id, issue)
		}
	}
	for _, id := range []string{"operational_action", "health", "spend_analytics", "cache_flush", "suggestion_discovery", "inference_workload"} {
		category, ok := categories[id]
		if !ok || category.Disposition != "excluded_non_durable" {
			return fmt.Errorf("required explicit non-durable category %q is absent", id)
		}
	}

	contractSet := map[string]bool{}
	for method, operations := range contracts {
		for _, operation := range operations {
			contractSet[method+" "+operation.path] = true
		}
	}
	supportedSet := map[string]bool{}
	for _, operation := range supported {
		supportedSet[operation.Method+" "+operation.Path] = true
	}
	classifiedSet := map[string]bool{}
	durableCount, excludedCount := 0, 0
	for _, operation := range classification.Operations {
		key := operation.Method + " " + operation.Path
		if operation.Method != strings.ToUpper(operation.Method) || !safeNormalizedRoute(operation.Path) {
			return fmt.Errorf("classification contains non-normalized operation %q", key)
		}
		if classifiedSet[key] {
			return fmt.Errorf("classification duplicates operation %s", key)
		}
		classifiedSet[key] = true
		if !contractSet[key] {
			return fmt.Errorf("classification contains stale operation %s", key)
		}
		if supportedSet[key] {
			return fmt.Errorf("supported provider operation %s is also classified unsupported", key)
		}
		category, ok := categories[operation.Category]
		if !ok {
			return fmt.Errorf("operation %s uses unknown classification category %q", key, operation.Category)
		}
		if category.Disposition == "unsupported_durable" {
			durableCount++
		} else {
			excludedCount++
		}
	}
	for key := range contractSet {
		if !supportedSet[key] && !classifiedSet[key] {
			return fmt.Errorf("unclassified API operation %s", key)
		}
	}
	if durableCount != pins.Artifacts.Classification.UnsupportedDurableCount || excludedCount != pins.Artifacts.Classification.ExcludedNonDurableCount {
		return fmt.Errorf("classification disposition counts changed: durable=%d excluded=%d", durableCount, excludedCount)
	}
	return nil
}

func safeNormalizedRoute(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.Contains(path, "?") && !strings.Contains(path, "//") && !strings.Contains(path, "{}")
}

func readJSONFile(path string, destination interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func verifyChecksum(path, want string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s", filepath.Base(path), got)
	}
	return nil
}
func sliceSet(in []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	return m
}
