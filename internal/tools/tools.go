// Package tools exists purely to ensure the package manager doesn't prune the
// CI tools from our vendor folder. This package is not meant for importing.
package tools

import (
	_ "github.com/kardianos/govendor"
	_ "github.com/wadey/gocovmerge"
	_ "golang.org/x/lint/golint"
	_ "golang.org/x/tools/cmd/goimports"
	_ "honnef.co/go/tools/cmd/staticcheck"
)

func init() {
	panic("the tools package should never be imported by another package")
}
