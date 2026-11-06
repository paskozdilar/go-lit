// Command go-lit tangles Go code out of literate Markdown documents.
//
// Fenced ```go blocks are collected by their first line:
//
//	// file: path/to/out.go   append the block to an output file
//	//== name ==//            define (or extend) a named chunk
//
// Inside any block, a line consisting of //:: name :://  is replaced by the
// chunk's lines, indented to match. Blocks without a marker are ignored.
//
// Typical use from a Go file in the same directory:
//
//	//go:generate go-lit -source=architecture.md -outdir=./
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, toolName+":", err)
		os.Exit(1)
	}
}

func run() error {
	source := flag.String("source", "", "comma-separated Markdown files (default: positional args, else all *.md in the current directory)")
	outDir := flag.String("outdir", ".", "directory output paths are relative to")
	lineMap := flag.Bool("linemap", false, "emit //line directives pointing back at the Markdown (breaks gopls and cover HTML)")
	flag.Parse()

	// go generate runs us in the directory of $GOFILE, so the defaults above
	// already place output next to the invoking file.
	var sources []string
	if *source != "" {
		sources = strings.Split(*source, ",")
	} else if flag.NArg() > 0 {
		sources = flag.Args()
	} else {
		var err error
		if sources, err = filepath.Glob("*.md"); err != nil {
			return err
		}
	}
	if len(sources) == 0 {
		return fmt.Errorf("no Markdown sources found")
	}

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
	if len(p.order) == 0 {
		return fmt.Errorf("no \"// file:\" blocks found in %s", strings.Join(sources, ", "))
	}

	files, err := p.Tangle(*outDir, *lineMap)
	if err != nil {
		return err
	}
	return WriteFiles(*outDir, files, os.Getenv("GOFILE"))
}
