package parser

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Parser analyzes the source files and finds the returned error AST nodes
type Parser struct {
	pkgs          []*packages.Package
	parseRetError RetParamParseFunc

	// errsToEdit holds all errors that have to be edited.
	// index in the first slice corresponds to the package index;
	// index in the second slice corresponds to the statement order
	errsToEdit [][]ast.Node
}

type ParserOptions struct {
	// RetParamParser parses and possibly modified each node that represents a returned error
	RetParamParser RetParamParseFunc
	// SkipPaths lists all the paths that should not be analyzed.
	// The output path should be included here.
	SkipPaths []string
}

// RetParamParseFunc is called for each node that represents a returned error.
// If skip is set to true, the node won't be included in the Parse result.
type RetParamParseFunc func(pkg *packages.Package, retParam ast.Expr) (out ast.Expr, skip bool)

func GetDefaultOptions() ParserOptions {
	return ParserOptions{
		RetParamParser: func(_ *packages.Package, retParam ast.Expr) (out ast.Expr, skip bool) {
			return retParam, false
		},
	}
}

// New analyses the package at the given directory and returns a new generator for this package
func New(dir string, options ParserOptions) (Parser, error) {
	// Get the absolute paths
	for i, p := range options.SkipPaths {
		pAbs, err := filepath.Abs(p)
		if err != nil {
			return Parser{}, fmt.Errorf("invalid skip path %s, can't create an absolute path: %w", p, err)
		}
		options.SkipPaths[i] = pAbs
	}

	// To load all project files
	cfg := &packages.Config{
		Mode:  packages.NeedSyntax | packages.NeedFiles | packages.NeedName,
		Dir:   dir,
		Tests: false,
		ParseFile: func(fset *token.FileSet, filename string, data []byte) (*ast.File, error) {
			// Check if the file is within the files to skip
			for _, p := range options.SkipPaths {
				if strings.Contains(filename, p) {
					return nil, nil
				}
			}

			// Check if there are any return or error statements in the file.
			// If none found -> we can skip processing this file
			if !bytes.Contains(data, []byte("return")) && !bytes.Contains(data, []byte("error")) {
				return nil, nil
			}

			const mode = parser.AllErrors | parser.SkipObjectResolution
			return parser.ParseFile(fset, filename, data, mode)
		},
	}

	// Load all nested packages within the directory
	const patterns = "./..."
	pkgs, err := packages.Load(cfg, patterns)
	if err != nil {
		return Parser{}, err
	}

	if cnt := packages.PrintErrors(pkgs); cnt > 0 {
		return Parser{}, fmt.Errorf("failed to load %d packages", cnt)
	}

	if len(pkgs) == 0 {
		return Parser{}, fmt.Errorf("no packages found in %s", dir)
	}

	return Parser{
		pkgs:          pkgs,
		parseRetError: options.RetParamParser,
	}, nil
}

// Parse returns the error nodes that represent returned error params. They are
// divided into the packages they belong to.
func (g *Parser) Parse() (map[*packages.Package][]ast.Node, error) {
	g.errsToEdit = make([][]ast.Node, len(g.pkgs))

	for pkgIdx, pkg := range g.pkgs {

		g.filterPackageDecls(pkg)

		// The remaining declarations are now only function declarations that return an error
		for _, stxFile := range pkg.Syntax {
			filename := getFilename(pkg, stxFile.FileStart)

			for _, d := range stxFile.Decls {
				funcDecl, ok := d.(*ast.FuncDecl)
				if !ok {
					// It's a bug!
					return nil, fmt.Errorf("%s: expected a function declaration, found: %T %+v", filename, d, d)
				}

				if funcDecl.Body == nil {
					// Shouldn't happen
					return nil, fmt.Errorf("%s: function declaration has no body: %s", filename, funcDecl.Name)
				}

				err := g.parseFunction(pkg, pkgIdx, funcDecl.Type, funcDecl.Body)
				if err != nil {
					return nil, fmt.Errorf("%s: failed to update function: %w", filename, err)
				}
			}
		}
	}

	// Convert the errsToEdit to the returned map
	ret := make(map[*packages.Package][]ast.Node, len(g.pkgs))
	for i, nodes := range g.errsToEdit {
		pkg := g.pkgs[i]
		ret[pkg] = nodes
	}

	return ret, nil
}

func (g *Parser) parseFunction(pkg *packages.Package, pkgIdx int, funcType *ast.FuncType, funcBody *ast.BlockStmt) error {

	retErrIdx := g.findResultParamIdx(funcType)
	if retErrIdx == -1 {
		// Error is not in the returned values
		return nil
	}
	inspectErrs := make([]error, 0)
	for _, stmt := range funcBody.List {
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncLit:
				// Parsing an annonymous function
				if err := g.parseFunction(pkg, pkgIdx, node.Type, node.Body); err != nil {
					inspectErrs = append(inspectErrs, err)
					return false
				}
				return false
			case *ast.ReturnStmt:
				g.parseResultParams(pkg, pkgIdx, node, retErrIdx, funcType.Results.NumFields())
				return false
			default:
				return true
			}
		})
	}

	return errors.Join(inspectErrs...)
}

// findResultParamIdx returns -1 if error in not found among returned params
func (g Parser) findResultParamIdx(funcType *ast.FuncType) int {
	// Find which ret param is an error
	retErrIdx := -1
	paramCnt := 0
	for _, res := range funcType.Results.List {
		// The returned error is of the ast.Ident type
		resType, ok := res.Type.(*ast.Ident)
		if ok && resType.Name == "error" {
			retErrIdx = paramCnt
			break
		}

		// If a function returns multiple times the same type and it's named,
		// the funcDecl.Type.Results.List will track it as just one result with multiple underlying Names;
		// e.g. `s1 string, s2 string, err error` will be represented as 2 List Results
		// where the first one has two names: s1 and s2.
		if len(res.Names) > 0 {
			paramCnt += len(res.Names)
		} else {
			paramCnt++
		}
	}
	return retErrIdx
}

func (g *Parser) parseResultParams(pkg *packages.Package, pkgIdx int, returnStmt *ast.ReturnStmt, retErrIdx int, retNumFields int) error {

	if len(returnStmt.Results) != retNumFields {
		// There are 2 reasons for it:
		// - just a return keyword is given with no params
		// - the returned value is a function call
		// We will ignore both of these cases.
		log.Default().Println(makeErrorMsgf(pkg, returnStmt, "unexpected number of returned values: %v/%v", len(returnStmt.Results), retNumFields))
		return nil
	}

	retParam := returnStmt.Results[retErrIdx]
	retIdent, ok := retParam.(*ast.Ident)
	if ok {
		if retIdent.Name == "nil" {
			// Ignore
			return nil
		}
	}

	// Let the user parse and edit the returned param node and notify if it should
	// be added to the output nodes
	retParam, skip := g.parseRetError(pkg, retParam)
	if skip {
		return nil
	}

	// Add to the found errors
	g.errsToEdit[pkgIdx] = append(g.errsToEdit[pkgIdx], retParam)
	return nil
}

func makeErrorMsgf(pkg *packages.Package, node ast.Node, message string, args ...any) string {
	if pkg == nil {
		return fmt.Sprintf(message, args...)
	}

	if node == nil {
		// Include just the package name
		msg := fmt.Sprintf("package %s: %s\n", pkg.Name, message)
		return fmt.Sprintf(msg, args...)
	}

	file := pkg.Fset.File(node.Pos())
	if file == nil {
		return fmt.Sprintf(message, args...)
	}

	ln := file.Line(node.Pos())
	filename := getFilename(pkg, node.Pos())

	msg := fmt.Sprintf("%s:%v - %s\n", filename, ln, message)
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

func (g *Parser) filterPackageDecls(pkg *packages.Package) error {
	stxIdx := 0
	for _, stxFile := range pkg.Syntax {
		filename := getFilename(pkg, stxFile.FileStart)

		// Filter functions that return an error
		j := 0
		for _, decl := range stxFile.Decls {
			fnDecl, ok := decl.(*ast.FuncDecl)
			if !ok ||
				fnDecl.Type.Results == nil ||
				len(fnDecl.Type.Results.List) == 0 {
				continue
			}

			rets := fnDecl.Type.Results.List
			for _, ret := range rets {
				// Errors will always implement the Stringer interface
				tp, ok := ret.Type.(fmt.Stringer)
				if !ok {
					continue
				}

				if tp.String() == "error" {
					// Found a function that returns an error,
					// keep it in the declarations list
					stxFile.Decls[j] = decl
					j++
				}
			}
		}
		stxFile.Decls = stxFile.Decls[:j]

		shouldKeep := len(stxFile.Decls) > 0
		if !shouldKeep {
			// Remove from token fileset
			tokenFile := pkg.Fset.File(stxFile.FileStart)
			if tokenFile == nil {
				return errors.New(makeErrorMsgf(pkg, stxFile, "%s: failed to get token file", filename))
			}
			pkg.Fset.RemoveFile(tokenFile)
		} else {
			// Add to the syntax files list
			pkg.Syntax[stxIdx] = stxFile
			stxIdx++
		}
	}
	pkg.Syntax = pkg.Syntax[:stxIdx]

	return nil
}

func getFilename(pkg *packages.Package, position token.Pos) string {
	tokenFile := pkg.Fset.File(position)
	filename := tokenFile.Name()
	return filename
}
