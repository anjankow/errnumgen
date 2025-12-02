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
		debugPrintln("PACKAGE NAME: ", pkg.Name)

		for _, stxFile := range pkg.Syntax {
			tokenFile := pkg.Fset.File(stxFile.FileStart)
			filename := tokenFile.Name()

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
						debugPrintf(filename, "return type is not a stringer: %s", ret.Type)
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
			debugPrintf(filename, "num decls: %d", len(stxFile.Decls))

			for _, d := range stxFile.Decls {
				debugPrintf(filename, "%+v", d)
			}

			shouldKeep := len(stxFile.Decls) > 0
			if !shouldKeep {
				fmt.Println(filename, "no errors ")
				pkg.Fset.RemoveFile(tokenFile)
			}
		}
	}

	return nil
}

func (g *Generator) Generate(output string) error {
	return nil
}

func debugPrintf(filename string, s string, a ...any) {
	fmt.Printf(fmt.Sprintf("%s: %s\n", filename, s), a...)
}

func debugPrintln(filename string, s string, a ...any) {
	args := []any{filename, s}
	args = append(args, a...)
	fmt.Println(args...)
}
