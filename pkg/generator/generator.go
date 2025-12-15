package generator

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"

	"golang.org/x/tools/go/packages"
)

// Generator holds the state of the analysis. Primarily used to buffer
// the output for format.Source.
type Generator struct {
	pkgs      []*packages.Package
	opts      GenOptions
	originals map[string] /*filename*/ []byte
	edits     map[string] /*filename*/ Edit
}

type Edit struct {
}

type GenOptions struct {
	OutPackageName string
}

func GetDefaultGenOptions() GenOptions {
	return GenOptions{
		OutPackageName: "errnumgen",
	}
}

// New analyses the package at the given directory and returns a new generator for this package
func New(dir string, options GenOptions) (Generator, error) {
	cfg := &packages.Config{
		Mode:  packages.NeedSyntax | packages.NeedFiles | packages.NeedName,
		Dir:   dir,
		Tests: false,
		ParseFile: func(fset *token.FileSet, filename string, data []byte) (*ast.File, error) {
			// Check if there are any return or error statements in the file
			if !bytes.Contains(data, []byte("return")) || !bytes.Contains(data, []byte("error")) {
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
		return Generator{}, err
	}

	if cnt := packages.PrintErrors(pkgs); cnt > 0 {
		return Generator{}, fmt.Errorf("failed to load %d packages", cnt)
	}

	if len(pkgs) == 0 {
		return Generator{}, fmt.Errorf("no packages found in %s", dir)
	}

	return Generator{
		pkgs: pkgs,
		opts: options,
	}, nil
}

func (g *Generator) ParseErrs() error {
	for _, pkg := range g.pkgs {

		g.filterPackageDecls(pkg)
		debugPrint(pkg, nil, "filtered package decls")

		// The remaining declarations are now only function declarations that return an error
		for _, stxFile := range pkg.Syntax {
			filename := getFilename(pkg, stxFile.FileStart)

			// The content of this file will most likely be changed,
			// read it first
			originalContent, err := os.ReadFile(filename)
			if err != nil {
				return errors.New(makeErrorMsgf(pkg, stxFile, "failed to read: %w", err))
			}

			for stxFileDeclIdx, d := range stxFile.Decls {
				funcDecl, ok := d.(*ast.FuncDecl)
				if !ok {
					// It's a bug!
					return fmt.Errorf("%s: expected a function declaration, found: %T %+v", filename, d, d)
				}

				if funcDecl.Body == nil {
					// Shouldn't happen
					return fmt.Errorf("%s: function declaration has no body: %s", filename, funcDecl.Name)
				}

				err := g.parseAndUpdateFunction(pkg, funcDecl, originalContent)
				if err != nil {
					return fmt.Errorf("failed to update function: %w", err)
				}

				// Update with the new function content
				stxFile.Decls[stxFileDeclIdx] = funcDecl
			}
		}
	}
	return nil
}
func (g *Generator) parseAndUpdateFunction(pkg *packages.Package, funcDecl *ast.FuncDecl, originalContent []byte) error {
	// Find which ret param is an error
	retErrIdx := -1
	for i, res := range funcDecl.Type.Results.List {
		resStr, ok := res.Type.(fmt.Stringer)
		if !ok {
			continue
		}
		if resStr.String() == "error" {
			retErrIdx = i
			continue
		}
	}
	if retErrIdx == -1 {
		// Error is not in the returned values
		return nil
	}

	for bodyIdx, stmt := range funcDecl.Body.List {
		inspectErrs := make([]error, 0)
		ast.Inspect(stmt, func(n ast.Node) bool {
			// Find only the return statements
			returnStmt, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}

			if len(returnStmt.Results) != funcDecl.Type.Results.NumFields() {
				// There are 2 reasons for it:
				// - just a return keyword is given with no params
				// - the returned value is a function call
				// We will ignore both of these cases.
				debugPrint(pkg, returnStmt, "%s: unexpected number of returned values: %v/%v", funcDecl.Name, len(returnStmt.Results), funcDecl.Type.Results.NumFields())
				return false
			}

			retParam := returnStmt.Results[retErrIdx]
			retIdent, ok := retParam.(*ast.Ident)
			if ok {
				if retIdent.Name == "nil" {
					// Ignore
					return false
				}
				debugPrint(pkg, retParam, "--- ret ident %s ", retIdent.Name)
			}

			// Read the retParam value
			fposStart := pkg.Fset.Position(retParam.Pos())
			fposEnd := pkg.Fset.Position(retParam.End())
			errorContent := string(originalContent[fposStart.Offset:fposEnd.Offset])
			debugPrint(pkg, retParam, "--- ret %+v", errorContent)

			// Now wrap the error in the wrapper like:
			// errnums.New(ERR_NUM_PLACEHOLDER, errors.New("original error"))
			newErrorContent := fmt.Sprintf("%s.New(ERR_NUM_PLACEHOLDER, %s)", g.opts.OutPackageName, errorContent)
			debugPrint(pkg, retParam, "--- replaced with %s", newErrorContent)
			newRetParam, err := parser.ParseExpr(newErrorContent)
			if err != nil {
				// It's a bug!
				inspectErrs = append(inspectErrs, errors.New(makeErrorMsgf(pkg, retParam, "failed to parse modified statement: %+v\n%+v", err, newErrorContent)))
				return false
			}

			// Override the definition
			returnStmt.Results[retErrIdx] = newRetParam
			funcDecl.Body.List[bodyIdx] = returnStmt
			return false
		})
	}

	return nil
}

func debugPrint(pkg *packages.Package, node ast.Node, message string, args ...any) {
	msg := makeErrorMsgf(pkg, node, message, args...)
	fmt.Print(msg)
}

func makeErrorMsgf(pkg *packages.Package, node ast.Node, message string, args ...any) string {
	if pkg == nil {
		return fmt.Sprintf(message, args...)
	}

	if node == nil {
		// Include just the package name
		msg := fmt.Sprintf("package %s: %s\n", pkg.Name, message)
		return fmt.Sprintf(msg, args)
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

func (g *Generator) filterPackageDecls(pkg *packages.Package) error {
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
				// All types should implement the stringer interface
				tp, ok := ret.Type.(fmt.Stringer)
				if !ok {
					// Errors will always implement the Stringer interface
					// debugPrint("%v: return type is not a stringer: %s", getLineNum(pkg, ret), ret.Type)
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
		debugPrint(pkg, stxFile, "num decls: %d", len(stxFile.Decls))

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

func (g *Generator) Generate(output string) error {
	return nil
}

func getFilename(pkg *packages.Package, position token.Pos) string {
	tokenFile := pkg.Fset.File(position)
	filename := tokenFile.Name()
	return filename
}
