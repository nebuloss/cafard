package arch

import (
	"bufio"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// layers is the map in the package comment, as data. Keep the two in step.
var layers = map[string]int{
	"internal/version":   0,
	"internal/logging":   0,
	"internal/testenv":   0,
	"internal/arch":      0,
	"internal/config":    0,
	"internal/blobs":     0,
	"internal/ratelimit": 0,
	"internal/webui":     0,
	"internal/live":      0,
	"internal/mcp":       0,
	"internal/imagefit":  0,
	"internal/uploads":   0,
	"internal/install":   0,
	"internal/client":    0,
	"internal/i18n":      0,

	"internal/store": 1,

	"internal/service/access": 2,

	"internal/service/chat":      3,
	"internal/service/attention": 3,

	"internal/httpapi/httpx": 4,

	"internal/httpapi/api":   5,
	"internal/httpapi/admin": 5,
	"internal/httpapi/join":  5,
	"internal/httpapi/agent": 5,

	"internal/httpapi": 6,

	"cmd/cafard-server": 7,
	"cmd/cafard-client": 7,
}

// onlyImportedBy names packages that a lower layer is not enough to make
// available: each may be imported by the listed packages and no others.
var onlyImportedBy = map[string][]string{}

// The HTTP surfaces do not import one another: what one needs from another
// is handed across by the composition root, as in claudication.
var surfaces = []string{"internal/httpapi/api", "internal/httpapi/admin", "internal/httpapi/join", "internal/httpapi/agent"}

func TestSurfacesDoNotImportEachOther(t *testing.T) {
	root, module := moduleRoot(t)
	imports := moduleImports(t, root, module)
	for _, s := range surfaces {
		for _, dep := range imports[s] {
			if contains(surfaces, dep) {
				t.Errorf("%s imports %s: surfaces get what they share from internal/httpapi", s, dep)
			}
		}
	}
}

func TestImportsFollowTheLayers(t *testing.T) {
	root, module := moduleRoot(t)
	imports := moduleImports(t, root, module)

	var pkgs []string
	for p := range imports {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		layer, ok := layers[pkg]
		if !ok {
			t.Errorf("%s is not on the map in internal/arch: place it on a layer", pkg)
			continue
		}
		for _, dep := range imports[pkg] {
			depLayer, ok := layers[dep]
			if !ok {
				continue // reported as a package of its own above
			}
			if depLayer >= layer {
				t.Errorf("%s (layer %d) imports %s (layer %d): a package imports only from layers below its own",
					pkg, layer, dep, depLayer)
			}
			if allowed, ok := onlyImportedBy[dep]; ok && !contains(allowed, pkg) {
				t.Errorf("%s imports %s, which only %s may import",
					pkg, dep, strings.Join(allowed, " and "))
			}
		}
	}

	// A stale entry is a map that no longer describes the code.
	for pkg := range layers {
		if _, ok := imports[pkg]; !ok {
			t.Errorf("%s is on the map in internal/arch but no longer exists", pkg)
		}
	}
}

// Only service/access reads memberships to decide: a package above it that
// calls the store's membership reads is deciding access itself.
func TestOnlyAccessDecidesMembership(t *testing.T) {
	root, _ := moduleRoot(t)
	decides := []string{".IsMember(", ".ShareProject("}
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			pkg := filepath.ToSlash(rel)
			if pkg == "internal/store" || pkg == "internal/service/access" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, call := range decides {
				if strings.Contains(string(src), call) {
					t.Errorf("%s calls %s: ask service/access instead", path, strings.TrimSuffix(call[1:], "("))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// moduleRoot finds go.mod above this package and reads the module path.
func moduleRoot(t *testing.T) (dir, module string) {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		f, err := os.Open(filepath.Join(dir, "go.mod"))
		if err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if m, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
					return dir, strings.TrimSpace(m)
				}
			}
			t.Fatal("go.mod names no module")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}

// moduleImports reads, for every package under internal/ and cmd/, which of
// the module's own packages its non-test files import. Test files are free to
// reach further: a test of the composed server is not a dependency of it.
func moduleImports(t *testing.T, root, module string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Embedded UI and fixtures are not Go packages.
				if name := d.Name(); name == "webdist" || name == "testdata" {
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
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			deps := out[pkg]
			if deps == nil {
				deps = []string{}
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if dep, ok := strings.CutPrefix(p, module+"/"); ok && !contains(deps, dep) {
					deps = append(deps, dep)
				}
			}
			out[pkg] = deps
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
