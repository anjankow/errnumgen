package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/anjankow/errnumgen/pkg/generator"
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
	opts := generator.GetDefaultGenOptions()
	opts.OutPath = filepath.Join(dir, opts.OutPackageName, "errnums.go")

	g, err := generator.New(dir, opts)
	if err != nil {
		return err
	}

	if err := g.FindErrs(); err != nil {
		return err
	}

	if err := g.Generate(); err != nil {
		return err
	}

	// updated := g.GetFileContents()
	// for file, content := range updated {
	// 	fmt.Println(file)
	// 	fmt.Println(content)
	// 	fmt.Println()
	// }
	// fmt.Println("num of updated files: ", len(updated))

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
