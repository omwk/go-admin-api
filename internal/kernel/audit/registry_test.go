package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditEventsRegistry(t *testing.T) {
	// BE-IDN-009 §4.10: new static events and supported dynamic families require registration.
	root := filepath.Join("..", "..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "docs", "design", "audit-events.md"))
	require.NoError(t, err)
	registered := map[string]bool{}
	for _, match := range regexp.MustCompile("(?m)^\\| `([^`]+)` ").FindAllStringSubmatch(string(raw), -1) {
		require.True(t, actionPattern.MatchString(match[1]), match[1])
		require.False(t, registered[match[1]], "duplicate registration: %s", match[1])
		registered[match[1]] = true
	}
	require.NotEmpty(t, registered)
	constants := permissionActions(t, root)
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, action := range sourceAuditActions(file, path, constants) {
			require.True(t, registered[action], "unregistered audit action %s in %s; update docs/design/audit-events.md", action, path)
		}
		return nil
	})
	require.NoError(t, err)
	t.Logf("audit events registry: %d explicit actions; static literals, RBAC-derived actions and dynamic trade/sync families checked", len(registered))
}

func permissionActions(t *testing.T, root string) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "internal", "modules", "identity", "permission", "registry.go"), nil, 0)
	require.NoError(t, err)
	values := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		if spec, ok := node.(*ast.ValueSpec); ok && len(spec.Names) == 1 && len(spec.Values) == 1 {
			if literal, ok := spec.Values[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
				value, err := strconv.Unquote(literal.Value)
				require.NoError(t, err)
				values[spec.Names[0].Name] = value
			}
		}
		return true
	})
	return values
}

func sourceAuditActions(file *ast.File, path string, constants map[string]string) []string {
	actions := []string{}
	add := func(expr ast.Expr) {
		value := ""
		switch v := expr.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				value, _ = strconv.Unquote(v.Value)
			}
		case *ast.SelectorExpr:
			if x, ok := v.X.(*ast.Ident); ok && x.Name == "permission" {
				value = constants[v.Sel.Name]
			}
		}
		if strings.ContainsAny(value, ".:") && actionPattern.MatchString(Action(value)) {
			actions = append(actions, Action(value))
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CompositeLit:
			if typ, ok := n.Type.(*ast.SelectorExpr); ok && typ.Sel.Name == "Event" {
				if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "audit" {
					for _, element := range n.Elts {
						if pair, ok := element.(*ast.KeyValueExpr); ok {
							if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "Action" {
								add(pair.Value)
							}
						}
					}
				}
			}
		case *ast.CallExpr:
			if method, ok := n.Fun.(*ast.SelectorExpr); ok {
				switch method.Sel.Name {
				case "record", "audit", "schemaAudit", "registrationAudit", "AuditWallet", "AuditStatement":
					for _, arg := range n.Args {
						add(arg)
					}
				}
				if method.Sel.Name == "audit" && strings.Contains(filepath.ToSlash(path), "/modules/share/") && len(n.Args) == 5 {
					entity, eok := n.Args[2].(*ast.BasicLit)
					verb, vok := n.Args[4].(*ast.BasicLit)
					if eok && vok {
						e, _ := strconv.Unquote(entity.Value)
						v, _ := strconv.Unquote(verb.Value)
						actions = append(actions, Action("trade."+e+"."+v))
					}
				}
			}
		case *ast.BasicLit:
			// Dynamic dispatch variables still contain explicit action names at their source.
			if n.Kind == token.STRING {
				value, _ := strconv.Unquote(n.Value)
				if auditLiteral(value) {
					add(n)
				}
			}
		}
		return true
	})
	if strings.HasSuffix(path, "sync_drift_admin.go") {
		actions = append(actions, "sync.drift.ack", "sync.drift.resolve", "sync.drift.ignore")
	}
	return actions
}

func auditLiteral(value string) bool {
	for _, prefix := range []string{"billing.", "gateway.client.", "gateway.secret.", "gateway.resource.", "gateway.route.", "system.", "identity.password.", "notification.provider.", "notification.delivery.", "notification.template."} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
