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
	originals map[string] /*filename*/ []byte
	edits     map[string] /*filename*/ Edit
}

type Edit struct {
}

// New analyses the package at the given directory and returns a new generator for this package
func New(dir string) (Generator, error) {
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
	}, nil
}

func (g *Generator) ParseErrs(continueOnError bool) error {
	for _, pkg := range g.pkgs {

		_, debugPrintln := debugPrint(pkg.PkgPath)

		g.filterPackageDecls(pkg)
		debugPrintln("filtered package decls")

		// The remaining declarations are now only function declarations that return an error
		for _, stxFile := range pkg.Syntax {
			filename := getFilename(pkg, stxFile)
			debugPrintf, _ := debugPrint(filename)

			// The content of this file will most likely be changed,
			// read it first
			originalContent, err := os.ReadFile(filename)
			if err != nil {
				return fmt.Errorf("%s: failed to read: %w", filename, err)
			}

			for _, d := range stxFile.Decls {
				funcDecl, ok := d.(*ast.FuncDecl)
				if !ok {
					// It's a bug!
					return fmt.Errorf("%s: expected a function declaration, found: %T %+v", filename, d, d)
				}

				if funcDecl.Body == nil {
					// Shouldn't happen
					return fmt.Errorf("%s: function declaration has no body: %s", filename, funcDecl.Name)
				}

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

				for _, stmt := range funcDecl.Body.List {
					// Find only the return statements
					returnStmt, ok := stmt.(*ast.ReturnStmt)
					if !ok {
						continue
					}

					if len(returnStmt.Results) != funcDecl.Type.Results.NumFields() {
						msg := fmt.Sprintf("%s: %s: return statement has no results: %s: %v/%v", filename, funcDecl.Name, len(returnStmt.Results), funcDecl.Type.Results.NumFields())
						if continueOnError {
							debugPrint(msg)
							continue
						} else {
							return errors.New(msg)
						}
					}

					retParam := returnStmt.Results[retErrIdx]
					// Read the retParam value
					fposStart := pkg.Fset.Position(retParam.Pos())
					fposEnd := pkg.Fset.Position(retParam.End())
					errorContent := string(originalContent[fposStart.Offset:fposEnd.Offset])
					linenum := pkg.Fset.File(retParam.Pos()).Line(retParam.Pos())
					debugPrintf("%v --- ret %+v", linenum, errorContent)
				}
			}

		}
	}

	return nil
}

func (g *Generator) filterPackageDecls(pkg *packages.Package) error {
	stxIdx := 0
	for _, stxFile := range pkg.Syntax {
		filename := getFilename(pkg, stxFile)
		debugPrintf, _ := debugPrint(filename)

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
					linenum := pkg.Fset.File(ret.Pos()).Line(ret.Pos())
					debugPrintf("%v: return type is not a stringer: %s", linenum, ret.Type)
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
		debugPrintf("num decls: %d", len(stxFile.Decls))

		shouldKeep := len(stxFile.Decls) > 0
		if !shouldKeep {
			// Remove from token fileset
			tokenFile := pkg.Fset.File(stxFile.FileStart)
			if tokenFile == nil {
				return fmt.Errorf("%s: failed to get token file", filename)
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

func getFilename(pkg *packages.Package, stxFile *ast.File) string {
	tokenFile := pkg.Fset.File(stxFile.FileStart)
	filename := tokenFile.Name()
	return filename
}

func debugPrint(filename string) (debugPrintf func(string, ...any), debugPrintln func(string, ...any)) {
	printf := func(s string, a ...any) {
		fmt.Printf(fmt.Sprintf("%s:%s\n", filename, s), a...)
	}
	println := func(s string, a ...any) {
		args := []any{filename, s}
		args = append(args, a...)
		fmt.Println(args...)
	}
	return printf, println
}
