# go-lit

`go-lit` turns a Markdown document into Go source files. You write prose for
humans and put the code in fenced ` ```go ` blocks; `go-lit` pulls the code out,
puts it back together in the order the compiler needs, and emits formatted Go
files with their imports filled in.

This document *is* `go-lit`. Every `.go` file in this directory except
`doc.go` is generated from the code blocks below, so the best way to read the
program is top to bottom, right here.

## Contents

1. [The notation](#the-notation)
2. [Bootstrapping](#bootstrapping)
3. [The command line](#the-command-line)
4. [Lines and the symbol table](#lines-and-the-symbol-table)
5. [Reading Markdown](#reading-markdown)
6. [Expanding references](#expanding-references)
7. [Assembling a file](#assembling-a-file)
8. [Pointing errors back at the Markdown](#pointing-errors-back-at-the-markdown)
9. [Formatting and imports](#formatting-and-imports)
10. [Writing to disk](#writing-to-disk)

## The notation

Only fenced blocks tagged `go` are looked at, and only if their first
non-blank line is one of two markers:

```text
// file: path/to/out.go     append this block to an output file
//== name ==//              define a named chunk (or extend it)
```

Anywhere inside a block, a line that consists of nothing but

```text
//:: name :://
```

is replaced by the lines of chunk `name`, indented to match the reference.
Chunks may reference other chunks. Defining the same file or chunk twice
appends to it, in document order.

Every Go declaration is visible to the whole package no matter where in the
file it sits, so top-level code can simply be appended to a file in whatever
order suits the explanation. Chunks are for the remaining case: splitting
*one* function body across several paragraphs.

A `go` block without a marker is just an illustration and is skipped, so you
can still show snippets that aren't part of the program.

Imports are optional. The output goes through `goimports`, which adds what is
missing and removes what is unused. We write imports by hand only where
`goimports` could guess wrong, such as `go/scanner` versus `text/scanner`.

## Bootstrapping

`go-lit` builds itself. `doc.go` is the one hand-written file, and all it
holds is the `go:generate` line:

```go
package main

//go:generate go run . -source=README.md
```

`go generate` compiles the *currently generated* sources, and the result
regenerates them from this document. If an edit here produces Go that doesn't
compile, the tool will not run again until that is fixed. Restore the
generated files with `git checkout -- '*.go'` and try again. A failed tangle
writes nothing, so a broken document never overwrites working code.

## The command line

The command is a straight line: read the flags, decide which Markdown files
to read, load them into a program, and write out what they describe.

```go
// file: main.go

// Command go-lit tangles Go code out of literate Markdown documents.
// See README.md, which is also its source code.
package main

const toolName = "go-lit"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, toolName+":", err)
		os.Exit(1)
	}
}

func run() error {
	//:: flags :://

	//:: choose-sources :://

	//:: load-sources :://

	//:: tangle-and-write :://
}
```

There are three flags. `-linemap` is off by default because `//line`
directives that point at a `.md` file confuse `gopls` and
`go tool cover -html`. Turn it on when you would rather have compiler errors
and stack traces point at this document.

```go
//== flags ==//
source := flag.String("source", "", "comma-separated Markdown files (default: positional args, else all *.md in the current directory)")
outDir := flag.String("outdir", ".", "directory output paths are relative to")
lineMap := flag.Bool("linemap", false, "emit //line directives pointing back at the Markdown (breaks gopls and cover HTML)")
flag.Parse()
```

`go generate` runs commands in the directory of the file holding the
directive, so the defaults already put the output next to that file and pick
up its Markdown neighbours.

```go
//== choose-sources ==//
var sources []string
switch {
case *source != "":
	sources = strings.Split(*source, ",")
case flag.NArg() > 0:
	sources = flag.Args()
default:
	var err error
	if sources, err = filepath.Glob("*.md"); err != nil {
		return err
	}
}
if len(sources) == 0 {
	return fmt.Errorf("no Markdown sources found")
}
```

All sources go into one `Program`, so a chunk defined in one document can be
used in another. If no source declares an output file, we fail loudly rather
than succeed at writing nothing, which usually means the wrong directory or
`-source`.

```go
//== load-sources ==//
p := NewProgram()
for _, src := range sources {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := p.Parse(src, data); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
}
if !p.HasFiles() {
	return fmt.Errorf("no \"// file:\" blocks found in %s", strings.Join(sources, ", "))
}
```

When run by `go generate`, `$GOFILE` names the file with the directive. We
pass it along so that `go-lit` never overwrites it.

```go
//== tangle-and-write ==//
files, err := p.Tangle(*outDir, *lineMap)
if err != nil {
	return err
}
return WriteFiles(*outDir, files, os.Getenv("GOFILE"))
```

## Lines and the symbol table

The rest of the program lives in `tangle.go`. A few imports have to be
spelled out because `goimports` could pick the wrong package: `ast`, `text`
and `parser` also exist in goldmark, and `scanner` also exists in
`text/scanner`.

```go
// file: tangle.go
package main

import (
	"go/scanner"
	"go/token"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"golang.org/x/tools/imports"
)
```

The markers are recognised with regular expressions, and each section below
adds the patterns it needs to this one declaration:

```go
// file: tangle.go

var (
	//:: patterns :://
)
```

The unit of work is a line of Go code that remembers where it came from. The
origin is only needed for error messages and `//line` directives. Lines we
make up ourselves, such as separators or hoisted build tags, have `n == 0`.

```go
// file: tangle.go

type line struct {
	text string
	src  string // Markdown file name
	n    int    // 1-based line number in src; 0 if synthetic
}
```

A `Program` is the symbol table: named chunks, output files, and the order
in which the files were first seen, so output is deterministic.

```go
// file: tangle.go

type Program struct {
	chunks map[string][]line // //== name ==//
	files  map[string][]line // // file: path
	order  []string
}

func NewProgram() *Program {
	return &Program{chunks: map[string][]line{}, files: map[string][]line{}}
}

// HasFiles reports whether any "// file:" block was seen. Chunks alone
// produce no output, so without files there is nothing to write.
func (p *Program) HasFiles() bool {
	return len(p.files) > 0
}
```

## Reading Markdown

We do not scan the Markdown with regular expressions. Goldmark parses it into
a syntax tree, so anything that only *looks* like a code block, such as
indented text or fences inside an HTML block, is never picked up. Its walker
visits nodes depth first, which is document order. That matters, because
appending is order sensitive.

```go
// file: tangle.go

func (p *Program) Parse(srcName string, data []byte) error {
	doc := goldmark.New().Parser().Parse(text.NewReader(data))
	return ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		fb, ok := n.(*ast.FencedCodeBlock)
		if !entering || !ok || string(fb.Language(data)) != "go" {
			return ast.WalkContinue, nil
		}
		//:: block-lines :://
		//:: register-block :://
		return ast.WalkContinue, nil
	})
}
```

Goldmark gives us one byte segment per line of the block. Counting the
newlines before a segment gives its line number. Indented blocks, for example
inside a list, come with padding information that `Value` turns back into
spaces. Leading blank lines are dropped so the marker can be found.

```go
//== block-lines ==//
var lines []line
segs := fb.Lines()
for i := 0; i < segs.Len(); i++ {
	s := segs.At(i)
	n := bytes.Count(data[:s.Start], []byte("\n")) + 1
	lines = append(lines, line{strings.TrimRight(string(s.Value(data)), "\r\n"), srcName, n})
}
for len(lines) > 0 && strings.TrimSpace(lines[0].text) == "" {
	lines = lines[1:]
}
if len(lines) == 0 {
	return ast.WalkContinue, nil
}
```

The marker line is not itself output. Consecutive blocks for the same file
are separated by a blank line, which reads better and costs nothing.
Consecutive pieces of a chunk are joined directly, since they are usually
statements in a row.

```go
//== patterns ==//
fileRe = regexp.MustCompile(`^//\s*file:\s*(\S+)\s*$`)
defRe  = regexp.MustCompile(`^//==\s*(\S+)\s*==//$`)
```

```go
//== register-block ==//
first := strings.TrimSpace(lines[0].text)
if m := fileRe.FindStringSubmatch(first); m != nil {
	path := filepath.Clean(m[1])
	if _, seen := p.files[path]; !seen {
		p.order = append(p.order, path)
	} else {
		p.files[path] = append(p.files[path], line{})
	}
	p.files[path] = append(p.files[path], lines[1:]...)
} else if m := defRe.FindStringSubmatch(first); m != nil {
	p.chunks[m[1]] = append(p.chunks[m[1]], lines[1:]...)
}
```

### Testing as we go

Tests are written next to the code they check. They tangle small documents
built from Go strings. `fence` saves us from writing three backticks inside a
code block, where they would read like the end of the block.

```go
// file: tangle_test.go
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
)

const fence = "```"

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
```

Untagged blocks, other languages and unmarked `go` blocks are all ignored:

```go
// file: tangle_test.go

func TestIgnoresNonGoAndUnmarkedBlocks(t *testing.T) {
	md := fence + "\n// file: nope.go\npackage nope\n" + fence + "\n\n" +
		fence + "python\n// file: nope2.go\n" + fence + "\n\n" +
		fence + "go\nfmt.Println(\"illustration\")\n" + fence + "\n\n" +
		fence + "go\n// file: a.go\npackage a\n" + fence + "\n"
	files, err := tangle(t, md, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files["a.go"] == nil {
		t.Fatalf("unexpected files: %v", files)
	}
}
```

## Expanding references

Expansion goes line by line. Ordinary lines are copied with the current
indentation added in front. A reference line is replaced by its chunk,
expanded recursively with the reference's own indentation added on top.

Following references builds a graph, and a careless document can make it
cyclic. We keep the chain of chunks currently being expanded. A name that is
already on it is a cycle, and the error prints the whole chain so the author
can see where to cut it. A depth limit, the same 256 levels the C
preprocessor uses, catches anything pathological that is not a cycle.

One subtle point: a line inside a multi-line raw string must not be
re-indented, or the string's value changes. The same goes for reference-like
text inside one. `multilineTokenInteriors` finds such lines and is described
[below](#pointing-errors-back-at-the-markdown).

```go
//== patterns ==//
refRe = regexp.MustCompile(`^(\s*)//::\s*(\S+)\s*:://\s*$`)
```

```go
// file: tangle.go

const maxDepth = 256

func (p *Program) expand(in []line, indent string, stack []string) ([]line, error) {
	if len(stack) > maxDepth {
		return nil, fmt.Errorf("macro recursion deeper than %d: %s", maxDepth, strings.Join(stack, " -> "))
	}
	var out []line
	inRaw := multilineTokenInteriors(in)
	for i, l := range in {
		m := refRe.FindStringSubmatch(l.text)
		if m == nil || inRaw[i] {
			if strings.TrimSpace(l.text) != "" && !inRaw[i] {
				l.text = indent + l.text
			}
			out = append(out, l)
			continue
		}
		//:: expand-reference :://
	}
	return out, nil
}
```

The chain starts with a pseudo-entry for the file, so error messages show
where the expansion began.

```go
//== expand-reference ==//
name := m[2]
if slices.Contains(stack, name) {
	return nil, fmt.Errorf("%s:%d: circular reference: %s -> %s", l.src, l.n, strings.Join(stack, " -> "), name)
}
body, ok := p.chunks[name]
if !ok {
	return nil, fmt.Errorf("%s:%d: undefined chunk %q", l.src, l.n, name)
}
sub, err := p.expand(body, indent+m[1], append(stack, name))
if err != nil {
	return nil, err
}
out = append(out, sub...)
```

A reference nested two levels deep gets both levels of indentation, and
`goimports` supplies `fmt` and `strings`:

```go
// file: tangle_test.go

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
```

Cycles and missing chunks are both errors that point into the Markdown:

```go
// file: tangle_test.go

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
```

## Assembling a file

Once a file is fully expanded it is still just a list of lines. Assembling
turns it into one buffer with three parts:

```go
// file: tangle.go

func assemble(lines []line, targetDir string, lineMap bool) []byte {
	//:: hoist-build-constraints :://

	var b strings.Builder
	//:: write-header :://
	//:: write-body :://
	return []byte(b.String())
}
```

**Build constraints** must come before the package clause, or the compiler
silently ignores them. An author may reasonably write `//go:build linux`
next to the code that needs it, in the middle of the document, so we lift
every constraint line out of the body. Legacy `// +build` lines are combined
by AND anyway, so we only remove duplicates. A file may have only one
`//go:build` line, so different expressions are joined with `&&`.

```go
//== patterns ==//
buildRe = regexp.MustCompile(`^\s*(//go:build\s|// \+build\s)`)
```

```go
//== hoist-build-constraints ==//
var body []line
var goBuild, plusBuild []string
for _, l := range lines {
	if !buildRe.MatchString(l.text) {
		body = append(body, l)
		continue
	}
	t := strings.TrimSpace(l.text)
	if expr, ok := strings.CutPrefix(t, "//go:build "); ok {
		if !slices.Contains(goBuild, expr) {
			goBuild = append(goBuild, expr)
		}
	} else if !slices.Contains(plusBuild, t) {
		plusBuild = append(plusBuild, t)
	}
}
```

**The header** is the standard marker that `gopls`, linters and reviewers
recognise as generated code. The constraints follow it, then the blank line
they require.

```go
// file: tangle.go

const header = "// Code generated by " + toolName + ". DO NOT EDIT.\n"
```

```go
//== write-header ==//
b.WriteString(header + "\n")
switch len(goBuild) {
case 0:
case 1:
	b.WriteString("//go:build " + goBuild[0] + "\n")
default:
	b.WriteString("//go:build (" + strings.Join(goBuild, ") && (") + ")\n")
}
for _, t := range plusBuild {
	b.WriteString(t + "\n")
}
if len(goBuild)+len(plusBuild) > 0 {
	b.WriteString("\n")
}
```

**The body** is the remaining lines, optionally with `//line` directives
in between. Those are covered in the next section.

```go
//== write-body ==//
var unsafe map[int]bool
if lineMap {
	unsafe = multilineTokenInteriors(body)
}
for i, l := range body {
	if lineMap && needsDirective(body, i, unsafe) {
		rel, err := filepath.Rel(targetDir, l.src)
		if err != nil {
			rel = l.src
		}
		fmt.Fprintf(&b, "//line %s:%d\n", filepath.ToSlash(rel), l.n)
	}
	b.WriteString(l.text + "\n")
}
```

A constraint written in a later block ends up at the very top:

```go
// file: tangle_test.go

func TestBuildTagsHoisted(t *testing.T) {
	md := fence + "go\n// file: a.go\npackage a\n" + fence + "\n" +
		fence + "go\n// file: a.go\n//go:build linux\n\nvar X = 1\n" + fence + "\n"
	got := mustTangle(t, md, "a.go", false)
	if !strings.HasPrefix(got, header+"\n//go:build linux\n\npackage a\n") {
		t.Fatalf("build tag not hoisted:\n%s", got)
	}
}
```

## Pointing errors back at the Markdown

A `//line file:N` comment tells the compiler that the *next* line is line `N`
of `file`, and that the lines after it follow on from there. Relative names
are resolved against the directory of the generated file, which is why
`write-body` uses `filepath.Rel`. With directives in place, compiler errors,
panics and debuggers all report positions in this document.

The obvious approach is a directive at the start of each chunk and after
each chunk ends. That is not enough, because formatting runs *after* we emit
the directives and moves lines around: `gofmt` collapses runs of blank lines
and inserts a blank line between top-level declarations. Every inserted or
removed line shifts the mapping by one. So a line gets a directive when

- it is the first line,
- it does not directly follow the previous line in the Markdown (a chunk
  starts or ends here),
- the previous line is blank (gofmt may have removed lines here), or
- it starts in column 0 with something other than a closing bracket, which
  means a new top-level declaration (gofmt may have added a line before it).

A directive is never put on a blank line, which gofmt might delete along with
the directive, nor on a line that has no origin.

```go
// file: tangle.go

func needsDirective(body []line, i int, unsafe map[int]bool) bool {
	l := body[i]
	if l.n == 0 || strings.TrimSpace(l.text) == "" || unsafe[i] {
		return false
	}
	if i == 0 {
		return true
	}
	prev := body[i-1]
	return prev.src != l.src || prev.n != l.n-1 ||
		strings.TrimSpace(prev.text) == "" ||
		!strings.ContainsAny(l.text[:1], " \t})")
}
```

The `unsafe` lines are the ones that begin *inside* a token spanning several
lines: a raw string, where a directive would become part of the string's
value, or a `/* */` comment, which includes cgo preambles, where it would
become C code. Rather than guess with string matching, we run Go's own
scanner over the lines and note the inner lines of every multi-line string
or comment. The scanner keeps going after errors, which is what we want for
fragments that are not complete files yet.

```go
// file: tangle.go

func multilineTokenInteriors(lines []line) map[int]bool {
	var src strings.Builder
	for _, l := range lines {
		src.WriteString(l.text + "\n")
	}
	fset := token.NewFileSet()
	f := fset.AddFile("", -1, src.Len())
	var s scanner.Scanner
	s.Init(f, []byte(src.String()), func(token.Position, string) {}, scanner.ScanComments)
	inside := map[int]bool{}
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return inside
		}
		if tok != token.STRING && tok != token.COMMENT {
			continue
		}
		start := fset.Position(pos).Line
		for ln := start + 1; ln <= start+strings.Count(lit, "\n"); ln++ {
			inside[ln-1] = true // scanner lines are 1-based, indexes 0-based
		}
	}
}
```

The test checks the cases from this section: a chunk jump, a raw string with
a blank line inside it (which must not be touched), a mapping that has to
survive the end of that raw string, and a top-level declaration after which
gofmt inserts a blank line.

```go
// file: tangle_test.go

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
	if !strings.Contains(got, "//line doc.md:13\n\t_ = `raw\n\nstring`") {
		t.Fatalf("raw string altered or not mapped:\n%s", got)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", got, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn := f.Decls[0].(*ast.FuncDecl)
	if p := fset.Position(fn.Pos()); filepath.Base(p.Filename) != "doc.md" || p.Line != 6 {
		t.Fatalf("func F mapped to %v, want doc.md:6", p)
	}
	if p := fset.Position(fn.Body.List[1].Pos()); p.Line != 16 {
		t.Fatalf("println mapped to %v, want doc.md:16", p)
	}
}
```

## Formatting and imports

`Tangle` connects the steps: for each output file, expand it, assemble it,
then hand it to `imports.Process`, the library behind `goimports`. That one
call parses the code, formats it the way `gofmt` would, adds missing imports,
and removes unused or duplicate ones, including duplicates that come from
several blocks importing the same package. Its file name argument tells it
which module to search for imports.

If the tangled code does not parse, the error positions refer to a buffer
nobody can see, so we attach that buffer with line numbers.

```go
// file: tangle.go

func (p *Program) Tangle(outDir string, lineMap bool) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, path := range p.order {
		lines, err := p.expand(p.files[path], "", []string{"file:" + path})
		if err != nil {
			return nil, err
		}
		target := filepath.Join(outDir, path)
		raw := assemble(lines, filepath.Dir(target), lineMap)
		src, err := imports.Process(target, raw, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8})
		if err != nil {
			return nil, fmt.Errorf("%s: %w\n--- tangled source ---\n%s", path, err, numbered(raw))
		}
		out[path] = src
	}
	return out, nil
}

func numbered(src []byte) string {
	var b strings.Builder
	for i, l := range strings.Split(strings.TrimRight(string(src), "\n"), "\n") {
		fmt.Fprintf(&b, "%4d  %s\n", i+1, l)
	}
	return b.String()
}
```

## Writing to disk

Nothing is written until every file has tangled successfully, which is what
makes the bootstrapping safe. Files whose content has not changed are left
alone, so running `go generate` again does not touch timestamps or trigger
rebuilds. Last, a guard: the output must never replace the file that holds
the `go:generate` directive.

```go
// file: tangle.go

func WriteFiles(outDir string, files map[string][]byte, protect string) error {
	for path, src := range files {
		target := filepath.Join(outDir, path)
		if protect != "" && sameFile(target, protect) {
			return fmt.Errorf("refusing to overwrite %s (it holds the go:generate directive)", target)
		}
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, src) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, src, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func sameFile(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}
```

That's the whole program. To see it at work on something smaller, look at
[example/hello.md](example/hello.md).
