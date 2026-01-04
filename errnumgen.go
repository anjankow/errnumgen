package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/anjankow/errnumgen/pkg/generator"
	"github.com/anjankow/errnumgen/pkg/parser"
)

var (
	outputPackage = flag.String("out-pkg", "errnums", "Output package; defaults to errnums")
	outputFile    = flag.String("out-file", "", "Output file name; defaults to <input-dir>/<output-package>/errnums.go")
	skipPaths     = flag.String("skip", "", "Comma separated list of files or directories to skip")
	dryRun        = flag.Bool("dry", false, "Dry run - print the changes to be made to stdout")
	backup        = flag.Bool("bkp", true, "Backup the source files before overwriting; used only if dry-run is set to false")
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("errnumgen: ")
	flag.Parse()
	logFlags := "flags: "
	flag.VisitAll(func(f *flag.Flag) {
		logFlags = fmt.Sprintf("%s %s=%q", logFlags, f.Name, f.Value)
	})
	log.Default().Println(logFlags)

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
	if *outputPackage != "" {
		gopts.OutPackageName = *outputPackage
	}
	if *outputFile != "" {
		gopts.OutPath = *outputFile
	} else {
		// Set to the default if not given
		gopts.OutPath = filepath.Join(dir, gopts.OutPackageName, "errnums.go")
	}

	// Initialize the errnum generator
	g, err := generator.New(gopts)
	if err != nil {
		return err
	}

	popts := parser.GetDefaultOptions()
	// Use the generator's callback to process the error params
	popts.RetParamParser = g.ParseRetParam
	popts.SkipPaths = []string{gopts.OutPath}
	for p := range strings.SplitSeq(*skipPaths, ",") {
		popts.SkipPaths = append(popts.SkipPaths, p)
	}
	// Initialize the parser
	p, err := parser.New(dir, popts)
	if err != nil {
		return err
	}

	// Now parse the packages
	parsed, err := p.Parse()
	if err != nil {
		return err
	}

	// And generate the output
	updated, outputFilename, err := g.Generate(parsed)
	if err != nil {
		return err
	}

	log.Default().Println("output file: ", outputFilename)
	log.Default().Println("num of updated files: ", len(updated)-1)

	if *dryRun {
		fmt.Println("=== OUTPUT FILE ===")
		fmt.Println(updated[outputFilename])
		delete(updated, outputFilename)
		fmt.Println()

		fmt.Println("=== SOURCE FILES ===")
		for file, content := range updated {
			fmt.Println("---> ", file)
			fmt.Println(content)
			fmt.Println()
		}
		return nil
	}

	// Write the files to the disk
	outputFileContent := updated[outputFilename]
	if err := os.MkdirAll(filepath.Dir(outputFilename), 0775); err != nil {
		return fmt.Errorf("failed to create output directory %q: %w", outputFilename, err)
	}
	if err := os.WriteFile(outputFilename, []byte(outputFileContent), 0664); err != nil {
		return fmt.Errorf("failed to write the output file %q: %w", outputFilename, err)
	}
	delete(updated, outputFilename)

	for filename, content := range updated {
		st, err := os.Stat(filename)
		if err != nil {
			return fmt.Errorf("failed to stat the source file %q: %w", filename, err)
		}
		fileMode := st.Mode()
		if *backup {
			currentContent, err := os.ReadFile(filename)
			if err != nil {
				return fmt.Errorf("failed to read source file %q: %w", filename, err)
			}
			backupName := filename + ".bkp"
			if err := os.WriteFile(backupName, currentContent, fileMode); err != nil {
				return fmt.Errorf("failed to write source file backup %q: %w", backupName, err)
			}
		}
		if err := os.WriteFile(filename, []byte(content), fileMode); err != nil {
			return fmt.Errorf("failed to write source file %q: %w", filename, err)
		}
	}

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
