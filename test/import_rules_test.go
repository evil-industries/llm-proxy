package test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// forbiddenImports are replaced by MoeForge/moego packages (see AGENTS.md).
var forbiddenImports = map[string]string{
	"encoding/json":            "github.com/MoeForge/moego/json",
	"encoding/base64":          "github.com/MoeForge/moego/base64",
	"regexp":                   "github.com/MoeForge/moego/regexp",
	"github.com/tidwall/gjson": "github.com/MoeForge/moego/ojson",
	"github.com/tidwall/sjson": "github.com/MoeForge/moego/ojson",
}

// importRuleExclusions lists files of this module that may keep a forbidden
// import, with the reason. Directories with their own go.mod (examples/,
// plugin fixtures) are separate modules and are skipped automatically.
var importRuleExclusions = map[string]string{
	// Upstream-owned test; kept identical to upstream so syncs stay conflict-free.
	"sdk/cliproxy/auth/conductor_refresh_disabled_test.go": "upstream test file",
}

// TestNoForbiddenStandardCodecImports enforces the AGENTS.md rule for every Go
// file built by `go test ./...` from the module root.
func TestNoForbiddenStandardCodecImports(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, errStat := os.Stat(filepath.Join(root, "go.mod")); errStat != nil {
		t.Fatalf("module root not found: %v", errStat)
	}
	seenExclusions := make(map[string]bool)
	var violations []string
	fset := token.NewFileSet()
	errWalk := filepath.WalkDir(root, func(path string, entry fs.DirEntry, errEntry error) error {
		if errEntry != nil {
			return errEntry
		}
		rel, errRel := filepath.Rel(root, path)
		if errRel != nil {
			return errRel
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			name := entry.Name()
			if rel != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			if rel != "." {
				if _, errMod := os.Stat(filepath.Join(path, "go.mod")); errMod == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		file, errParse := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if errParse != nil {
			return errParse
		}
		for _, spec := range file.Imports {
			importPath, errUnquote := strconv.Unquote(spec.Path.Value)
			if errUnquote != nil {
				return errUnquote
			}
			replacement, forbidden := forbiddenImports[importPath]
			if !forbidden {
				continue
			}
			if _, excluded := importRuleExclusions[rel]; excluded {
				seenExclusions[rel] = true
				continue
			}
			violations = append(violations, rel+": "+importPath+" (use "+replacement+")")
		}
		return nil
	})
	if errWalk != nil {
		t.Fatal(errWalk)
	}
	sort.Strings(violations)
	for _, violation := range violations {
		t.Error(violation)
	}
	for rel := range importRuleExclusions {
		if !seenExclusions[rel] {
			t.Errorf("stale exclusion %s: it no longer imports a forbidden package", rel)
		}
	}
}
