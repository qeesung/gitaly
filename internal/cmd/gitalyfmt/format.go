package main

import (
	"bytes"
	"errors"
	"go/scanner"
	"go/token"
	"regexp"
)

type editCommand int

const (
	keepLine editCommand = iota
	addLineBefore
	removeLine
)

type edit struct {
	line int
	cmd  editCommand
}

var (
	nonStdlibImportRegex = regexp.MustCompile(`^[^/]+\..*/`)
)

func format(src []byte) ([]byte, error) {
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, src, nil, scanner.ScanComments)

	var (
		edits                               []edit
		lastNonEmptyLine, lastLBraceLine    int
		lastOuterRBraceLine                 = -1
		importLine, lastNonStdlibImportLine int
		inImports                           bool
	)

	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}

		position := fset.Position(pos)
		currentLine := position.Line
		var nextEdit edit

		switch tok {
		case token.IMPORT:
			importLine = currentLine
		case token.LPAREN:
			if currentLine == importLine {
				inImports = true
			}
		case token.RPAREN:
			inImports = false
		case token.STRING:
			if inImports {
				if nonStdlibImportRegex.MatchString(lit) {
					if currentLine-lastNonStdlibImportLine > 1 && lastNonStdlibImportLine > 0 {
						nextEdit = edit{line: currentLine - 1, cmd: removeLine}
					}
					lastNonStdlibImportLine = currentLine
				} else if lastNonStdlibImportLine > 0 {
					// It would be nicer to fix this automatically, but how?
					return nil, errors.New("stdlib import after non-stdlib import is not allowed")
				}
			}
		case token.RBRACE:
			if currentLine-lastNonEmptyLine > 1 {
				// ......foo
				//
				// ...}
				nextEdit = edit{line: currentLine - 1, cmd: removeLine}
			}

			if position.Column == 1 {
				lastOuterRBraceLine = currentLine
			}

			if lastLBraceLine == currentLine {
				lastLBraceLine = 0
			}
		case token.LBRACE:
			lastLBraceLine = currentLine

			if lastOuterRBraceLine == currentLine {
				lastOuterRBraceLine = -1
			}
		default:
			if currentLine-lastOuterRBraceLine == 1 {
				// }
				// func bar() {
				nextEdit = edit{line: currentLine, cmd: addLineBefore}
			} else if currentLine-lastLBraceLine > 1 && lastNonEmptyLine == lastLBraceLine {
				// ...foo() {
				//
				// ......bar
				nextEdit = edit{line: currentLine - 1, cmd: removeLine}
			}
		}

		if nextEdit.cmd != keepLine {
			if len(edits) == 0 || edits[0] != nextEdit {
				// Store edits in reverse line order: that way line numbers in edits
				// won't become invalid when edits get applied.
				edits = append([]edit{nextEdit}, edits...)
			}
		}

		lastNonEmptyLine = currentLine
	}

	srcLines := bytes.Split(src, []byte("\n"))
	for _, e := range edits {
		i := e.line - 1 // scanner uses 1 based indexing; convert to 0 based

		switch e.cmd {
		case addLineBefore:
			srcLines = append(srcLines[:i], append([][]byte{nil}, srcLines[i:]...)...)
		case removeLine:
			if len(srcLines[i]) == 0 {
				srcLines = append(srcLines[:i], srcLines[i+1:]...)
			}
		}
	}

	return bytes.Join(srcLines, []byte("\n")), nil
}
