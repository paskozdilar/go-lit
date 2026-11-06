package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func tangle(t *testing.T, md string, lineMap bool) (map[string][]byte, error) {
	t.Helper()
	p := NewProgram()
	if err := p.Parse("doc.md", []byte(md)); err != nil {
		t.Fatal(err)
	}
	return p.Tangle(".", lineMap)
}

func mustTangle(t *testing.T, md, path string, lineMap bool) string {
	t.Helper()
	files, err := tangle(t, md, lineMap)
	if err != nil {
		t.Fatal(err)
	}
	return string(files[path])
}

const fence = "```"

func TestMacroExpansionAndImports(t *testing.T) {
	md := fence + "go\n// file: main.go\npackage main\n\nfunc main() {\n\tif true {\n\t\t//:: body :://\n\t}\n}\n" + fence + "\n\n" +
		fence + "go\n//== body ==//\nfmt.Println(strings.ToUpper(\"x\"))\n" + fence + "\n"
	got := mustTangle(t, md, "main.go", false)
	want := header + `
package main

import (
	"fmt"
	"strings"
)

func main() {
	if true {
		fmt.Println(strings.ToUpper("x"))
	}
}
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestIgnoresNonGoAndUnmarkedBlocks(t *testing.T) {
	md := "```\n// file: nope.go\npackage nope\n```\n\n```python\n// file: nope2.go\n```\n\n" +
		fence + "go\n// file: a.go\npackage a\n" + fence + "\n"
	files, err := tangle(t, md, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files["a.go"] == nil {
		t.Fatalf("unexpected files: %v", files)
	}
}

func TestCycleDetected(t *testing.T) {
	md := fence + "go\n// file: a.go\npackage a\nfunc f() {\n//:: x :://\n}\n" + fence + "\n" +
		fence + "go\n//== x ==//\n//:: y :://\n" + fence + "\n" +
		fence + "go\n//== y ==//\n//:: x :://\n" + fence + "\n"
	_, err := tangle(t, md, false)
	if err == nil || !strings.Contains(err.Error(), "circular reference: file:a.go -> x -> y -> x") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestUndefinedChunk(t *testing.T) {
	md := fence + "go\n// file: a.go\npackage a\nfunc f() {\n//:: missing :://\n}\n" + fence + "\n"
	_, err := tangle(t, md, false)
	if err == nil || !strings.Contains(err.Error(), `doc.md:5: undefined chunk "missing"`) {
		t.Fatalf("got %v", err)
	}
}

func TestBuildTagsHoisted(t *testing.T) {
	md := fence + "go\n// file: a.go\npackage a\n" + fence + "\n" +
		fence + "go\n// file: a.go\n//go:build linux\n\nvar X = 1\n" + fence + "\n"
	got := mustTangle(t, md, "a.go", false)
	if !strings.HasPrefix(got, header+"\n//go:build linux\n\npackage a\n") {
		t.Fatalf("build tag not hoisted:\n%s", got)
	}
}

func TestLineMap(t *testing.T) {
	md := "intro\n\n" + // lines 1-2
		fence + "go\n" + // 3
		"// file: a.go\n" + // 4
		"package a\n" + // 5
		"func F() {\n" + // 6
		"\t//:: inner :://\n" + // 7
		"}\n" + // 8
		fence + "\n\n" + // 9-10
		fence + "go\n" + // 11
		"//== inner ==//\n" + // 12
		"_ = `raw\n\nstring`\n" + // 13-15
		"println()\n" + // 16
		fence + "\n"
	got := mustTangle(t, md, "a.go", true)
	if strings.Contains(got, "raw\n//line") {
		t.Fatalf("directive inserted inside raw string:\n%s", got)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", got, 0)
	if err != nil {
		t.Fatal(err)
	}
	pos := fset.Position(f.Decls[0].Pos())
	if filepath.Base(pos.Filename) != "doc.md" || pos.Line != 6 {
		t.Fatalf("func F mapped to %v, want doc.md:6", pos)
	}
	// println() follows a multi-line raw string; its mapping must survive.
	stmts := f.Decls[0].(*ast.FuncDecl).Body.List
	if p := fset.Position(stmts[1].Pos()); p.Line != 16 {
		t.Fatalf("println mapped to %v, want doc.md:16", p)
	}
	if !strings.Contains(got, "//line doc.md:13\n\t_ = `raw\n\nstring`") {
		t.Fatalf("missing directive for chunk:\n%s", got)
	}
}
