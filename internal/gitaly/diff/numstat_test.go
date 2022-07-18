//go:build !gitaly_test_sha256

package diff

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumStatParser(t *testing.T) {
	file, err := os.Open("testdata/z-numstat.txt")

	require.NoError(t, err)
	defer file.Close()

	var parsedStats []*NumStat

	parser := NewDiffNumStatParser(file)

	for {
		stat, err := parser.NextNumStat()
		if err == io.EOF {
			break
		}

		require.NoError(t, err)
		parsedStats = append(parsedStats, stat)
	}

	expectedStats := []NumStat{
		{
			Path:      []byte("app/controllers/graphql_controller.rb"),
			Additions: 0,
			Deletions: 15,
		},
		{
			Path:      []byte("app/models/mr.rb"),
			Additions: 0,
			Deletions: 0,
		},
		{
			Path:      []byte("image.jpg"),
			Additions: 0,
			Deletions: 0,
		},
		{
			Path:      []byte("files/autocomplete_users_finder.rb"),
			Additions: 0,
			Deletions: 0,
		},
		{
			Path:      []byte("newfile"),
			Additions: 0,
			Deletions: 0,
		},
		{
			Path:      []byte("xpto\nspace and linebreak"),
			Additions: 1,
			Deletions: 5,
		},
		{
			Path:      []byte("files/new.jpg"),
			OldPath:   []byte("files/original.jpg"),
			Additions: 0,
			Deletions: 0,
		},
	}

	require.Equal(t, len(expectedStats), len(parsedStats))

	for index, parsedStat := range parsedStats {
		expectedStat := expectedStats[index]

		require.Equal(t, expectedStat.Additions, parsedStat.Additions)
		require.Equal(t, expectedStat.Deletions, parsedStat.Deletions)
		require.Equal(t, expectedStat.Path, parsedStat.Path)
	}
}
