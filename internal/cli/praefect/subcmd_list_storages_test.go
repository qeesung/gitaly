package praefect

import (
	"bytes"
	"testing"

	"github.com/olekukonko/tablewriter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
)

func TestListStoragesSubcommand(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc            string
		virtualStorages []*config.VirtualStorage
		args            []string
		expectedOutput  func(*tablewriter.Table)
	}{
		{
			desc: "one virtual storage",
			virtualStorages: []*config.VirtualStorage{
				{
					Name: "vs-1",
					Nodes: []*config.Node{
						{
							Storage: "storage-1",
							Address: "tcp://1.2.3.4",
						},
						{
							Storage: "storage-2",
							Address: "tcp://4.3.2.1",
						},
					},
				},
			},
			args: []string{},
			expectedOutput: func(t *tablewriter.Table) {
				t.Append([]string{"vs-1", "storage-1", "tcp://1.2.3.4"})
				t.Append([]string{"vs-1", "storage-2", "tcp://4.3.2.1"})
			},
		},
		{
			desc: "multiple virtual storages but only show one",
			virtualStorages: []*config.VirtualStorage{
				{
					Name: "vs-1",
					Nodes: []*config.Node{
						{
							Storage: "storage-1",
							Address: "tcp://1.2.3.4",
						},
						{
							Storage: "storage-2",
							Address: "tcp://4.3.2.1",
						},
					},
				},
				{
					Name: "vs-2",
					Nodes: []*config.Node{
						{
							Storage: "storage-3",
							Address: "tcp://1.1.3.4",
						},
						{
							Storage: "storage-4",
							Address: "tcp://1.3.2.1",
						},
					},
				},
				{
					Name: "vs-3",
					Nodes: []*config.Node{
						{
							Storage: "storage-5",
							Address: "tcp://2.1.3.4",
						},
						{
							Storage: "storage-6",
							Address: "tcp://2.3.2.1",
						},
					},
				},
			},
			args: []string{"-virtual-storage", "vs-2"},
			expectedOutput: func(t *tablewriter.Table) {
				t.Append([]string{"vs-2", "storage-3", "tcp://1.1.3.4"})
				t.Append([]string{"vs-2", "storage-4", "tcp://1.3.2.1"})
			},
		},
		{
			desc: "one virtual storage with virtual storage arg",
			virtualStorages: []*config.VirtualStorage{
				{
					Name: "vs-1",
					Nodes: []*config.Node{
						{
							Storage: "storage-1",
							Address: "tcp://1.2.3.4",
						},
						{
							Storage: "storage-2",
							Address: "tcp://4.3.2.1",
						},
					},
				},
			},
			args: []string{"-virtual-storage", "vs-1"},
			expectedOutput: func(t *tablewriter.Table) {
				t.Append([]string{"vs-1", "storage-1", "tcp://1.2.3.4"})
				t.Append([]string{"vs-1", "storage-2", "tcp://4.3.2.1"})
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			var expectedOutput bytes.Buffer
			table := tablewriter.NewWriter(&expectedOutput)
			table.SetHeader([]string{"Virtual storage", "Gitaly node", "Gitaly node address"})
			table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
			table.SetAutoFormatHeaders(false)
			table.SetAlignment(tablewriter.ALIGN_LEFT)
			table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
			table.SetCenterSeparator("|")
			tc.expectedOutput(table)
			table.Render()

			conf := config.Config{
				ListenAddr:      ":0",
				VirtualStorages: tc.virtualStorages,
			}
			confPath := writeConfigToFile(t, conf)
			stdout, stderr, err := runApp(append([]string{"-config", confPath, "list-storages"}, tc.args...))
			assert.Empty(t, stderr)
			require.NoError(t, err)
			require.Equal(t, expectedOutput.String(), stdout)
		})
	}

	t.Run("negative", func(t *testing.T) {
		conf := config.Config{
			ListenAddr: ":0",
			VirtualStorages: []*config.VirtualStorage{
				{
					Name: "vs-1",
					Nodes: []*config.Node{
						{
							Storage: "storage-1",
							Address: "tcp://1.2.3.4",
						},
						{
							Storage: "storage-2",
							Address: "tcp://4.3.2.1",
						},
					},
				},
			},
		}
		confPath := writeConfigToFile(t, conf)

		t.Run("virtual storage arg matches no virtual storages", func(t *testing.T) {
			stdout, stderr, err := runApp([]string{"-config", confPath, "list-storages", "-virtual-storage", "vs-2"})
			assert.Empty(t, stderr)
			require.NoError(t, err)
			require.Equal(t, "No virtual storages named vs-2.\n", stdout)
		})

		t.Run("positional arguments", func(t *testing.T) {
			_, stderr, err := runApp([]string{"-config", confPath, "list-storages", "-virtual-storage", "vs-1", "positional-arg"})
			assert.Empty(t, stderr)
			require.Equal(t, cli.Exit(unexpectedPositionalArgsError{Command: "list-storages"}, 1), err)
		})
	})
}
