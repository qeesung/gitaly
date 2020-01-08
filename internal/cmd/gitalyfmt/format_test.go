package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormat(t *testing.T) {
	testCases := []struct {
		desc      string
		in        string
		out       string
		unchanged bool
		fail      bool
	}{
		{
			desc: "empty lines inside braces",
			in: `package main

import "log"

func main() {

	log.Print("hello")

}
`,
			out: `package main

import "log"

func main() {
	log.Print("hello")
}
`,
		},
		{
			desc: "missing empty line after top-level closing brace",
			in: `package main

import "log"

func main() {
	log.Print("hello")
}
func foo() { log.Print("foobar") }
`,
			out: `package main

import "log"

func main() {
	log.Print("hello")
}

func foo() { log.Print("foobar") }
`,
		},
		{
			desc: "allow skipping empty line when not at top level",
			in: `package main

import "log"

func main() {
	if true {
		log.Print("hello")
	}
	if false {
		log.Print("world")
	}
}
`,
			unchanged: true,
		},
		{
			desc: "allow skipping empty line between one-line functions",
			in: `package main

import "log"

func foo() { log.Print("world") }
func bar() { log.Print("hello") }

func main() {
	foo()
	bar()
}
`,
			unchanged: true,
		},
		{
			desc: "allow }{ at start of line",
			in: `package main

var anonymousStruct = struct {
	foo string
}{
	foo: "bar",
}
`,
			unchanged: true,
		},
		{
			desc: "allow trailing */ before closing brace",
			in: `package main

func foo() {
	return

	/* trailing comment
	*/
}
`,
			unchanged: true,
		},
		{
			desc: "empty lines between non-stdlib imports",
			in: `package main

import (
	"net/http"

	"example.com/foo"

	bar "example.com/bar"
)

func main() {}
`,
			out: `package main

import (
	"net/http"

	"example.com/foo"
	bar "example.com/bar"
)

func main() {}
`,
		},
		{
			desc: "alternating stdlib and non stdlib",
			in: `package main

import (
	"net/http"

	"example.com/foo"

	"io"
)

func main() {}
`,
			fail: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			out, err := format([]byte(tc.in))

			if tc.fail {
				require.Error(t, err, "expect format error")
				return
			}

			require.NoError(t, err, "format error")

			if tc.unchanged {
				require.Equal(t, tc.in, string(out))
			} else {
				require.Equal(t, tc.out, string(out))
			}
		})
	}
}
