package generator

import (
	// blank import to allow the usage of go:embed
	_ "embed"
)

var (
	//go:embed errnums.tmpl
	outputFileTemplate []byte
)
