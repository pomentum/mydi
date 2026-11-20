package mydi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exported is every top-level name the files exported, methods left out.
func exported(t *testing.T, pattern string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		t.Fatalf("no files at %s", pattern)
	}
	names := map[string]bool{}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							names[s.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								names[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return names
}

func TestTheRootForwardsEveryNameSrcExports(t *testing.T) {
	// programs import the root, the code lives in src/ (ADR-0001 part A): go generate at the root writes mydi.go again
	src, root := exported(t, "../src/*.go"), exported(t, "../*.go")
	var missing, extra []string
	for name := range src {
		if !root[name] {
			missing = append(missing, name)
		}
	}
	for name := range root {
		if !src[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("mydi.go is out of step with src/ (run go generate at the root): missing %v, not in src %v", missing, extra)
	}
}
