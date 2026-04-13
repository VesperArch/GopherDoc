// Package releasecheck implements a static analysis pass that verifies
// engine.Result.Doc.Release() is called for every engine.Result received
// from a GopherDoc pipeline channel.
//
// GopherDoc's pipeline uses a "Transferable Ownership" model backed by
// sync.Pool. Every engine.Result emitted by Pipeline.Run carries a
// pool-allocated buffer via Doc.PoolBuf. The consumer must call Doc.Release()
// once after consuming the content; failing to do so is a silent heap leak.
//
// Tracked receive patterns:
//
//	for result := range pipeline.Run(ctx, n, tasks) { ... }
//	result := <-resultCh
//	result, ok := <-resultCh
//
// Recognized release forms:
//
//	result.Doc.Release()
//	defer result.Doc.Release()  // valid for function-scoped bindings only
//	doc := result.Doc; doc.Release()
//
// For range-over-channel bindings, defer inside the loop body is NOT accepted:
// the call executes at function return, not per-iteration, so buffers
// accumulate. Only a direct call in the loop body satisfies the contract.
//
// Suppress with //nolint:releasecheck on the receive line when Release is
// guaranteed through a mechanism invisible to this pass (e.g. a helper that
// takes ownership of the Doc).
package releasecheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the releasecheck analysis.Analyzer.
var Analyzer = &analysis.Analyzer{
	Name: "releasecheck",
	Doc: "check that engine.Result.Doc.Release() is called after every channel receive\n\n" +
		"See package documentation for the full memory contract.",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

// EnginePkgPath is the import path of the package that defines the Result type.
// Override in tests to point at a stub without replicating the full module path
// inside testdata/. Production code must not change this value.
var EnginePkgPath = "github.com/vesperarch/gopherdoc/pkg/engine"

const (
	resultTypeName = "Result"
	docFieldName   = "Doc"
	releaseMethod  = "Release"
	nolintMarker   = "nolint:releasecheck"
)

// binding records an engine.Result binding established by a channel receive.
type binding struct {
	varName  string    // bound variable name
	pos      token.Pos // position for diagnostics
	searchIn ast.Node  // loop body (range) or function body (single receive)
	// loopBound: defer inside the loop does not satisfy the contract because
	// it only fires at function return, not at the end of each iteration.
	loopBound bool
	suppress  bool // //nolint:releasecheck present on the binding line
}

func run(pass *analysis.Pass) (interface{}, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	commentsByLine := buildCommentLineMap(pass)

	nodeFilter := []ast.Node{
		(*ast.FuncDecl)(nil),
		(*ast.FuncLit)(nil),
	}

	insp.Nodes(nodeFilter, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		var body *ast.BlockStmt
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body == nil {
				return false // external or interface function
			}
			body = fn.Body
		case *ast.FuncLit:
			body = fn.Body
		}
		checkFunc(pass, body, commentsByLine)
		return true // nested FuncLits are visited as separate nodes
	})

	return nil, nil
}

func checkFunc(pass *analysis.Pass, body *ast.BlockStmt, commentsByLine map[int][]string) {
	for _, b := range collectBindings(pass, body, commentsByLine) {
		if b.suppress {
			continue
		}
		aliases := collectDocAliases(b.searchIn, b.varName)
		if !hasRelease(b.searchIn, b.varName, aliases, !b.loopBound) {
			pass.Reportf(b.pos,
				"engine.Result %q received from channel but Doc.Release() is never called "+
					"within the required scope; the pool-backed buffer backing Doc.Content "+
					"will leak — add result.Doc.Release() or defer result.Doc.Release() "+
					"(for function-scoped bindings), or suppress with //nolint:releasecheck "+
					"if Release is called via a helper",
				b.varName,
			)
		}
	}
}

// collectBindings walks body (without descending into nested function literals)
// and returns every engine.Result channel-receive binding it finds.
func collectBindings(
	pass *analysis.Pass,
	body *ast.BlockStmt,
	commentsByLine map[int][]string,
) []binding {
	var bindings []binding
	walkShallow(body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.RangeStmt:
			if b, ok := rangeBinding(pass, stmt, commentsByLine); ok {
				bindings = append(bindings, b)
			}
		case *ast.AssignStmt:
			bindings = append(bindings, receiveBindings(pass, stmt, body, commentsByLine)...)
		}
		return true
	})
	return bindings
}

func rangeBinding(
	pass *analysis.Pass,
	stmt *ast.RangeStmt,
	commentsByLine map[int][]string,
) (binding, bool) {
	if stmt.Key == nil {
		return binding{}, false
	}
	ident, ok := stmt.Key.(*ast.Ident)
	if !ok || ident.Name == "_" {
		return binding{}, false
	}
	chanType, ok := chanOf(pass, stmt.X)
	if !ok || !isEngineResult(pass, chanType.Elem()) {
		return binding{}, false
	}
	line := pass.Fset.Position(stmt.Pos()).Line
	return binding{
		varName:   ident.Name,
		pos:       stmt.Pos(),
		searchIn:  stmt.Body,
		loopBound: true,
		suppress:  hasSuppression(commentsByLine, line),
	}, true
}

// receiveBindings handles both result := <-ch and result, ok := <-ch.
func receiveBindings(
	pass *analysis.Pass,
	stmt *ast.AssignStmt,
	funcBody *ast.BlockStmt,
	commentsByLine map[int][]string,
) []binding {
	// ignore op-assigns (+=, etc.)
	if stmt.Tok != token.DEFINE && stmt.Tok != token.ASSIGN {
		return nil
	}
	// channel receive always collapses to a single RHS expression
	if len(stmt.Rhs) != 1 {
		return nil
	}
	unary, ok := stmt.Rhs[0].(*ast.UnaryExpr)
	if !ok || unary.Op != token.ARROW {
		return nil
	}
	// resolve via channel element type — more robust than TypeOf(<-ch) for
	// the two-value form (result, ok := <-ch)
	chanType, ok := chanOf(pass, unary.X)
	if !ok || !isEngineResult(pass, chanType.Elem()) {
		return nil
	}
	lhsIdent, ok := stmt.Lhs[0].(*ast.Ident)
	if !ok || lhsIdent.Name == "_" {
		return nil
	}
	line := pass.Fset.Position(stmt.Pos()).Line
	return []binding{{
		varName:   lhsIdent.Name,
		pos:       stmt.Pos(),
		searchIn:  funcBody,
		loopBound: false,
		suppress:  hasSuppression(commentsByLine, line),
	}}
}

// hasRelease reports whether scope contains a Release call for varName.
// allowDefer controls whether a DeferStmt counts as a valid release.
func hasRelease(scope ast.Node, varName string, aliases map[string]bool, allowDefer bool) bool {
	found := false
	ast.Inspect(scope, func(n ast.Node) bool {
		if found || n == nil {
			return false
		}
		var call *ast.CallExpr
		isDefer := false
		switch stmt := n.(type) {
		case *ast.ExprStmt:
			c, ok := stmt.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			call = c
		case *ast.DeferStmt:
			call = stmt.Call
			isDefer = true
		default:
			return true
		}
		if isDefer && !allowDefer {
			return true
		}
		if isDirectReleaseCall(call, varName) || isAliasReleaseCall(call, aliases) {
			found = true
		}
		return !found
	})
	return found
}

// isDirectReleaseCall matches varName.Doc.Release() with no arguments.
func isDirectReleaseCall(call *ast.CallExpr, varName string) bool {
	if len(call.Args) != 0 {
		return false
	}
	outer, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || outer.Sel.Name != releaseMethod {
		return false
	}
	inner, ok := outer.X.(*ast.SelectorExpr)
	if !ok || inner.Sel.Name != docFieldName {
		return false
	}
	recv, ok := inner.X.(*ast.Ident)
	return ok && recv.Name == varName
}

// isAliasReleaseCall matches alias.Release() where alias ∈ aliases.
func isAliasReleaseCall(call *ast.CallExpr, aliases map[string]bool) bool {
	if len(call.Args) != 0 || len(aliases) == 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != releaseMethod {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && aliases[recv.Name]
}

// collectDocAliases returns identifiers assigned from varName.Doc within scope
// (e.g. doc := result.Doc), enabling alias.Release() to satisfy the contract.
func collectDocAliases(scope ast.Node, varName string) map[string]bool {
	aliases := make(map[string]bool)
	ast.Inspect(scope, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			sel, ok := rhs.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != docFieldName {
				continue
			}
			src, ok := sel.X.(*ast.Ident)
			if !ok || src.Name != varName {
				continue
			}
			if i < len(assign.Lhs) {
				if lhsIdent, ok := assign.Lhs[i].(*ast.Ident); ok && lhsIdent.Name != "_" {
					aliases[lhsIdent.Name] = true
				}
			}
		}
		return true
	})
	return aliases
}

func chanOf(pass *analysis.Pass, expr ast.Expr) (*types.Chan, bool) {
	t := pass.TypesInfo.TypeOf(expr)
	if t == nil {
		return nil, false
	}
	ch, ok := t.Underlying().(*types.Chan)
	return ch, ok
}

// isEngineResult unwraps a pointer if present, then checks for engine.Result.
func isEngineResult(pass *analysis.Pass, t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil &&
		obj.Pkg() != nil &&
		obj.Pkg().Path() == EnginePkgPath &&
		obj.Name() == resultTypeName
}

// walkShallow calls ast.Inspect on n but stops at nested FuncLit nodes.
// Closures are visited as independent function scopes by the top-level Nodes call.
func walkShallow(n ast.Node, f func(ast.Node) bool) {
	ast.Inspect(n, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok && node != n {
			return false // do not recurse into closures
		}
		return f(node)
	})
}

// buildCommentLineMap indexes package comments by line number for O(1) suppression lookups.
func buildCommentLineMap(pass *analysis.Pass) map[int][]string {
	m := make(map[int][]string)
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				line := pass.Fset.Position(c.Pos()).Line
				m[line] = append(m[line], c.Text)
			}
		}
	}
	return m
}

func hasSuppression(commentsByLine map[int][]string, line int) bool {
	for _, text := range commentsByLine[line] {
		if strings.Contains(text, nolintMarker) {
			return true
		}
	}
	return false
}
