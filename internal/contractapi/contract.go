package contractapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	pinnedRepository   = "https://github.com/BerriAI/litellm"
	pinnedTag          = "v1.98.0"
	pinnedCommit       = "d8f71d7bdbd7c9873d98293f83d64c6db72847e6"
	pinnedPython       = "3.12.14"
	pinnedUV           = "0.12.6"
	pinnedUVLockSHA256 = "a7cc57875c67de85bbae0f82b834f31fc9d0c029073ef29e0883787a31a985e8"
)

var httpMethods = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT", "MethodPatch": "PATCH",
	"MethodDelete": "DELETE", "MethodHead": "HEAD", "MethodOptions": "OPTIONS",
}

var clientRequestMethods = map[string]int{
	"DoRequest": 2, "DoRequestWithResponse": 2, "doRequestWithResponse": 2,
	"doRequestWithResponseOptions": 2, "doFreshRequestWithResponse": 2,
}

var helperRequestWrappers = map[string]int{
	"fetchTopLevelListObjects": 2, "fetchEnvelopeListObjects": 2, "readModelDataSourceWithRetry": 2,
	"readPromptDataSourceWithRetry": 2, "probeCredentialEndpoint": 2,
}

var approvedClientTransportInternals = map[string]bool{
	"prepareRequest": true, "executeRequest": true, "executeRequestWithOptions": true,
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
}

type Artifact struct {
	SchemaVersion  int                     `json:"schema_version"`
	UpstreamCommit string                  `json:"upstream_commit"`
	Routes         []SupplementalOperation `json:"routes"`
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
	RequiredLazyFeatures []string    `json:"required_lazy_features"`
	Operations           []Operation `json:"provider_operations"`
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
	Categories []CategoryDefinition `json:"categories"`
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
	shapes          []string
	queries         map[string]bool
	unresolvedQuery bool
	ok              bool
}

func literalValue(s string) value {
	return value{shapes: []string{s}, queries: map[string]bool{}, ok: true}
}
func dynamicValue() value { return literalValue("{}") }

func mergeValues(values ...value) value {
	out := value{queries: map[string]bool{}, ok: true}
	seen := map[string]bool{}
	for _, v := range values {
		if !v.ok {
			out.ok = false
			continue
		}
		for _, s := range v.shapes {
			if !seen[s] {
				seen[s] = true
				out.shapes = append(out.shapes, s)
			}
		}
		for q := range v.queries {
			out.queries[q] = true
		}
		out.unresolvedQuery = out.unresolvedQuery || v.unresolvedQuery
	}
	sort.Strings(out.shapes)
	return out
}

type extractor struct {
	root              string
	fset              *token.FileSet
	files             map[string]*ast.File
	funcs             map[string]*ast.FuncDecl
	constants         map[string]string
	clientMethods     map[string]*ast.FuncDecl
	clientMethodFiles map[string]string
}

func ExtractProvider(root string) ([]Operation, error) {
	x := &extractor{root: root, fset: token.NewFileSet(), files: map[string]*ast.File{}, funcs: map[string]*ast.FuncDecl{}, constants: map[string]string{}, clientMethods: map[string]*ast.FuncDecl{}, clientMethodFiles: map[string]string{}}
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
			case *ast.FuncDecl:
				x.funcs[node.Name.Name] = node
				if receiverTypeName(node) == "Client" {
					x.clientMethods[node.Name.Name] = node
					x.clientMethodFiles[node.Name.Name] = path
				}
			case *ast.GenDecl:
				if node.Tok == token.CONST {
					x.collectConstants(node)
				}
			}
		}
	}

	if err := x.validateClientTransport(); err != nil {
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
			if receiverTypeName(fn) == "Client" || fn.Name.Name == "fetchTopLevelListObjects" || fn.Name.Name == "fetchEnvelopeListObjects" || fn.Name.Name == "readModelDataSourceWithRetry" || fn.Name.Name == "readPromptDataSourceWithRetry" || fn.Name.Name == "probeCredentialEndpoint" {
				continue
			}
			env := x.functionEnv(fn, nil, map[string]bool{})
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
				pathIndex, isClientRequest := clientRequestMethods[name]
				if isClientRequest && !callHasClientReceiver(call, fn) {
					isClientRequest = false
				}
				if !isClientRequest {
					var isHelperRequest bool
					pathIndex, isHelperRequest = helperRequestWrappers[name]
					if !isHelperRequest {
						return true
					}
				}
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
				for _, shape := range pv.shapes {
					pathShape, query, unresolvedQuery := splitShape(shape)
					for q := range pv.queries {
						query[q] = true
					}
					if pathShape == "{}" || !strings.HasPrefix(pathShape, "/") || pv.unresolvedQuery || unresolvedQuery {
						problems = append(problems, fmt.Sprintf("%s:%d: unresolved dynamic HTTP path or query name", base, pos.Line))
						continue
					}
					extracted = append(extracted, Operation{Method: method, Path: pathShape, QueryParameters: sortedKeys(query), Evidence: []Evidence{{File: "internal/provider/" + base, Line: pos.Line}}})
				}
				return true
			})
		}
	}
	if err := x.detectRawHTTP(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return combineOperations(extracted), nil
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

func receiverTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	typeExpr := fn.Recv.List[0].Type
	if star, ok := typeExpr.(*ast.StarExpr); ok {
		typeExpr = star.X
	}
	if id, ok := typeExpr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

func callHasClientReceiver(call *ast.CallExpr, fn *ast.FuncDecl) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch receiver := selector.X.(type) {
	case *ast.Ident:
		if receiverTypeName(fn) == "Client" && receiver.Name == receiverName(fn) {
			return true
		}
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				fieldType := field.Type
				if star, ok := fieldType.(*ast.StarExpr); ok {
					fieldType = star.X
				}
				id, typedClient := fieldType.(*ast.Ident)
				if !typedClient || id.Name != "Client" {
					continue
				}
				for _, name := range field.Names {
					if name.Name == receiver.Name {
						return true
					}
				}
			}
		}
	case *ast.SelectorExpr:
		return receiver.Sel.Name == "client" || receiver.Sel.Name == "Client"
	}
	return false
}

func rawHTTPCallName(call *ast.CallExpr) (string, bool) {
	name := callName(call.Fun)
	rawHTTP := name == "NewRequest" || name == "NewRequestWithContext" || name == "Do" || name == "RoundTrip"
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		root := selectorRootName(selector.X)
		if root == "http" && (name == "Get" || name == "Post" || name == "PostForm" || name == "Head") {
			rawHTTP = true
		}
		if strings.Contains(strings.ToLower(root), "httpclient") && (name == "Get" || name == "Post" || name == "Head") {
			rawHTTP = true
		}
	}
	return name, rawHTTP
}

func (x *extractor) validateClientTransport() error {
	freeFunctions := map[string]*ast.FuncDecl{}
	for _, file := range x.files {
		for _, declaration := range file.Decls {
			if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Recv == nil {
				freeFunctions[fn.Name.Name] = fn
			}
		}
	}
	freeDirect := map[string]bool{}
	freeEdges := map[string]map[string]bool{}
	for name, fn := range freeFunctions {
		freeEdges[name] = map[string]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if _, raw := rawHTTPCallName(call); raw {
				freeDirect[name] = true
			}
			called := callName(call.Fun)
			if callHasClientReceiver(call, fn) {
				if _, ok := clientRequestMethods[called]; ok {
					freeDirect[name] = true
				}
				if approvedClientTransportInternals[called] {
					freeDirect[name] = true
				}
			}
			if _, ok := helperRequestWrappers[called]; ok {
				freeDirect[name] = true
			}
			if _, ok := call.Fun.(*ast.Ident); ok {
				if _, exists := freeFunctions[called]; exists {
					freeEdges[name][called] = true
				}
			}
			return true
		})
	}
	freeReaches := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for name := range freeFunctions {
			if freeReaches[name] {
				continue
			}
			if freeDirect[name] {
				freeReaches[name] = true
				changed = true
				continue
			}
			for called := range freeEdges[name] {
				if freeReaches[called] {
					freeReaches[name] = true
					changed = true
					break
				}
			}
		}
	}

	direct := map[string]bool{}
	edges := map[string]map[string]bool{}
	for name, fn := range x.clientMethods {
		receiver := receiverName(fn)
		edges[name] = map[string]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if _, raw := rawHTTPCallName(call); raw {
				direct[name] = true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && freeReaches[id.Name] {
				direct[name] = true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := selector.X.(*ast.Ident)
			if !ok || id.Name != receiver {
				return true
			}
			called := selector.Sel.Name
			if _, exists := x.clientMethods[called]; exists {
				edges[name][called] = true
			}
			if _, transport := clientRequestMethods[called]; transport {
				direct[name] = true
			}
			return true
		})
	}
	reachesTransport := map[string]bool{}
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
		_, requestApproved := clientRequestMethods[method]
		if !requestApproved && !approvedClientTransportInternals[method] {
			position := x.fset.Position(x.clientMethods[method].Pos())
			problems = append(problems, fmt.Sprintf("%s:%d: unknown Client HTTP abstraction %s", filepath.Base(x.clientMethodFiles[method]), position.Line, method))
		}
	}
	for path, file := range x.files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil || receiverTypeName(fn) == "Client" {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !callHasClientReceiver(call, fn) {
					return true
				}
				if approvedClientTransportInternals[callName(call.Fun)] {
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
						if id, ok := sel.X.(*ast.Ident); ok {
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
	case *ast.BinaryExpr:
		if node.Op == token.ADD {
			a, b := x.eval(node.X, env, stack), x.eval(node.Y, env, stack)
			if !a.ok || !b.ok {
				return value{}
			}
			out := value{queries: map[string]bool{}, ok: true}
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
			out.unresolvedQuery = a.unresolvedQuery || b.unresolvedQuery
			return out
		}
	case *ast.CompositeLit:
		if isURLValuesType(node.Type) {
			v := literalValue("")
			for _, elt := range node.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if q, ok := stringLiteral(kv.Key); ok {
						v.queries[q] = true
					}
				}
			}
			return v
		}
	case *ast.CallExpr:
		name := callName(node.Fun)
		switch name {
		case "ReplaceAll":
			if len(node.Args) > 0 {
				return x.eval(node.Args[0], env, stack)
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
		case "endpointWithQuery":
			if len(node.Args) != 2 {
				return value{}
			}
			p, q := x.eval(node.Args[0], env, stack), x.eval(node.Args[1], env, stack)
			if !p.ok || !q.ok {
				return value{}
			}
			for key := range q.queries {
				p.queries[key] = true
			}
			p.unresolvedQuery = p.unresolvedQuery || q.unresolvedQuery
			return p
		}
		if fn := x.funcs[name]; fn != nil && approvedURLHelper(name) && !stack[name] {
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
			next[name] = true
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
		if !v.ok {
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
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, rawHTTP := rawHTTPCallName(call)
				if !rawHTTP {
					return true
				}
				approved := receiverTypeName(fn) == "Client" && ((fn.Name.Name == "prepareRequest" && name == "NewRequestWithContext") || (fn.Name.Name == "executeRequestWithOptions" && name == "Do"))
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
		existing.Evidence = append(existing.Evidence, op.Evidence...)
	}
	out := make([]Operation, 0, len(by))
	for _, op := range by {
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
	return strings.HasSuffix(name, "Endpoint") || strings.HasSuffix(name, "Path") || strings.HasSuffix(name, "Filters") || name == "escapeCredentialPathValue"
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
func isURLValuesType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Values"
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
		dynamic := a[i] == "{}" || strings.Contains(a[i], "{}") || (strings.HasPrefix(shape, "/fallback/") && (a[i] == "%2E" || a[i] == "%2E%2E"))
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
	if supplemental.SchemaVersion != 1 || supplemental.UpstreamCommit != pinnedCommit || len(supplemental.Routes) != pins.Artifacts.Supplemental.OperationCount || len(supplemental.Routes) != manifest.Supplemental.RouteCount {
		return errors.New("supplemental artifact metadata differs from reviewed pins")
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

func validateReview(manifest Manifest, pins ReviewedPins, classification ReviewedClassification, contracts map[string][]contractOperation, supported []Operation) error {
	requiredLazy := []string{"access_groups", "agents", "guardrails", "mcp_management", "prompts", "search_tools"}
	if strings.Join(manifest.RequiredLazyFeatures, ",") != strings.Join(requiredLazy, ",") {
		return errors.New("required lazy feature review is stale")
	}
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
		"credential_inventory": "#248", "vector_store_management": "#249", "jwt_mapping_management": "#250",
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
