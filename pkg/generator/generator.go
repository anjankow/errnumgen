package generator

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Generator holds the state of the analysis. Primarily used to buffer
// the output for format.Source.
type Generator struct {
	pkgs     []*packages.Package
	opts     GenOptions
	readFile ReadFileFunc

	// errsToEdit holds all errors that have to be edited.
	// index in the first slice corresponds to the package index;
	// index in the second slice corresponds to the statement order
	errsToEdit [][]ast.Node

	outPkg *packages.Package
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

// New analyses the package at the given directory and returns a new generator for this package
func New(dir string, options GenOptions) (Generator, error) {
	// To load all project files
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

	// Get the absolute paths of the input and output dirs
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return Generator{}, fmt.Errorf("invalid input dir %s, can't create an absolute path: %w", dir, err)
	}

	outPathAbs, err := filepath.Abs(options.OutPath)
	if err != nil {
		return Generator{}, fmt.Errorf("invalid output path %s, can't create an absolute path: %w", options.OutPath, err)
	}
	if outPathAbs == "." {
		return Generator{}, fmt.Errorf("invalid output file path %s", options.OutPath)
	}
	outDirAbs := path.Dir(outPathAbs)

	var outPkg *packages.Package
	// Now, if the output file will be generated outside of the input directory,
	// load the output package too (if exists)
	if !strings.Contains(outDirAbs, dirAbs) {
		cfg := &packages.Config{
			Mode:  packages.NeedSyntax | packages.NeedFiles | packages.NeedName,
			Dir:   outDirAbs,
			Tests: false,
			ParseFile: func(fset *token.FileSet, filename string, data []byte) (*ast.File, error) {
				if filename != path.Base(outPathAbs) {
					return nil, nil
				}
				const mode = parser.AllErrors | parser.SkipObjectResolution
				return parser.ParseFile(fset, filename, data, mode)
			},
		}

		// Load just the package from the out directory
		const patterns = "."
		pkgs, err := packages.Load(cfg, patterns)
		if err != nil {
			return Generator{}, fmt.Errorf("failed to load the package from out directory: %w", err)
		}

		if cnt := packages.PrintErrors(pkgs); cnt > 0 {
			return Generator{}, fmt.Errorf("failed to load %d out packages", cnt)
		}

		if len(pkgs) == 1 {
			outPkg = pkgs[0]
		} else if len(pkgs) != 0 {
			return Generator{}, fmt.Errorf("invalid number of found packages in out directory: %v, expected 1", len(pkgs))
		}
	} else {
		// The out package is within the loaded packages, found it.
		// It can be nil in case if the file hasn't been generated yet.
		for _, pkg := range pkgs {
			if slices.Contains(pkg.GoFiles, outPathAbs) {
				outPkg = pkg
				break
			}
		}
	}

	if outPkg == nil {
		log.Println("Output file not found, a new one will be generated")
	}

	return Generator{
		pkgs:       pkgs,
		opts:       options,
		errsToEdit: make([][]ast.Node, len(pkgs)),
		outPkg:     outPkg,
		readFile:   options.Reader,
	}, nil
}

func (g *Generator) ParseErrs() error {
	for pkgIdx, pkg := range g.pkgs {

		g.filterPackageDecls(pkg)

		// The remaining declarations are now only function declarations that return an error
		for _, stxFile := range pkg.Syntax {
			filename := getFilename(pkg, stxFile.FileStart)

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

				err := g.parseFunction(pkg, pkgIdx, funcDecl, funcDecl.Type, funcDecl.Body)
				if err != nil {
					return fmt.Errorf("%s: failed to update function: %w", filename, err)
				}
			}
		}
	}
	return nil
}
func (g *Generator) parseFunction(pkg *packages.Package, pkgIdx int, funcDecl ast.Node, funcType *ast.FuncType, funcBody *ast.BlockStmt) error {

	retErrIdx := g.findResultParamIdx(pkg, funcType)
	if retErrIdx == -1 {
		// Error is not in the returned values
		return nil
	}
	inspectErrs := make([]error, 0)
	for _, stmt := range funcBody.List {
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncLit:
				// Parsing an annonymous function
				if err := g.parseFunction(pkg, pkgIdx, node, node.Type, node.Body); err != nil {
					inspectErrs = append(inspectErrs, err)
					return false
				}
				return true
			case *ast.ReturnStmt:
				g.parseResultParams(pkg, pkgIdx, node, retErrIdx, funcType.Results.NumFields())
				return false
			default:
				return true
			}
		})
	}

	return errors.Join(inspectErrs...)
}

// findResultParamIdx returns -1 if error in not found among returned params
func (g Generator) findResultParamIdx(pkg *packages.Package, funcType *ast.FuncType) int {
	// Find which ret param is an error
	retErrIdx := -1
	paramCnt := 0
	// debugPrint(pkg, funcType, "%d %d %+v", funcType.Results.NumFields(), len(funcType.Results.List), funcType.Results.List[0].Names)
	for _, res := range funcType.Results.List {
		// The returned error is of the ast.Ident type
		resType, ok := res.Type.(*ast.Ident)
		if ok && resType.Name == "error" {
			retErrIdx = paramCnt
			break
		}

		// If a function returns multiple times the same type and it's named,
		// the funcDecl.Type.Results.List will track it as just one result with multiple underlying Names;
		// e.g. `s1 string, s2 string, err error` will be represented as 2 List Results
		// where the first one has two names: s1 and s2.
		if len(res.Names) > 0 {
			paramCnt += len(res.Names)
		} else {
			paramCnt++
		}
	}
	return retErrIdx
}

func (g *Generator) parseResultParams(pkg *packages.Package, pkgIdx int, returnStmt *ast.ReturnStmt, retErrIdx int, retNumFields int) error {

	if len(returnStmt.Results) != retNumFields {
		// There are 2 reasons for it:
		// - just a return keyword is given with no params
		// - the returned value is a function call
		// We will ignore both of these cases.
		debugPrint(pkg, returnStmt, "unexpected number of returned values: %v/%v", len(returnStmt.Results), retNumFields)
		return nil
	}

	retParam := returnStmt.Results[retErrIdx]
	retIdent, ok := retParam.(*ast.Ident)
	if ok {
		if retIdent.Name == "nil" {
			// Ignore
			return nil
		}
		// debugPrint(pkg, retParam, "--- ret ident %s ", retIdent.Name)
	}

	// If an error wrapper has already been generated, we want to keep it
	retCallStmt, ok := retParam.(*ast.CallExpr)
	if ok {
		// Read the function name from the selector expr
		selExpr, selOK := retCallStmt.Fun.(*ast.SelectorExpr)
		if selOK && selExpr.Sel.Name == "New" {
			// Identifier object holds the package name
			ident, identOK := selExpr.X.(*ast.Ident)
			if identOK && ident.Name == g.opts.OutPackageName {
				// Skip - already generated
				return nil
			}
		}
	}

	// Add to the found errors
	g.errsToEdit[pkgIdx] = append(g.errsToEdit[pkgIdx], retParam)
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
				// Errors will always implement the Stringer interface
				tp, ok := ret.Type.(fmt.Stringer)
				if !ok {
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
		// debugPrint(pkg, stxFile, "num decls: %d", len(stxFile.Decls))

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

func (g *Generator) Generate() (fileContents map[string]string, err error) {

	//
	fileContents = make(map[string]string, len(g.errsToEdit))

	var errs []error
	for pkgIdx, pkg := range g.pkgs {
		errNodes := g.errsToEdit[pkgIdx]

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

			// Now wrap the error in the wrapper like:
			// errnums.New(ERR_NUM_PLACEHOLDER, errors.New("original error"))
			newErrorContent := fmt.Sprintf("%s.New(ERR_NUM_PLACEHOLDER, %s)", g.opts.OutPackageName, errorContent)
			debugPrint(pkg, errNode, "--- replaced with %s", newErrorContent)
			_, err := parser.ParseExpr(newErrorContent)
			if err != nil {
				// It's a bug!
				return nil, errors.New(makeErrorMsgf(pkg, errNode, "failed to parse modified statement: %+v\n%+v", err, newErrorContent))
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
	}

	return fileContents, errors.Join(errs...)
}

func getFilename(pkg *packages.Package, position token.Pos) string {
	tokenFile := pkg.Fset.File(position)
	filename := tokenFile.Name()
	return filename
}
