package repograph

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
)

type goOwner struct {
	name       string
	start, end token.Pos
}

func extractGo(facts *FileFacts, data []byte) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, facts.Path, data, parser.ParseComments|parser.AllErrors)
	if err != nil {
		facts.Warnings = append(facts.Warnings, "Go parser: "+err.Error())
	}
	if file == nil {
		return
	}

	pkg := file.Name.Name
	owners := make([]goOwner, 0)
	importPositions := make(map[token.Pos]struct{}, len(file.Imports))
	for _, item := range file.Imports {
		target, unquoteErr := strconv.Unquote(item.Path.Value)
		if unquoteErr != nil {
			target = strings.Trim(item.Path.Value, "`\"")
		}
		facts.Imports = append(facts.Imports, ImportFact{Target: target, Line: fset.Position(item.Pos()).Line})
		importPositions[item.Path.Pos()] = struct{}{}
	}

	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			parent := receiverName(declaration.Recv)
			qualified := qualify(pkg, declaration.Name.Name)
			kind := "function"
			if parent != "" {
				kind = "method"
				qualified = parent + "." + declaration.Name.Name
			}
			symbol := SymbolFact{
				Name:      declaration.Name.Name,
				Qualified: qualified,
				Kind:      kind,
				Signature: goFunctionSignature(fset, declaration),
				StartLine: fset.Position(declaration.Pos()).Line,
				EndLine:   fset.Position(declaration.End()).Line,
				Parent:    parent,
				Exported:  ast.IsExported(declaration.Name.Name),
			}
			facts.Symbols = append(facts.Symbols, symbol)
			owners = append(owners, goOwner{name: qualified, start: declaration.Pos(), end: declaration.End()})
		case *ast.GenDecl:
			extractGoGenDecl(facts, fset, pkg, declaration)
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			if _, imported := importPositions[node.Pos()]; imported {
				return true
			}
			value, unquoteErr := strconv.Unquote(node.Value)
			if unquoteErr != nil {
				return true
			}
			if kind := literalKind(value); kind != "" {
				facts.Literals = append(facts.Literals, LiteralFact{Value: value, Kind: kind, Line: fset.Position(node.Pos()).Line})
			}
		case *ast.CallExpr:
			callee := goExpressionName(fset, node.Fun)
			if callee == "" {
				return true
			}
			owner := enclosingGoOwner(owners, node.Pos())
			line := fset.Position(node.Pos()).Line
			facts.Calls = append(facts.Calls, CallFact{Caller: owner, Callee: callee, Line: line})
			if method, path := goRouteCall(callee, node.Args); path != "" {
				facts.Routes = append(facts.Routes, RouteFact{Method: method, Path: path, Owner: owner, Line: line})
			}
		}
		return true
	})
}

func extractGoGenDecl(facts *FileFacts, fset *token.FileSet, pkg string, declaration *ast.GenDecl) {
	for _, spec := range declaration.Specs {
		switch spec := spec.(type) {
		case *ast.TypeSpec:
			kind := "type"
			switch spec.Type.(type) {
			case *ast.StructType:
				kind = "struct"
			case *ast.InterfaceType:
				kind = "interface"
			}
			facts.Symbols = append(facts.Symbols, SymbolFact{
				Name:      spec.Name.Name,
				Qualified: qualify(pkg, spec.Name.Name),
				Kind:      kind,
				Signature: printGoNode(fset, spec),
				StartLine: fset.Position(spec.Pos()).Line,
				EndLine:   fset.Position(spec.End()).Line,
				Exported:  ast.IsExported(spec.Name.Name),
			})
			extractGoTypeMembers(facts, fset, spec)
		case *ast.ValueSpec:
			kind := "variable"
			if declaration.Tok == token.CONST {
				kind = "constant"
			}
			for _, name := range spec.Names {
				facts.Symbols = append(facts.Symbols, SymbolFact{
					Name:      name.Name,
					Qualified: qualify(pkg, name.Name),
					Kind:      kind,
					Signature: printGoNode(fset, spec),
					StartLine: fset.Position(spec.Pos()).Line,
					EndLine:   fset.Position(spec.End()).Line,
					Exported:  ast.IsExported(name.Name),
				})
			}
		}
	}
}

func extractGoTypeMembers(facts *FileFacts, fset *token.FileSet, spec *ast.TypeSpec) {
	var fields *ast.FieldList
	switch value := spec.Type.(type) {
	case *ast.StructType:
		fields = value.Fields
	case *ast.InterfaceType:
		fields = value.Methods
	}
	if fields == nil {
		return
	}

	for _, field := range fields.List {
		if len(field.Names) == 0 {
			parent := goExpressionName(fset, field.Type)
			if parent != "" {
				facts.Inheritance = append(facts.Inheritance, InheritanceFact{
					Child: spec.Name.Name, Parent: parent, Line: fset.Position(field.Pos()).Line,
				})
			}
			continue
		}
		kind := "field"
		if _, ok := spec.Type.(*ast.InterfaceType); ok {
			kind = "method"
		}
		for _, name := range field.Names {
			facts.Symbols = append(facts.Symbols, SymbolFact{
				Name:      name.Name,
				Qualified: spec.Name.Name + "." + name.Name,
				Kind:      kind,
				Signature: printGoNode(fset, field),
				StartLine: fset.Position(field.Pos()).Line,
				EndLine:   fset.Position(field.End()).Line,
				Parent:    spec.Name.Name,
				Exported:  ast.IsExported(name.Name),
			})
		}
	}
}

func goFunctionSignature(fset *token.FileSet, declaration *ast.FuncDecl) string {
	copy := *declaration
	copy.Doc = nil
	copy.Body = nil
	return printGoNode(fset, &copy)
}

func printGoNode(fset *token.FileSet, node any) string {
	var output bytes.Buffer
	if err := printer.Fprint(&output, fset, node); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(output.String()), " ")
}

func receiverName(receivers *ast.FieldList) string {
	if receivers == nil || len(receivers.List) == 0 {
		return ""
	}
	return bareGoType(receivers.List[0].Type)
}

func bareGoType(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return bareGoType(expression.X)
	case *ast.IndexExpr:
		return bareGoType(expression.X)
	case *ast.IndexListExpr:
		return bareGoType(expression.X)
	case *ast.SelectorExpr:
		left := bareGoType(expression.X)
		if left == "" {
			return expression.Sel.Name
		}
		return left + "." + expression.Sel.Name
	default:
		return ""
	}
}

func goExpressionName(fset *token.FileSet, expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		left := goExpressionName(fset, expression.X)
		if left == "" {
			return expression.Sel.Name
		}
		return left + "." + expression.Sel.Name
	case *ast.ParenExpr:
		return goExpressionName(fset, expression.X)
	case *ast.IndexExpr:
		return goExpressionName(fset, expression.X)
	case *ast.IndexListExpr:
		return goExpressionName(fset, expression.X)
	case *ast.StarExpr:
		return goExpressionName(fset, expression.X)
	default:
		return ""
	}
}

func enclosingGoOwner(owners []goOwner, position token.Pos) string {
	best := token.Pos(0)
	name := ""
	for _, owner := range owners {
		if owner.start <= position && position <= owner.end && owner.start >= best {
			best = owner.start
			name = owner.name
		}
	}
	return name
}

func goRouteCall(callee string, arguments []ast.Expr) (string, string) {
	part := callee
	if index := strings.LastIndexByte(part, '.'); index >= 0 {
		part = part[index+1:]
	}
	lower := strings.ToLower(part)
	method := ""
	pathIndex := 0
	switch lower {
	case "get", "post", "put", "patch", "delete", "options", "head", "connect", "trace":
		method = strings.ToUpper(lower)
	case "handle", "handlefunc", "route", "group", "mount", "use", "any", "all":
	case "method", "methodfunc", "methods":
		if len(arguments) < 2 {
			return "", ""
		}
		if value, ok := goString(arguments[0]); ok {
			method = strings.ToUpper(value)
		}
		pathIndex = 1
	default:
		return "", ""
	}
	if pathIndex >= len(arguments) {
		return "", ""
	}
	value, ok := goString(arguments[pathIndex])
	if !ok {
		return "", ""
	}
	// Go 1.22 ServeMux patterns may prefix a route with an HTTP method,
	// for example HandleFunc("GET /users/{id}", handler).
	if method == "" {
		if fields := strings.Fields(value); len(fields) == 2 && isHTTPMethod(fields[0]) {
			method = strings.ToUpper(fields[0])
			value = fields[1]
		}
	}
	return method, normalizeExtractedRoute(value)
}

func isHTTPMethod(value string) bool {
	switch strings.ToUpper(value) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "CONNECT", "TRACE":
		return true
	default:
		return false
	}
}

func goString(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func qualify(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}
