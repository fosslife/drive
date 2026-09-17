package files

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// 9.5: permanent deletion is the only thing that destroys user file content.
//
// This is the whole-codebase form of the rule the design opens with. It parses
// every non-test source file, finds every call that can remove or truncate
// something on disk, and insists each one is a site somebody deliberately
// listed here with a reason. A new Remove anywhere fails this test, which is
// the point: the ways to lose a file are meant to be countable.
func TestOnlyPermanentDeleteDestroysContent(t *testing.T) {
	// Keyed by "<path>:<enclosing function>". A site earns an entry by being
	// permanent deletion, the trash step of an overwrite, or cleanup of
	// temporary data no user has ever seen.
	allowed := map[string]string{
		"internal/storage/storage.go:Write":         "removes its own temp file when the write failed; the destination is never touched",
		"internal/storage/storage.go:Purge":         "permanent deletion, the one place bytes are destroyed on purpose",
		"internal/storage/storage.go:DiscardUpload": "removes an abandoned upload's temp data, which has no destination yet",
	}

	eachFunc(t, func(site string, fn *ast.FuncDecl) {
		ast.Inspect(fn, func(n ast.Node) bool {
			name := ""
			switch n := n.(type) {
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && destroys(n, sel) {
					name = sel.Sel.Name
				}
			case *ast.Ident:
				if n.Name == "O_TRUNC" {
					name = n.Name
				}
			}
			if name == "" {
				return true
			}
			if _, ok := allowed[site]; !ok {
				t.Errorf("%s uses %s: anything that can destroy content must be reachable only from "+
					"permanent deletion, the trash step of an overwrite, or temp cleanup. If it is one "+
					"of those, add it to the allowlist in this test with the reason.", site, name)
			}
			return true
		})
	})
}

// destroys reports whether a call can remove or shorten something on disk.
// Matched on the name because resolving types across the module would cost far
// more than the few false positives it avoids. The two exceptions are the
// names that are almost never a file: Create is an account or a folder unless
// it is os.Create, and Truncate is a timestamp when its argument is a duration.
func destroys(call *ast.CallExpr, sel *ast.SelectorExpr) bool {
	switch sel.Sel.Name {
	case "Remove", "RemoveAll":
		return true
	case "Truncate":
		return !takesFrom(call, "time")
	case "Create", "WriteFile":
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "os"
	}
	return false
}

// takesFrom reports whether any argument is a value out of the named package,
// as in t.Truncate(time.Second).
func takesFrom(call *ast.CallExpr, pkg string) bool {
	for _, arg := range call.Args {
		sel, ok := arg.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == pkg {
			return true
		}
	}
	return false
}

// 9.5: the same rule in the index. A row for content a user can still see is
// never deleted; only Purge removes one, and it removes the bytes first.
func TestOnlyPurgeDeletesFileRows(t *testing.T) {
	eachFunc(t, func(site string, fn *ast.FuncDecl) {
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			sql := strings.Join(strings.Fields(lit.Value), " ")
			if strings.Contains(sql, "DELETE FROM files") && fn.Name.Name != "Purge" {
				t.Errorf("%s deletes file rows: only permanent deletion may, and deleting an account "+
					"cascades in the schema rather than in a statement", site)
			}
			return true
		})
	})
}

// eachFunc visits every function in the module's own non-test source.
func eachFunc(t *testing.T, visit func(site string, fn *ast.FuncDecl)) {
	t.Helper()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && (d.Name() == ".git" || d.Name() == "openspec" || d.Name() == "node_modules"):
			return fs.SkipDir
		case d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go"):
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				visit(rel+":"+fn.Name.Name, fn)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
