package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestActivityEnumsCoverLoggedValues keeps get_recent_activity's action and source filters honest.
//
// Both filters are closed enums, so a value logged but missing from one cannot be filtered on at
// all: "verify" and "delete-refused" were recorded for a long time while the schema rejected both,
// and so was the "lifecycle" source. No single list of logged values exists to check against,
// because each writer passes its own strings, so this reads the package source instead. It follows
// the source and action arguments of every PublishActivity call back to the string literals that
// reach them — through the parameters of wrapper functions, local variables, and the functions
// those variables are assigned from — and requires each enum to be exactly that set. An argument it
// cannot follow fails the test rather than being skipped, so a new kind of call site cannot slip
// past it.
func TestActivityEnumsCoverLoggedValues(t *testing.T) {
	props := getRecentActivityTool.Schema["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})

	// PublishActivity(source, action, tool, slug, title, agent)
	for _, tc := range []struct {
		param string
		arg   int
		enum  []string
	}{
		{"source", 0, activityLogSources},
		{"action", 1, activityLogActions},
	} {
		t.Run(tc.param, func(t *testing.T) {
			tracer := newLiteralTracer(t)
			tracer.followArgument("PublishActivity", tc.arg)
			if len(tracer.values) == 0 {
				t.Fatal("found no logged values at all; the source scan is broken")
			}
			var logged []string
			for value := range tracer.values {
				logged = append(logged, value)
			}
			slices.Sort(logged)
			enum := slices.Clone(tc.enum)
			slices.Sort(enum)
			if !slices.Equal(logged, enum) {
				t.Errorf("get_recent_activity's %s enum is %v, but the values logged are %v", tc.param, enum, logged)
			}

			// And the schema a client sees is that list.
			if got := props[tc.param].(map[string]interface{})["enum"].([]string); !slices.Equal(got, tc.enum) {
				t.Errorf("schema %s enum %v is not %v", tc.param, got, tc.enum)
			}
		})
	}
}

// literalTracer resolves the string literals that can reach an argument position, over the
// package's non-test source, collecting them in values.
type literalTracer struct {
	t       *testing.T
	fset    *token.FileSet
	funcs   map[string][]*ast.FuncDecl // by bare name; methods and functions alike
	values  map[string]bool
	visited map[string]bool
}

func newLiteralTracer(t *testing.T) *literalTracer {
	t.Helper()
	tr := &literalTracer{t: t, fset: token.NewFileSet(), funcs: map[string][]*ast.FuncDecl{},
		values: map[string]bool{}, visited: map[string]bool{}}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(tr.fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				tr.funcs[fn.Name.Name] = append(tr.funcs[fn.Name.Name], fn)
			}
		}
	}
	return tr
}

func (tr *literalTracer) fail(node ast.Node, why string) {
	tr.t.Errorf("%s: cannot follow an activity log argument (%s); extend TestActivityEnumsCoverLoggedValues",
		tr.fset.Position(node.Pos()), why)
}

// followArgument resolves argument arg of every call to a function named fn.
func (tr *literalTracer) followArgument(fn string, arg int) {
	key := fn + "/" + strconv.Itoa(arg)
	if tr.visited[key] {
		return
	}
	tr.visited[key] = true
	for _, decls := range tr.funcs {
		for _, caller := range decls {
			ast.Inspect(caller.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && calleeName(call) == fn && len(call.Args) > arg {
					tr.resolve(caller, call.Args[arg])
				}
				return true
			})
		}
	}
}

// resolve records the literals expr can hold inside fn.
func (tr *literalTracer) resolve(fn *ast.FuncDecl, expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		value, err := strconv.Unquote(e.Value)
		if e.Kind != token.STRING || err != nil {
			tr.fail(e, "not a string literal")
			return
		}
		tr.values[value] = true

	case *ast.Ident:
		// A parameter: the action comes from every caller.
		index := 0
		for _, field := range fn.Type.Params.List {
			if len(field.Names) == 0 {
				index++
			}
			for _, name := range field.Names {
				if name.Name == e.Name {
					tr.followArgument(fn.Name.Name, index)
					return
				}
				index++
			}
		}
		// A local variable: the action is whatever is assigned to it.
		assigned := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range assign.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == e.Name {
					assigned = true
					if len(assign.Rhs) != len(assign.Lhs) {
						tr.fail(assign, "multi-value assignment")
						continue
					}
					tr.resolveAssigned(fn, assign.Rhs[i])
				}
			}
			return true
		})
		if !assigned {
			tr.fail(e, e.Name+" is neither a parameter nor an assigned variable")
		}

	default:
		tr.fail(expr, "unsupported expression")
	}
}

// resolveAssigned follows a value assigned to an action variable: an expression directly, or every
// value the called function returns.
func (tr *literalTracer) resolveAssigned(fn *ast.FuncDecl, value ast.Expr) {
	call, ok := value.(*ast.CallExpr)
	if !ok {
		tr.resolve(fn, value)
		return
	}
	callees := tr.funcs[calleeName(call)]
	if len(callees) == 0 {
		tr.fail(call, "call to a function outside the package")
		return
	}
	for _, callee := range callees {
		ast.Inspect(callee.Body, func(n ast.Node) bool {
			if _, nested := n.(*ast.FuncLit); nested {
				return false
			}
			if ret, ok := n.(*ast.ReturnStmt); ok {
				if len(ret.Results) != 1 {
					tr.fail(ret, "not a single return value")
					return true
				}
				tr.resolve(callee, ret.Results[0])
			}
			return true
		})
	}
}

// calleeName is the bare name of the function or method a call invokes.
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// TestMCPToolActions pins how tool calls are recorded, and that every registered tool maps to an
// action the activity filter offers.
func TestMCPToolActions(t *testing.T) {
	for tool, want := range map[string]string{
		"create_wiki_article":    "create",
		"edit_agent_plan":        "edit",
		"append_agent_memory":    "edit",
		"update_article_tags":    "edit",
		"revert_article_version": "revert",
		"delete_agent_memory":    "delete",
		"search_wiki":            "read",
	} {
		if got := mcpToolAction(tool); got != want {
			t.Errorf("mcpToolAction(%q) = %q, want %q", tool, got, want)
		}
	}
	for tool := range toolsByName {
		if action := mcpToolAction(tool); !slices.Contains(activityLogActions, action) {
			t.Errorf("%s is logged as %q, which get_recent_activity cannot filter on", tool, action)
		}
	}
}

// TestMCPRevertIsLoggedAsRevert is the regression guard for an agent's revert being recorded as an
// edit, which made filtering the log on "revert" find only the reverts made in the web UI.
func TestMCPRevertIsLoggedAsRevert(t *testing.T) {
	srv := newMCPServer(t)
	if _, err := srv.Storage.SaveArticle("", "Revert Me", "# v1", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if _, err := srv.Storage.SaveArticle("revert-me", "Revert Me", "# v2", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	updates := srv.EventBus.SubscribeWikiUpdates()
	defer srv.EventBus.UnsubscribeWikiUpdates(updates)

	result, rpcErr := srv.executeToolCall(json.RawMessage(`{"name":"revert_article_version","arguments":{"slug":"revert-me","version":1}}`), "Test Agent")
	if rpcErr != nil || isToolError(result) {
		t.Fatalf("revert failed: %v %+v", rpcErr, result)
	}

	history := srv.EventBus.GetHistory()
	if len(history) != 1 {
		t.Fatalf("expected one activity event, got %+v", history)
	}
	ev := history[0]
	if ev.Source != "mcp" || ev.Action != "revert" || ev.Tool != "revert_article_version" || ev.Slug != "revert-me" || ev.Agent != "Test Agent" {
		t.Errorf("unexpected event %+v, want an mcp revert of revert-me by Test Agent", ev)
	}
	select {
	case u := <-updates:
		if u.Type != "article-edited" || u.Slug != "revert-me" {
			t.Errorf("unexpected wiki update %+v", u)
		}
	default:
		t.Error("a revert must still broadcast a wiki update")
	}

	// And the filter an agent uses finds it.
	found, _ := srv.toolGetRecentActivity(json.RawMessage(`{"action":"revert","source":"mcp"}`))
	if out, ok := found.(ToolResponse).StructuredContent.(ActivityOutput); !ok || out.Count != 1 {
		t.Errorf("get_recent_activity(action: revert, source: mcp) did not find the revert: %+v", found)
	}
}
