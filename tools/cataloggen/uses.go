package main

import (
	"go/ast"
	"go/types"
	"maps"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// roles are the services a Temporal server runs; code anywhere else is shared by all of them.
var roles = []string{"frontend", "history", "matching", "worker"}

// uses records on byVar every setting p references: the role of p, and whether p reads
// the value once (X.Get(c)()) or subscribes to changes (X.Subscribe(c)).
func uses(p *packages.Package, byVar map[*types.Var]*found) {
	if p.PkgPath == dynamicconfig || p.TypesInfo == nil {
		return
	}
	role := roleOf(p.PkgPath)
	for _, f := range p.Syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				if s := settingOf(p.TypesInfo, byVar, n); s != nil {
					s.roles[role] = true
				}
			case *ast.CallExpr:
				if s := getOnce(p.TypesInfo, byVar, n); s != nil {
					s.get = true
				}
				if s, m := method(p.TypesInfo, byVar, n.Fun); s != nil && m == "Subscribe" {
					s.watched = true
				}
			}
			return true
		})
	}
}

// roleOf is the role of a package: service/<role>/... is that role, anything else shared.
func roleOf(pkgPath string) string {
	rest, ok := strings.CutPrefix(
		pkgPath,
		"go.temporal.io/server/service/",
	)
	if !ok {
		return "shared"
	}
	role, _, _ := strings.Cut(rest, "/")
	if !slices.Contains(roles, role) {
		return "shared"
	}

	return role
}

// settingOf is the setting e refers to: a name, or the name after a package selector.
func settingOf(info *types.Info, byVar map[*types.Var]*found, e ast.Expr) *found {
	switch e := e.(type) {
	case *ast.Ident:
		v, _ := info.Uses[e].(*types.Var)
		return byVar[v]
	case *ast.SelectorExpr:
		return settingOf(info, byVar, e.Sel)
	}
	return nil
}

// method is the setting and method name of a call's function X.M.
func method(info *types.Info, byVar map[*types.Var]*found, fun ast.Expr) (*found, string) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return nil, ""
	}

	return settingOf(info, byVar, sel.X), sel.Sel.Name
}

// getOnce is the setting of a call X.Get(c)(): the value is read once, where it is called.
func getOnce(info *types.Info, byVar map[*types.Var]*found, call *ast.CallExpr) *found {
	inner, ok := call.Fun.(*ast.CallExpr)
	if !ok {
		return nil
	}
	if s, m := method(info, byVar, inner.Fun); m == "Get" {
		return s
	}
	return nil
}

// result is the finished setting: the roles that read it, and a static apply hint.
func (s *found) result() setting {
	out := s.setting
	if len(s.roles) == 0 || s.roles["shared"] {
		out.Roles = []string{"shared"}
	} else {
		out.Roles = slices.Sorted(maps.Keys(s.roles))
	}

	switch {
	case s.get || strings.Contains(strings.ToLower(s.Description), "restart"):
		out.Candidate = "restart"
	case s.watched:
		out.Candidate = "hot"
	default:
		out.Candidate = "unknown"
	}
	return out
}
