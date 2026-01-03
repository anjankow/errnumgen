package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/anjankow/errnumgen/pkg/generator"
	"github.com/anjankow/errnumgen/pkg/parser"
)

var (
	output = flag.String("output", "errnums.go", "output file name; default errnums.go")
	dryRun = flag.Bool("dry-run", false, "dry run - print the changes to be made to stdout")
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("errnumgen: ")
	flag.Parse()

	args := flag.Args()
	dir := "."
	if len(args) > 0 {
		if !isDirectory(args[0]) {
			log.Fatalf("%q is not a directory", args[0])
		}
		dir = args[0]
	}

	// User input parsed and validated, start the generation
	if err := run(dir); err != nil {
		log.Fatal(err)
	}
}

func run(dir string) error {
	gopts := generator.GetDefaultGenOptions()
	gopts.OutPath = filepath.Join(dir, gopts.OutPackageName, "errnums.go")

	g, err := generator.New(gopts)
	if err != nil {
		return err
	}

	popts := parser.GetDefaultOptions()
	popts.RetParamParser = g.ParseRetParam
	popts.SkipPaths = []string{gopts.OutPath}
	p, err := parser.New(dir, popts)
	if err != nil {
		return err
	}

	parsed, err := p.Parse()
	if err != nil {
		return err
	}

	updated, outputFilename, err := g.Generate(parsed)
	if err != nil {
		return err
	}

	fmt.Println(updated[outputFilename])
	// for file, content := range updated {
	// 	fmt.Println(file)
	// 	fmt.Println(content)
	// 	fmt.Println()
	// }
	fmt.Println("num of updated files: ", len(updated))

	return nil
}

// isDirectory reports whether the named file is a directory.
func isDirectory(name string) bool {
	info, err := os.Stat(name)
	if err != nil {
		log.Fatal(err)
	}
	return info.IsDir()
}
