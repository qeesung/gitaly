package smarthttp

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestSuccessfulScanDeepen(t *testing.T) {
	examples := []struct {
		input  string
		output bool
	}{
		{"000dsomething000cdeepen 10000", true},
		{"000dsomething0000000cdeepen 1", true},
		{"000dsomething0000000cdeepen 1" + strings.Repeat("garbage", 1000000), true},
		{"000dsomething0000", false},
	}

	for _, example := range examples {
		desc := fmt.Sprintf(".30s", example.input) // guard against printing very long input
		reader := bytes.NewReader([]byte(example.input))
		hasDeepen := scanDeepen(reader)
		if n := reader.Len(); n != 0 {
			t.Fatalf("scanDeepen %q: expected reader to be drained, found %d bytes left", desc, n)
		}

		if hasDeepen != example.output {
			t.Fatalf("scanDeepen %q: expected %v, got %v", desc, example.output, hasDeepen)
		}
	}
}

func TestFailedScanDeepen(t *testing.T) {
	examples := []string{
		"invalid data",
		"deepen",
		"000cdeepen",
	}

	for _, example := range examples {
		if scanDeepen(bytes.NewReader([]byte(example))) == true {
			t.Fatalf("scanDeepen %q: expected result to be false, got true", example)
		}
	}
}
