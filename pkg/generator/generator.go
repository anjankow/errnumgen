package generator

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"golang.org/x/tools/go/packages"
)

// Generator is used to generate the output files
type Generator struct {
	opts     GenOptions
	readFile ReadFileFunc

	outPathAbs string
	// lastErrNum is the last err number, the next generated error should start from this one +1
	lastErrNum int
}

type GenOptions struct {
	OutPackageName string
	// OutPath is the path of the output file containing error enumeration
	OutPath string
	DryRun  bool
	Reader  ReadFileFunc
}

type ReadFileFunc func(filename string) ([]byte, error)

func GetDefaultGenOptions() GenOptions {
	return GenOptions{
		OutPackageName: "errnums",
		OutPath:        "./errnums/errnums.go",
		DryRun:         false,
		Reader:         os.ReadFile,
	}
}

func New(opts GenOptions) (Generator, error) {
	// Get the absolute paths
	outPathAbs, err := filepath.Abs(opts.OutPath)
	if err != nil {
		return Generator{}, fmt.Errorf("invalid output path %s, can't create an absolute path: %w", opts.OutPath, err)
	}
	if outPathAbs == "." {
		return Generator{}, fmt.Errorf("invalid output file path %s", opts.OutPath)
	}

	return Generator{
		opts:       opts,
		readFile:   os.ReadFile,
		outPathAbs: outPathAbs,
	}, nil
}

func (g *Generator) ParseRetParam(pkg *packages.Package, retParam ast.Expr) (out ast.Expr, skip bool) {
	// We won't modify anything in the resulting node, set out param right away
	out = retParam

	// Check if the wrapper has already been generated.
	// Set skip to false if anything is not as expected to generate the wrapper after parsing.
	retCallStmt, ok := retParam.(*ast.CallExpr)
	if !ok {
		return
	}
	// Read the function name from the selector expr
	selExpr, selOK := retCallStmt.Fun.(*ast.SelectorExpr)
	if !selOK || selExpr.Sel.Name != "New" {
		return
	}

	// Identifier object holds the package name
	ident, identOK := selExpr.X.(*ast.Ident)
	if !identOK || !(ident.Name == g.opts.OutPackageName) {
		return
	}

	// Already generated.
	skip = true

	// Check if the error number is not bigger than
	// the latest found.
	if len(retCallStmt.Args) != 2 {
		return
	}

	numArg := retCallStmt.Args[0]
	selExpr, selOK = numArg.(*ast.SelectorExpr)
	if !selOK {
		return
	}
	numName := selExpr.Sel.Name
	numStr, ok := strings.CutPrefix(numName, constErrPrefix)
	if !ok {
		return
	}
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return
	}
	// If the last found error is smaller than the current one,
	// assign it to the latest found
	if g.lastErrNum < num {
		g.lastErrNum = num
	}

	return
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

const constErrPrefix = "N_"

func (g *Generator) Generate(errNodesMap map[*packages.Package][]ast.Node) (fileContents map[string]string, outFilePath string, err error) {
	// Init updated file contents with the out file
	fileContents = make(map[string]string, len(errNodesMap))

	var errs []error
	for pkg, errNodes := range errNodesMap {
		// Start from the end of the slice to update the file from the end
		// maintaining the correct positions of the previous nodes
		for i := len(errNodes) - 1; i >= 0; i-- {
			errNode := errNodes[i]
			filename := getFilename(pkg, errNode.Pos())

			// Get the file content
			content, ok := fileContents[filename]
			if !ok {
				// Read it
				originalContent, err := g.readFile(filename)
				if err != nil {
					errs = append(errs, errors.New(makeErrorMsgf(pkg, errNode, "failed to read: %v", err)))
					continue
				}
				content = string(originalContent)
			}

			// Read the retParam value
			fposStart := pkg.Fset.Position(errNode.Pos())
			fposEnd := pkg.Fset.Position(errNode.End())
			errorContent := content[fposStart.Offset:fposEnd.Offset]
			debugPrint(pkg, errNode, "--- ret %+v", errorContent)

			errNum := g.lastErrNum + i + 1
			// Now wrap the error in the wrapper like:
			// errnums.New(errnums.N_12, errors.New("original error"))
			newErrorContent := fmt.Sprintf("%s.New(%s.%s%v, %s)",
				g.opts.OutPackageName, g.opts.OutPackageName, constErrPrefix, errNum, errorContent)
			debugPrint(pkg, errNode, "--- replaced with %s", newErrorContent)
			_, err := parser.ParseExpr(newErrorContent)
			if err != nil {
				// It's a bug!
				return nil, "", errors.New(makeErrorMsgf(pkg, errNode, "failed to parse modified statement: %+v\n%+v", err, newErrorContent))
			}

			file := pkg.Fset.File(errNode.Pos())
			if file == nil {
				errs = append(errs, fmt.Errorf("file not found within the original files: %s", filename))
				continue
			}

			start := file.Position(errNode.Pos())
			stop := file.Position(errNode.End())

			newContent := content[0:start.Offset] +
				newErrorContent +
				content[stop.Offset:]

				// Assign to the return map
			fileContents[filename] = newContent
		}

		g.lastErrNum += len(errNodes)

	}

	outFileContent, err := g.genOutputFile()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate the output file: %w", err)
	}
	fileContents[g.outPathAbs] = outFileContent

	return fileContents, g.outPathAbs, errors.Join(errs...)
}

func (g *Generator) genOutputFile() (string, error) {
	tmpl, err := template.New("output_file").Parse(string(outputFileTemplate))
	if err != nil {
		return "", fmt.Errorf("failed to parse output template: %w", err)
	}

	// Create "const" section
	// const N_<number> ErrNum = <number>
	var consts strings.Builder
	consts.WriteString("const (\n")

	for i := range g.lastErrNum {
		line := fmt.Sprintf("\t%s%v ErrNum = %v\n", constErrPrefix, i+1, i+1)
		consts.WriteString(line)
	}
	consts.WriteString(")")
	vars := map[string]any{
		"package_name":       g.opts.OutPackageName,
		"const_declarations": consts.String(),
	}

	var buf bytes.Buffer
	if err := tmpl.Option("missingkey=invalid").
		Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("failed to execute output template: %w", err)
	}

	return buf.String(), err
}

func getFilename(pkg *packages.Package, position token.Pos) string {
	tokenFile := pkg.Fset.File(position)
	filename := tokenFile.Name()
	return filename
}
