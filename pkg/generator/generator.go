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
		fmt.Println("PACKAGE NAME: ", pkg.Name)

		for _, stxFile := range pkg.Syntax {
			tokenFile := pkg.Fset.File(stxFile.FileStart)
			filename := tokenFile.Name()

			if stxFile.Decls == nil {
				fmt.Println(filename, "  --> No decls")
			}

			shouldKeep := ast.FilterFile(stxFile, func(decl string) bool {
				fmt.Println(filename, " -- decl: ", decl)
				return true
			})

			if !shouldKeep {
				fmt.Println(filename, " REMOVE ")
				pkg.Fset.RemoveFile(tokenFile)
			}
		}
	}

	return nil
}

func (g *Generator) Generate(output string) error {
	return nil
}
