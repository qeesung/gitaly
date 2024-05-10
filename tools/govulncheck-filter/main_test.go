package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilteredVulncheck(t *testing.T) {
	testLog, err := os.ReadFile(filepath.Join("testdata", "out.log"))
	testLogContent := string(testLog) + outputPrologue

	require.NoError(t, err)

	for _, tc := range []struct {
		Name         string
		Ignored      ignoreList
		ExpectedExit int
	}{
		{
			Name: "when the ignore list covers the actual vulns entirely",
			Ignored: ignoreList{
				"GO-2024-2824": {},
				"GO-2024-2687": {},
			},
			ExpectedExit: 0,
		},
		{
			Name: "when the ignore list covers the actual vulns partially",
			Ignored: ignoreList{
				"GO-2024-2824": {},
			},
			ExpectedExit: 1,
		},
		{
			Name:         "when the ignore list is empty",
			ExpectedExit: 1,
		},
		{
			Name: "when the ignore list contains informational vulns",
			Ignored: ignoreList{
				"GO-2023-1989": {},
				"GO-2022-0646": {},
			},
			ExpectedExit: 1,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			testLog, err := os.Open(filepath.Join("testdata", "out.log"))
			require.NoError(t, err)

			var buf bytes.Buffer

			actualExit, err := FilteredVulncheck(testLog, &buf, tc.Ignored)
			require.NoError(t, err)

			require.Equal(t, tc.ExpectedExit, actualExit)

			// Assert the ignore list separately since map order isn't guaranteed.
			require.Contains(t, buf.String(), testLogContent)
			for id := range tc.Ignored {
				require.Contains(t, buf.String(), fmt.Sprintf("- %s\n", id))
			}
		})
	}
}
