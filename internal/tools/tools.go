// +build tools

package main

import (
	_ "github.com/kardianos/govendor"
	_ "github.com/wadey/gocovmerge"
	_ "golang.org/x/lint/golint"
	_ "golang.org/x/tools/cmd/goimports"
	_ "honnef.co/go/tools/cmd/staticcheck"
)
