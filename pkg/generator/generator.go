package generator

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"golang.org/x/tools/go/packages"
)

// Generator holds the state of the analysis. Primarily used to buffer
// the output for format.Source.
type Generator struct {
	buf  bytes.Buffer // Accumulated output.
	pkgs []*packages.Package
}

// New analyses the package at the given directory and returns a new generator for this package
func New(dir string) (Generator, error) {
	cfg := &packages.Config{
		Mode:  packages.NeedSyntax | packages.NeedFiles | packages.NeedName,
		Dir:   dir,
		Tests: false,
		ParseFile: func(fset *token.FileSet, filename string, data []byte) (*ast.File, error) {
			// Check if there are any return statements in the file
			if !bytes.Contains(data, []byte("return")) {
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

func (g *Generator) ParseErrs() error {
	for _, pkg := range g.pkgs {

		_, debugPrintln := debugPrint(pkg.PkgPath)

		g.filterPackageDecls(pkg)
		debugPrintln("filtered package decls")

		// The remaining declarations are now only function declarations that return an error
		for _, stxFile := range pkg.Syntax {
			filename := getFilename(pkg, stxFile)
			debugPrintf, _ := debugPrint(filename)

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

				for _, stmt := range funcDecl.Body.List {
					// Find only the return statements
					returnStmt, ok := stmt.(*ast.ReturnStmt)
					if !ok {
						continue
					}

					if len(returnStmt.Results) == 0 {
						return fmt.Errorf("%s: return statement has no results: %s", filename, funcDecl.Name)
					}

					for _, ret := range returnStmt.Results {
						debugPrintf("--- ret %+v", ret)
					}
				}
			}

		}
	}

	return nil
}

func (g *Generator) filterPackageDecls(pkg *packages.Package) {
	for _, stxFile := range pkg.Syntax {
		filename := getFilename(pkg, stxFile)
		debugPrintf, debugPrintln := debugPrint(filename)

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
					debugPrintf("return type is not a stringer: %s", ret.Type)
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
			debugPrintln("no errors ")
			tokenFile := pkg.Fset.File(stxFile.FileStart)
			pkg.Fset.RemoveFile(tokenFile)
		}
	}
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
		fmt.Printf(fmt.Sprintf("%s: %s\n", filename, s), a...)
	}
	println := func(s string, a ...any) {
		args := []any{filename, s}
		args = append(args, a...)
		fmt.Println(args...)
	}
	return printf, println
}
