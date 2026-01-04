package errparser_test

import (
	"go/ast"
	"io"
	"log"
	"os"
	"path"
	"testing"

	"github.com/anjankow/errnumgen/pkg/errparser"
	"golang.org/x/tools/go/packages"
)

func TestParserReturnsOnlyErrorNodes(t *testing.T) {
	log.SetOutput(io.Discard)

	opts := errparser.GetDefaultOptions()
	p, err := errparser.New(path.Join("./testdata/", t.Name()), opts)
	if err != nil {
		t.Fatalf("failed to initialize a new parser: %v", err)
	}
	parsed, err := p.Parse()
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(parsed) > 1 {
		t.Fatalf("expected one parsed package, got: %d", len(parsed))
	}

	// bytealg.go returns no errors
	// filepathlite.go returns one error
	var errNode ast.Node
	for _, nodes := range parsed {
		if len(nodes) != 1 {
			t.Fatalf("invalid number of nodes, expected 1, got: %d", len(nodes))
		}
		errNode = nodes[0]
	}

	// The node should be "errInvalidPath"
	errIdent, ok := errNode.(*ast.Ident)
	if !ok || errIdent.Name != "errInvalidPath" {
		t.Errorf("invalid node found, expected %q, found %q", "errInvalidPath", errNode)
	}
}

func TestParserFindsAllErrorNodes(t *testing.T) {
	log.SetOutput(io.Discard)

	opts := errparser.GetDefaultOptions()
	p, err := errparser.New(path.Join("./testdata/", t.Name()), opts)
	if err != nil {
		t.Fatalf("failed to initialize a new parser: %v", err)
	}
	parsed, err := p.Parse()
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(parsed) > 1 {
		t.Fatalf("expected one parsed package, got: %d", len(parsed))
	}

	var pkg *packages.Package
	var errNodes []ast.Node
	for p, nodes := range parsed {
		pkg = p
		errNodes = nodes
	}
	expNodesLen := 22
	if len(errNodes) != expNodesLen {
		t.Fatalf("invalid number of found error nodes, expected %d, found %d", expNodesLen, len(errNodes))
	}

	// Read the test file content
	content, err := os.ReadFile(path.Join("./testdata/", t.Name(), "proto.go"))
	if err != nil {
		t.Fatalf("failed to read the test file: %v", err)
	}

	// Now read the found nodes
	errNodesContent := make([]string, 0, len(errNodes))
	for _, n := range errNodes {
		fposStart := pkg.Fset.Position(n.Pos())
		fposEnd := pkg.Fset.Position(n.End())
		errNodesContent = append(errNodesContent,
			string(content[fposStart.Offset:fposEnd.Offset]))

	}

	// Assert that the following nodes are present in the list:
	expNodesSet := []string{
		`err`,
		`errors.New("not enough data")`,
		`errors.New("type mismatch")`,
		`errors.New("too much data")`,
		`errors.New("bad varint")`,
		`fmt.Errorf("unknown wire type: %d", b.typ)`,
		`decodeMessage(&b, m)`,
	}
	foundNodes := make(map[string]bool, len(expNodesSet))

	for _, node := range errNodesContent {
		foundNodes[node] = true
	}

	if len(foundNodes) > len(expNodesSet) {
		var unexpectedNodes []string
		for n := range foundNodes {
			expected := false
			for _, e := range expNodesSet {
				if n == e {
					expected = true
					break
				}
			}
			if !expected {
				unexpectedNodes = append(unexpectedNodes, n)
			}
		}
		t.Errorf("found unexpected nodes: %v", unexpectedNodes)
	}

	for _, e := range expNodesSet {
		_, ok := foundNodes[e]
		if !ok {
			t.Errorf("not found expected node: %s", e)
		}
	}
}
