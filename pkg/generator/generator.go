package generator

import (
	"bytes"
	"fmt"
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
	}
	pkgs, err := packages.Load(cfg)
	if err != nil {
		return Generator{}, err
	}

	if len(pkgs) == 0 {
		return Generator{}, fmt.Errorf("no packages found in %s", dir)
	}

	return Generator{
		pkgs: pkgs,
	}, nil
}

func (g *Generator) ParseErrs() error {
	for _, p := range g.pkgs {
		fmt.Println(p.Dir)
		p.Fset.Iterate(func(f *token.File) bool {
			fmt.Println(f.Name())
			fmt.Println("line count: ", f.LineCount())
			start := f.LineStart(4)
			stop := f.LineStart(5)
			fmt.Println("start: ", start, "stop: ", stop)
			return true
		})
	}
	return nil
}

func (g *Generator) Generate(output string) error {
	return nil
}
