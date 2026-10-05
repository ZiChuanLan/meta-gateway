package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// parseGoFile parses one Go source file with comments attached, which is what
// the doc comments on config and runtime-settings fields are read from.
func parseGoFile(path string) (*ast.File, *token.FileSet, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file, fset, nil
}

// stringLit unwraps a string literal expression.
func stringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// literalText renders a literal expression the way it would be written in
// source, for the "default value" column. Anything non-literal yields ok=false
// so the caller can print an honest placeholder instead of guessing.
func literalText(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			value, err := strconv.Unquote(e.Value)
			if err != nil {
				return "", false
			}
			if value == "" {
				return `""`, true
			}
			return value, true
		}
		return e.Value, true
	case *ast.Ident:
		// true / false / nil
		return e.Name, true
	case *ast.UnaryExpr:
		if e.Op == token.SUB {
			if inner, ok := literalText(e.X); ok {
				return "-" + inner, true
			}
		}
	case *ast.SelectorExpr:
		// A package-qualified constant (time.Minute, http.StatusOK). Render the
		// qualified name rather than pretending to know its value.
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name, true
		}
	}
	return "", false
}

// exprText renders a short source-like form of an expression, used for handler
// names and similar references.
func exprText(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.CallExpr:
		return exprText(e.Fun) + "()"
	case *ast.BasicLit:
		return e.Value
	case *ast.StarExpr:
		return "*" + exprText(e.X)
	case *ast.ArrayType:
		return "[]" + exprText(e.Elt)
	case *ast.MapType:
		return "map[" + exprText(e.Key) + "]" + exprText(e.Value)
	case *ast.InterfaceType:
		return "any"
	case *ast.ParenExpr:
		return exprText(e.X)
	default:
		return ""
	}
}

// findStruct returns the named struct type declaration in a file.
func findStruct(file *ast.File, name string) (*ast.StructType, bool) {
	var found *ast.StructType
	ast.Inspect(file, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.TYPE {
			return true
		}
		for _, spec := range decl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if ok {
				found = st
			}
			return false
		}
		return true
	})
	return found, found != nil
}

// structFieldDocs maps field name → its doc comment, joined onto one line.
func structFieldDocs(file *ast.File, structName string) map[string]string {
	docs := map[string]string{}
	st, ok := findStruct(file, structName)
	if !ok {
		return docs
	}
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		doc := docText(field.Doc)
		if doc == "" {
			doc = docText(field.Comment)
		}
		for _, name := range field.Names {
			docs[name.Name] = doc
		}
	}
	return docs
}

// docText flattens a doc comment into one line. Comments are wrapped in the
// source, but a reference table reads better with the full sentence than with
// the first 60 characters of it.
func docText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	var parts []string
	for _, line := range strings.Split(strings.TrimSpace(group.Text()), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, " ")
}

// jsonTagName extracts the name from a struct tag's json key.
func jsonTagName(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	raw := strings.Trim(tag.Value, "`")
	const key = `json:"`
	idx := strings.Index(raw, key)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	name := rest[:end]
	if comma := strings.IndexByte(name, ','); comma >= 0 {
		name = name[:comma]
	}
	return name
}

// goFilesIn returns the non-test .go files of a package directory.
func goFilesIn(dir string) ([]string, error) {
	entries, err := readDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range entries {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}
