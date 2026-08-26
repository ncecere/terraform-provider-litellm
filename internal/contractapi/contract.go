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
	pinnedUVLockSHA256 = "a7cc57875c67de85bbae0f82b834f31fc9d0c029073ef29e0883787a31a985e8"
)

var httpMethods = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT", "MethodPatch": "PATCH",
	"MethodDelete": "DELETE", "MethodHead": "HEAD", "MethodOptions": "OPTIONS",
}

var requestWrappers = map[string]int{
	"DoRequest": 2, "DoRequestWithResponse": 2, "doRequestWithResponse": 2,
	"doRequestWithResponseOptions": 2, "doFreshRequestWithResponse": 2,
	"fetchTopLevelListObjects": 2, "fetchEnvelopeListObjects": 2, "readModelDataSourceWithRetry": 2, "readPromptDataSourceWithRetry": 2, "probeCredentialEndpoint": 2,
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
	RequiredLazyFeatures []string           `json:"required_lazy_features"`
	Operations           []Operation        `json:"provider_operations"`
	Unsupported          []UnsupportedGroup `json:"unsupported_durable_operations"`
	Excluded             []ExcludedCategory `json:"excluded_non_durable_categories"`
}

type UnsupportedGroup struct {
	Group      string   `json:"group"`
	Issue      string   `json:"issue"`
	Rationale  string   `json:"rationale"`
	Operations []string `json:"operations"`
}

type ExcludedCategory struct {
	Category  string `json:"category"`
	Rationale string `json:"rationale"`
}

type contractOperation struct {
	method      string
	path        string
	pathParams  []string
	queryParams map[string]bool
}

type value struct {
	shapes  []string
	queries map[string]bool
	ok      bool
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
	}
	sort.Strings(out.shapes)
	return out
}

type extractor struct {
	root      string
	fset      *token.FileSet
	files     map[string]*ast.File
	funcs     map[string]*ast.FuncDecl
	constants map[string]string
}

func ExtractProvider(root string) ([]Operation, error) {
	x := &extractor{root: root, fset: token.NewFileSet(), files: map[string]*ast.File{}, funcs: map[string]*ast.FuncDecl{}, constants: map[string]string{}}
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
			case *ast.GenDecl:
				if node.Tok == token.CONST {
					x.collectConstants(node)
				}
			}
		}
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
			if base == "client.go" || fn.Name.Name == "fetchTopLevelListObjects" || fn.Name.Name == "fetchEnvelopeListObjects" || fn.Name.Name == "readModelDataSourceWithRetry" || fn.Name.Name == "readPromptDataSourceWithRetry" || fn.Name.Name == "probeCredentialEndpoint" {
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
				pathIndex, isRequest := requestWrappers[name]
				if !isRequest {
					return true
				}
				if len(call.Args) <= pathIndex {
					problems = append(problems, fmt.Sprintf("%s:%d: malformed HTTP wrapper call", base, pos.Line))
					return true
				}
				method := "GET"
				if name != "fetchTopLevelListObjects" && name != "fetchEnvelopeListObjects" && name != "readModelDataSourceWithRetry" && name != "readPromptDataSourceWithRetry" && name != "probeCredentialEndpoint" {
					method = x.evalMethod(call.Args[pathIndex-1], env)
				}
				pv := x.eval(call.Args[pathIndex], env, map[string]bool{})
				if method == "" || !pv.ok || len(pv.shapes) == 0 {
					problems = append(problems, fmt.Sprintf("%s:%d: unresolved HTTP method or path", base, pos.Line))
					return true
				}
				for _, shape := range pv.shapes {
					pathShape, query := splitShape(shape)
					for q := range pv.queries {
						query[q] = true
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
							if q, ok := stringLiteral(node.Args[0]); ok {
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
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
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
			if rawHTTP && base != "client.go" {
				pos := x.fset.Position(call.Pos())
				bad = append(bad, fmt.Sprintf("%s:%d: raw HTTP request construction is not approved", base, pos.Line))
			}
			return true
		})
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

func splitShape(shape string) (string, map[string]bool) {
	query := map[string]bool{}
	parts := strings.SplitN(shape, "?", 2)
	if len(parts) == 2 {
		for _, piece := range strings.Split(parts[1], "&") {
			name := strings.SplitN(piece, "=", 2)[0]
			if decoded, err := url.QueryUnescape(name); err == nil && decoded != "" && decoded != "{}" {
				query[decoded] = true
			}
		}
	}
	return parts[0], query
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
	manifestPath := filepath.Join(repoRoot, "internal/contract/manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema version %d", manifest.SchemaVersion)
	}
	if manifest.Upstream.Repository != pinnedRepository || manifest.Upstream.Tag != pinnedTag || manifest.Upstream.Commit != pinnedCommit || manifest.Upstream.Python != pinnedPython || manifest.Upstream.UVLockSHA256 != pinnedUVLockSHA256 {
		return errors.New("manifest upstream provenance differs from the reviewed pin")
	}
	if strings.Contains(manifest.GenerationCommand, "/tmp/") || strings.Contains(manifest.GenerationCommand, "/Users/") {
		return errors.New("manifest generation command contains a host-specific path")
	}
	if !safeManifestPath(manifest.OpenAPI.Path) || !safeManifestPath(manifest.Supplemental.Path) {
		return errors.New("manifest artifact path is not repository-relative")
	}
	openapiPath := filepath.Join(repoRoot, manifest.OpenAPI.Path)
	suppPath := filepath.Join(repoRoot, manifest.Supplemental.Path)
	if err = verifyChecksum(openapiPath, manifest.OpenAPI.SHA256); err != nil {
		return err
	}
	if err = verifyChecksum(suppPath, manifest.Supplemental.SHA256); err != nil {
		return err
	}
	var versionDocument struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	openapiData, readErr := os.ReadFile(openapiPath)
	if readErr != nil {
		return readErr
	}
	if err := json.Unmarshal(openapiData, &versionDocument); err != nil || versionDocument.Info.Version != "1.98.0" {
		return errors.New("OpenAPI info.version differs from the reviewed 1.98.0 pin")
	}
	contracts, pathCount, operationCount, err := LoadContracts(openapiPath, suppPath)
	if err != nil {
		return err
	}
	if pathCount != manifest.OpenAPI.PathCount || operationCount != manifest.OpenAPI.OperationCount {
		return fmt.Errorf("OpenAPI counts changed: paths=%d operations=%d", pathCount, operationCount)
	}
	var artifact Artifact
	suppData, _ := os.ReadFile(suppPath)
	if err = json.Unmarshal(suppData, &artifact); err != nil {
		return err
	}
	if artifact.SchemaVersion != 1 || artifact.UpstreamCommit != pinnedCommit {
		return errors.New("supplemental artifact provenance differs from the reviewed pin")
	}
	if len(artifact.Routes) != manifest.Supplemental.RouteCount {
		return fmt.Errorf("supplemental route count changed: %d", len(artifact.Routes))
	}
	extracted, err := ExtractProvider(filepath.Join(repoRoot, "internal/provider"))
	if err != nil {
		return err
	}
	resolved, err := ResolveOperations(extracted, contracts)
	if err != nil {
		return err
	}
	if diff := compareInventory(resolved, manifest.Operations); diff != "" {
		return errors.New(diff)
	}
	return validateReview(manifest)
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

func validateReview(m Manifest) error {
	requiredLazy := []string{"access_groups", "agents", "guardrails", "mcp_management", "prompts", "search_tools"}
	if strings.Join(m.RequiredLazyFeatures, ",") != strings.Join(requiredLazy, ",") {
		return errors.New("required lazy feature review is stale")
	}
	for _, operation := range m.Operations {
		if len(operation.Evidence) == 0 {
			return fmt.Errorf("provider operation %s %s lacks code evidence", operation.Method, operation.Path)
		}
		for _, evidence := range operation.Evidence {
			if !strings.HasPrefix(evidence.File, "internal/provider/") || evidence.Line < 1 {
				return fmt.Errorf("provider operation %s %s has invalid code evidence", operation.Method, operation.Path)
			}
		}
	}
	requiredGroups := map[string]string{"credential inventories": "#248", "vector inventories": "#249", "JWT mappings": "#250", "pass-through configuration": "#251", "policy versions and attachments": "#252", "MCP toolsets": "#207", "customer and end-user records": "#207", "SCIM": "#207", "global configuration": "#207"}
	seenGroups := map[string]bool{}
	for _, g := range m.Unsupported {
		if seenGroups[g.Group] {
			return fmt.Errorf("unsupported durable group %q is duplicated", g.Group)
		}
		seenGroups[g.Group] = true
		if issue, ok := requiredGroups[g.Group]; ok {
			if g.Issue != issue {
				return fmt.Errorf("unsupported durable group %q must reference %s", g.Group, issue)
			}
		}
		if g.Issue == "" || g.Rationale == "" || len(g.Operations) == 0 {
			return fmt.Errorf("unsupported durable group %q lacks review metadata", g.Group)
		}
	}
	for group := range requiredGroups {
		if !seenGroups[group] {
			return fmt.Errorf("unsupported durable inventory omits %q", group)
		}
	}
	requiredExcluded := map[string]bool{"operational actions": false, "health": false, "spend and analytics": false, "cache flush": false, "suggestions": false, "inference": false}
	seenExcluded := map[string]bool{}
	for _, c := range m.Excluded {
		if seenExcluded[c.Category] {
			return fmt.Errorf("excluded category %q is duplicated", c.Category)
		}
		seenExcluded[c.Category] = true
		if _, ok := requiredExcluded[c.Category]; ok {
			requiredExcluded[c.Category] = true
		}
		if c.Rationale == "" {
			return fmt.Errorf("excluded category %q lacks rationale", c.Category)
		}
	}
	for c, ok := range requiredExcluded {
		if !ok {
			return fmt.Errorf("excluded non-durable inventory omits %q", c)
		}
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
