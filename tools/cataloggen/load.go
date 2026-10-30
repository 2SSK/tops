package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

const (
	dynamicconfig = "go.temporal.io/server/common/dynamicconfig"
	constantsFile = "common/dynamicconfig/constants.go"
	server        = "go.temporal.io/server/cmd/server"
)

// constructor is New<Precedence><Type>Setting, optionally WithConstrainedDefault.
var constructor = regexp.MustCompile(`^New(Global|NamespaceID|Namespace|TaskQueue|ShardID|TaskType|Destination|ChasmTaskType)(Bool|Int|Float|String|Duration|Map|Typed)Setting(WithConstrainedDefault)?$`)

// precedences maps the constructor's precedence to its name and the constraints a
// file may use with it: convertYamlConstraints in common/dynamicconfig/yaml_loader.go.
var precedences = map[string]struct {
	name        string
	constraints []string
}{
	"Global":        {"global", []string{}},
	"Namespace":     {"namespace", []string{"namespace"}},
	"NamespaceID":   {"namespaceId", []string{"namespaceId"}},
	"TaskQueue":     {"taskQueue", []string{"namespace", "taskQueueName", "taskType"}},
	"ShardID":       {"shardId", []string{"shardId"}},
	"TaskType":      {"taskType", []string{"historyTaskType"}},
	"Destination":   {"destination", []string{"namespace", "destination"}},
	"ChasmTaskType": {"chasmTaskType", []string{"chasmTaskType"}},
}

// load type-checks the checkout at src and returns its settings sorted by key, each
// with the roles of the server packages that reference it.
func load(src string) ([]setting, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:  src,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		return nil, fmt.Errorf("%d errors loading %s", n, src)
	}

	i := slices.IndexFunc(pkgs, func(p *packages.Package) bool {
		return p.PkgPath == dynamicconfig
	})
	j := slices.IndexFunc(pkgs, func(p *packages.Package) bool {
		return p.PkgPath == server
	})
	if i < 0 || j < 0 {
		return nil, fmt.Errorf("%s has no package %s or %s", src, dynamicconfig, server)
	}
	byVar, err := declared(pkgs[i])
	if err != nil {
		return nil, err
	}

	// Only what the server binary imports: test harnesses and tools reference settings
	// too, and would make every one look shared.
	packages.Visit(pkgs[j:j+1], nil, func(p *packages.Package) {
		uses(p, byVar)
	})

	settings := make([]setting, 0, len(byVar))
	for _, s := range byVar {
		settings = append(settings, s.result())
	}
	slices.SortFunc(settings, func(a, b setting) int {
		return strings.Compare(a.Key, b.Key)
	})

	return settings, duplicates(settings)
}

// found is a setting being collected, by the variable constants.go assigns it to.
type found struct {
	setting
	roles   map[string]bool // roleOf of every package that references it
	get     bool            // read once with X.Get(c)()
	watched bool            // X.Subscribe(c)
}

// declared reads every New...Setting call in constants.go, by the variable it sets.
func declared(p *packages.Package) (map[*types.Var]*found, error) {
	byVar := map[*types.Var]*found{}
	for _, f := range p.Syntax {
		if filepath.Base(p.Fset.File(f.Pos()).Name()) != filepath.Base(constantsFile) {
			continue
		}
		for _, d := range f.Decls {
			for _, spec := range valueSpecs(d) {
				for i, name := range spec.Names {
					if i >= len(spec.Values) {
						continue
					}
					call, ok := spec.Values[i].(*ast.CallExpr)
					if !ok {
						continue
					}
					s, ok, err := parse(p.TypesInfo, call)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", name.Name, err)
					}
					if ok {
						byVar[p.TypesInfo.Defs[name].(*types.Var)] = s
					}
				}
			}
		}
	}
	return byVar, nil
}

func valueSpecs(d ast.Decl) []*ast.ValueSpec {
	g, ok := d.(*ast.GenDecl)
	if !ok {
		return nil
	}
	var specs []*ast.ValueSpec
	for _, s := range g.Specs {
		if v, ok := s.(*ast.ValueSpec); ok {
			specs = append(specs, v)
		}
	}
	return specs
}

// parse reads one constructor call: key, default and description are its three
// arguments. ok is false for a call that is not a setting constructor.
func parse(info *types.Info, call *ast.CallExpr) (*found, bool, error) {
	m := constructor.FindStringSubmatch(funcName(call.Fun))
	if m == nil {
		return nil, false, nil
	}
	if len(call.Args) != 3 {
		return nil, false, fmt.Errorf("%s has %d arguments, want 3", m[0], len(call.Args))
	}
	key, err := stringValue(info, call.Args[0])
	if err != nil {
		return nil, false, fmt.Errorf("key: %w", err)
	}
	description, err := stringValue(info, call.Args[2])
	if err != nil {
		return nil, false, fmt.Errorf("%s description: %w", key, err)
	}

	p := precedences[m[1]]
	s := &found{
		setting: setting{
			Key:         key,
			Type:        strings.ToLower(m[2]),
			Precedence:  p.name,
			Constraints: p.constraints,
			DefaultExpr: types.ExprString(call.Args[1]),
			Description: description,
		},
		roles: map[string]bool{},
	}
	if m[3] != "" {
		s.Default = constrainedDefault(info, call.Args[1], s.Type)
	} else {
		s.Default = value(info, call.Args[1], s.Type)
	}
	return s, true, nil
}

// funcName is the name of a called function, without a package or type arguments.
func funcName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.IndexExpr:
		return funcName(e.X)
	case *ast.IndexListExpr:
		return funcName(e.X)
	}
	return ""
}

func stringValue(info *types.Info, e ast.Expr) (string, error) {
	v := info.Types[e].Value
	if v == nil || v.Kind() != constant.String {
		return "", fmt.Errorf("%s is not a constant string", types.ExprString(e))
	}
	return constant.StringVal(v), nil
}

// value is a default as JSON: a constant evaluated by the type checker (a duration
// as 1h, 5m), else nil, and defaultExpr keeps the source.
func value(info *types.Info, e ast.Expr, typ string) any {
	v := info.Types[e].Value
	if v == nil {
		return nil
	}

	switch v.Kind() {
	case constant.Bool:
		return constant.BoolVal(v)
	case constant.String:
		return constant.StringVal(v)
	case constant.Int:
		n, _ := constant.Int64Val(v)
		if typ == "duration" {
			return shortDuration(time.Duration(n))
		}
		return n
	case constant.Float:
		f, _ := constant.Float64Val(v)
		return f
	}
	return nil
}

// constrainedDefault is the Value of each element of a []TypedConstrainedValue
// literal, in order; nil for any other expression.
func constrainedDefault(info *types.Info, e ast.Expr, typ string) any {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	values := []any{}
	for _, elt := range lit.Elts {
		cv, ok := elt.(*ast.CompositeLit)
		if !ok {
			return nil
		}
		for _, f := range cv.Elts {
			if kv, ok := f.(*ast.KeyValueExpr); ok && types.ExprString(kv.Key) == "Value" {
				values = append(values, value(info, kv.Value, typ))
			}
		}
	}
	return values
}

// shortDuration drops the zero units time.Duration.String keeps: 1h0m0s is 1h.
func shortDuration(d time.Duration) string {
	s := d.String()

	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}

	return s
}

// duplicates fails for two settings whose keys differ only in case: Temporal
// looks keys up case-insensitively.
func duplicates(settings []setting) error {
	seen := map[string]string{}
	for _, s := range settings {
		k := strings.ToLower(s.Key)
		if prev, ok := seen[k]; ok {
			return fmt.Errorf("keys %s and %s differ only in case", prev, s.Key)
		}
		seen[k] = s.Key
	}
	return nil
}
