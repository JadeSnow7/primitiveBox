package astdiff

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

type SymbolChange struct {
	Kind      string `json:"kind"`
	Symbol    string `json:"symbol"`
	CallSites int    `json:"call_sites"`
	Summary   string `json:"summary"`
}

type fileIndex struct {
	pkgName string
	funcs   map[string]funcDecl
	types   map[string]typeDecl
	file    *ast.File
}

type funcDecl struct {
	name           string
	receiver       string
	receiverType   string
	symbol         string
	signature      string
	paramSummaries []namedType
}

type typeDecl struct {
	name       string
	symbol     string
	signature  string
	structural string
}

type namedType struct {
	name string
	typ  string
}

func Diff(before, after []byte) ([]SymbolChange, error) {
	if bytes.Equal(before, after) {
		return []SymbolChange{}, nil
	}

	beforeIndex, err := parseIndex(before)
	if err != nil {
		return nil, err
	}
	afterIndex, err := parseIndex(after)
	if err != nil {
		return nil, err
	}

	if beforeIndex.pkgName == "" {
		beforeIndex.pkgName = afterIndex.pkgName
	}
	if afterIndex.pkgName == "" {
		afterIndex.pkgName = beforeIndex.pkgName
	}

	changes := make([]SymbolChange, 0)

	for name, beforeDecl := range beforeIndex.funcs {
		afterDecl, ok := afterIndex.funcs[name]
		if !ok {
			continue
		}
		if beforeDecl.signature == afterDecl.signature {
			continue
		}
		changes = append(changes, SymbolChange{
			Kind:      "func_signature",
			Symbol:    afterDecl.symbol,
			CallSites: countCallSites(afterIndex.file, afterDecl),
			Summary:   summarizeSignatureChange(beforeDecl, afterDecl),
		})
	}

	for name, afterDecl := range afterIndex.funcs {
		if afterDecl.receiver == "" {
			continue
		}
		if _, ok := beforeIndex.funcs[name]; ok {
			continue
		}
		changes = append(changes, SymbolChange{
			Kind:      "method_added",
			Symbol:    afterDecl.symbol,
			CallSites: countCallSites(afterIndex.file, afterDecl),
			Summary:   fmt.Sprintf("method %s added", strings.TrimPrefix(afterDecl.symbol, afterIndex.pkgName+".")),
		})
	}

	for name, afterDecl := range afterIndex.types {
		if _, ok := beforeIndex.types[name]; ok {
			continue
		}
		changes = append(changes, SymbolChange{
			Kind:      "type_added",
			Symbol:    afterDecl.symbol,
			CallSites: 0,
			Summary:   fmt.Sprintf("type %s added", name),
		})
	}

	for name, beforeDecl := range beforeIndex.types {
		afterDecl, ok := afterIndex.types[name]
		if !ok {
			changes = append(changes, SymbolChange{
				Kind:      "type_removed",
				Symbol:    beforeDecl.symbol,
				CallSites: 0,
				Summary:   fmt.Sprintf("type %s removed", name),
			})
			continue
		}
		if beforeDecl.structural == afterDecl.structural {
			continue
		}
		changes = append(changes, SymbolChange{
			Kind:      "field_changed",
			Symbol:    afterDecl.symbol,
			CallSites: 0,
			Summary:   summarizeTypeChange(name, beforeDecl, afterDecl),
		})
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Symbol == changes[j].Symbol {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Symbol < changes[j].Symbol
	})

	return changes, nil
}

func parseIndex(content []byte) (*fileIndex, error) {
	index := &fileIndex{
		funcs: make(map[string]funcDecl),
		types: make(map[string]typeDecl),
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return index, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, 0)
	if err != nil {
		return nil, err
	}
	index.file = file
	index.pkgName = file.Name.Name

	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			info := buildFuncDecl(index.pkgName, typed)
			index.funcs[funcKey(info)] = info
		case *ast.GenDecl:
			if typed.Tok != token.TYPE {
				continue
			}
			for _, spec := range typed.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				info := buildTypeDecl(index.pkgName, typeSpec)
				index.types[typeSpec.Name.Name] = info
			}
		}
	}

	return index, nil
}

func buildFuncDecl(pkgName string, decl *ast.FuncDecl) funcDecl {
	info := funcDecl{
		name: decl.Name.Name,
	}
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		info.receiver = receiverName(decl.Recv.List[0].Type)
		info.receiverType = exprString(decl.Recv.List[0].Type)
		info.symbol = pkgName + "." + info.receiver + "." + decl.Name.Name
	} else {
		info.symbol = pkgName + "." + decl.Name.Name
	}
	info.paramSummaries = fieldListSummary(decl.Type.Params)
	info.signature = buildFuncSignature(decl)
	return info
}

func buildTypeDecl(pkgName string, spec *ast.TypeSpec) typeDecl {
	return typeDecl{
		name:       spec.Name.Name,
		symbol:     pkgName + "." + spec.Name.Name,
		signature:  exprString(spec.Type),
		structural: structuralTypeString(spec.Type),
	}
}

func funcKey(info funcDecl) string {
	if info.receiver == "" {
		return info.name
	}
	return info.receiver + "." + info.name
}

func buildFuncSignature(decl *ast.FuncDecl) string {
	var b strings.Builder
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		b.WriteString("recv:")
		b.WriteString(exprString(decl.Recv.List[0].Type))
	}
	b.WriteString("params:")
	b.WriteString(fieldListSignature(decl.Type.Params))
	b.WriteString("|results:")
	b.WriteString(fieldListSignature(decl.Type.Results))
	return b.String()
}

func fieldListSignature(list *ast.FieldList) string {
	if list == nil || len(list.List) == 0 {
		return ""
	}
	items := make([]string, 0, len(list.List))
	for _, field := range list.List {
		typ := exprString(field.Type)
		if len(field.Names) == 0 {
			items = append(items, typ)
			continue
		}
		for range field.Names {
			items = append(items, typ)
		}
	}
	return strings.Join(items, ",")
}

func fieldListSummary(list *ast.FieldList) []namedType {
	if list == nil || len(list.List) == 0 {
		return nil
	}
	out := make([]namedType, 0, len(list.List))
	for _, field := range list.List {
		typ := exprString(field.Type)
		if len(field.Names) == 0 {
			out = append(out, namedType{typ: typ})
			continue
		}
		for _, name := range field.Names {
			out = append(out, namedType{name: name.Name, typ: typ})
		}
	}
	return out
}

func structuralTypeString(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.StructType:
		return "struct{" + fieldListStructuralString(typed.Fields) + "}"
	case *ast.InterfaceType:
		return "interface{" + fieldListStructuralString(typed.Methods) + "}"
	default:
		return exprString(expr)
	}
}

func fieldListStructuralString(list *ast.FieldList) string {
	if list == nil || len(list.List) == 0 {
		return ""
	}
	items := make([]string, 0, len(list.List))
	for _, field := range list.List {
		typ := exprString(field.Type)
		if len(field.Names) == 0 {
			items = append(items, typ)
			continue
		}
		for _, name := range field.Names {
			items = append(items, name.Name+":"+typ)
		}
	}
	return strings.Join(items, ";")
}

func summarizeSignatureChange(before, after funcDecl) string {
	if extra, ok := detectAddedParam(before.paramSummaries, after.paramSummaries); ok {
		label := extra.typ
		if extra.name != "" {
			label = extra.name + " " + extra.typ
		}
		return fmt.Sprintf("func %s signature changed: added param %s", after.name, label)
	}
	return fmt.Sprintf("func %s signature changed", after.name)
}

func detectAddedParam(before, after []namedType) (namedType, bool) {
	if len(after) != len(before)+1 {
		return namedType{}, false
	}
	for i := 0; i < len(after); i++ {
		candidate := after[i]
		matched := true
		for bi, ai := 0, 0; bi < len(before) && ai < len(after); {
			if ai == i {
				ai++
				continue
			}
			if before[bi].typ != after[ai].typ {
				matched = false
				break
			}
			bi++
			ai++
		}
		if matched {
			return candidate, true
		}
	}
	return namedType{}, false
}

func summarizeTypeChange(name string, before, after typeDecl) string {
	if strings.HasPrefix(before.structural, "struct{") || strings.HasPrefix(before.structural, "interface{") {
		return fmt.Sprintf("type %s fields changed", name)
	}
	return fmt.Sprintf("type %s definition changed", name)
}

func countCallSites(file *ast.File, decl funcDecl) int {
	if file == nil {
		return 0
	}
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if decl.receiver == "" && fun.Name == decl.name {
				count++
			}
		case *ast.SelectorExpr:
			if decl.receiver != "" && fun.Sel.Name == decl.name {
				count++
			}
		}
		return true
	})
	return count
}

func receiverName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverName(typed.X)
	case *ast.IndexExpr:
		return receiverName(typed.X)
	case *ast.IndexListExpr:
		return receiverName(typed.X)
	default:
		return exprString(expr)
	}
}

func exprString(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return exprString(typed.X) + "." + typed.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(typed.X)
	case *ast.Ellipsis:
		return "..." + exprString(typed.Elt)
	case *ast.ArrayType:
		if typed.Len == nil {
			return "[]" + exprString(typed.Elt)
		}
		return "[" + exprString(typed.Len) + "]" + exprString(typed.Elt)
	case *ast.MapType:
		return "map[" + exprString(typed.Key) + "]" + exprString(typed.Value)
	case *ast.InterfaceType:
		return "interface{" + fieldListStructuralString(typed.Methods) + "}"
	case *ast.StructType:
		return "struct{" + fieldListStructuralString(typed.Fields) + "}"
	case *ast.FuncType:
		return "func(" + fieldListSignature(typed.Params) + ")" + resultsString(typed.Results)
	case *ast.ChanType:
		prefix := "chan "
		if typed.Dir == ast.SEND {
			prefix = "chan<- "
		}
		if typed.Dir == ast.RECV {
			prefix = "<-chan "
		}
		return prefix + exprString(typed.Value)
	case *ast.ParenExpr:
		return "(" + exprString(typed.X) + ")"
	case *ast.BasicLit:
		return typed.Value
	case *ast.IndexExpr:
		return exprString(typed.X) + "[" + exprString(typed.Index) + "]"
	case *ast.IndexListExpr:
		parts := make([]string, 0, len(typed.Indices))
		for _, index := range typed.Indices {
			parts = append(parts, exprString(index))
		}
		return exprString(typed.X) + "[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprintf("%T", expr)
	}
}

func resultsString(list *ast.FieldList) string {
	if list == nil || len(list.List) == 0 {
		return ""
	}
	return "(" + fieldListSignature(list) + ")"
}
