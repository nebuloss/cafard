package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The calls whose format is said in French, by where they may be written
// bare: refuse and Refuse inside service/access, T inside this package,
// say (each surface's T for its request) anywhere; and as selectors,
// access.Refuse and i18n.T. Each takes its format second.
var (
	bare      = map[string]string{"refuse": "internal/service/access", "Refuse": "internal/service/access", "T": "internal/i18n", "say": ""}
	selectors = map[string]bool{"access.Refuse": true, "i18n.T": true}
)

// forwarders pass on a format they were given; theirs is the only call
// that may take one that is not a literal.
var forwarders = map[string]bool{"refuse": true, "Refuse": true, "T": true, "say": true, "In": true}

// TestCatalog reads cafard's source: every format said through T has its
// French, with the same verbs; every French entry is still said somewhere;
// and the French spaces its punctuation with no-break spaces.
func TestCatalog(t *testing.T) {
	used := map[string][]string{}
	for _, c := range translatedCalls(t) {
		if !c.literal {
			t.Errorf("%s: a format that is not a literal cannot be checked against the catalog", c.pos)
			continue
		}
		used[c.format] = append(used[c.format], c.pos)
	}
	if len(used) == 0 {
		t.Fatal("found no translated calls: the scan is broken")
	}

	var formats []string
	for f := range used {
		formats = append(formats, f)
	}
	sort.Strings(formats)
	for _, f := range formats {
		french, ok := fr[f]
		if !ok {
			t.Errorf("%s: no French for %q: add it to fr.go", used[f][0], f)
			continue
		}
		want, err := verbs(f)
		if err != nil {
			t.Errorf("%s: %q: %v", used[f][0], f, err)
			continue
		}
		got, err := verbs(french)
		if err != nil {
			t.Errorf("French for %q: %v", f, err)
			continue
		}
		if !sameVerbs(want, got) {
			t.Errorf("French for %q has verbs %v, the English %v", f, got, want)
		}
	}
	for f, french := range fr {
		if _, ok := used[f]; !ok {
			t.Errorf("fr.go has %q, which nothing says any more: remove it", f)
		}
		for _, p := range []string{" :", " ;", " ?", " !"} {
			if strings.Contains(french, p) {
				t.Errorf("French for %q: %q needs a no-break space (\\u00a0), not a space", f, p)
			}
		}
	}
}

func TestVerbs(t *testing.T) {
	for _, c := range []struct {
		format string
		want   map[int]rune
	}{
		{"plain, 100%%", map[int]rune{}},
		{"%s is at %.0f%% of %d", map[int]rune{1: 's', 2: 'f', 3: 'd'}},
		{"%[2]s then %[1]q", map[int]rune{1: 'q', 2: 's'}},
		{"%-8s|%+d", map[int]rune{1: 's', 2: 'd'}},
	} {
		got, err := verbs(c.format)
		if err != nil || !sameVerbs(got, c.want) {
			t.Errorf("verbs(%q) = %v, %v; want %v", c.format, got, err, c.want)
		}
	}
	if _, err := verbs("ends in %"); err == nil {
		t.Error("a format ending in a lone percent sign passed")
	}
}

type call struct {
	pos     string
	format  string
	literal bool
}

// translatedCalls are the calls to T, say and the refusals in every
// non-test file under internal/ and cmd/.
func translatedCalls(t *testing.T) []call {
	t.Helper()
	root := moduleRoot(t)
	fset := token.NewFileSet()
	var out []call
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "testdata" || name == "dist" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			pkg := filepath.ToSlash(rel)
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			for _, decl := range f.Decls {
				fn, _ := decl.(*ast.FuncDecl)
				ast.Inspect(decl, func(n ast.Node) bool {
					c, ok := n.(*ast.CallExpr)
					if !ok || len(c.Args) < 2 || !translates(c.Fun, pkg) {
						return true
					}
					s, lit := literal(c.Args[1])
					if !lit && fn != nil && forwarders[fn.Name.Name] {
						return true
					}
					out = append(out, call{pos: fset.Position(c.Pos()).String(), format: s, literal: lit})
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func translates(fun ast.Expr, pkg string) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		where, ok := bare[f.Name]
		return ok && (where == "" || where == pkg)
	case *ast.SelectorExpr:
		x, ok := f.X.(*ast.Ident)
		return ok && selectors[x.Name+"."+f.Sel.Name]
	}
	return false
}

// literal is the string e is, if it is a string literal or literals joined
// with +.
func literal(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return literal(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		a, ok1 := literal(e.X)
		b, ok2 := literal(e.Y)
		return a + b, ok1 && ok2
	}
	return "", false
}

// verbs is, for each argument a format uses, numbered from 1, the verb it
// is formatted with. Explicit indexes (%[2]s) are followed as fmt follows
// them, so a French format may take its arguments in another order.
func verbs(format string) (map[int]rune, error) {
	out := map[int]rune{}
	next := 1
	index := func(i int) int {
		if i < len(format) && format[i] == '[' {
			if end := strings.IndexByte(format[i:], ']'); end > 0 {
				if n, err := strconv.Atoi(format[i+1 : i+end]); err == nil && n > 0 {
					next = n
					return i + end + 1
				}
			}
		}
		return i
	}
	digits := func(i int) int {
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			i++
		}
		return i
	}
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			i++
		}
		i = digits(index(i))
		if i < len(format) && format[i] == '.' {
			i = digits(i + 1)
		}
		i = index(i)
		if i >= len(format) {
			return nil, fmt.Errorf("the format ends in the middle of a verb")
		}
		v, size := utf8.DecodeRuneInString(format[i:])
		if prev, ok := out[next]; ok && prev != v {
			return nil, fmt.Errorf("argument %d is formatted as %%%c and as %%%c", next, prev, v)
		}
		out[next] = v
		next++
		i += size - 1
	}
	return out, nil
}

func sameVerbs(a, b map[int]rune) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// moduleRoot is the directory of the go.mod above this package.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}
