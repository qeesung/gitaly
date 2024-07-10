package praefect

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

type stubFlag struct{}

func (stubFlag) String() string { return "it is a stub" }

func (stubFlag) Apply(*flag.FlagSet) error { return nil }

func (stubFlag) Names() []string { return nil }

func (stubFlag) IsSet() bool { return false }

func TestGetStarterConfigs(t *testing.T) {
	for _, tc := range []struct {
		desc   string
		conf   config.Config
		exp    []starter.Config
		expErr error
	}{
		{
			desc:   "no addresses",
			expErr: errors.New("no listening addresses were provided, unable to start"),
		},
		{
			desc: "addresses without schema",
			conf: config.Config{
				ListenAddr:    "127.0.0.1:2306",
				TLSListenAddr: "127.0.0.1:2307",
				SocketPath:    "/socket/path",
			},
			exp: []starter.Config{
				{
					Name:              starter.TCP,
					Addr:              "127.0.0.1:2306",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.TLS,
					Addr:              "127.0.0.1:2307",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.Unix,
					Addr:              "/socket/path",
					HandoverOnUpgrade: true,
				},
			},
		},
		{
			desc: "addresses with schema",
			conf: config.Config{
				ListenAddr:    "tcp://127.0.0.1:2306",
				TLSListenAddr: "tls://127.0.0.1:2307",
				SocketPath:    "unix:///socket/path",
			},
			exp: []starter.Config{
				{
					Name:              starter.TCP,
					Addr:              "127.0.0.1:2306",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.TLS,
					Addr:              "127.0.0.1:2307",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.Unix,
					Addr:              "/socket/path",
					HandoverOnUpgrade: true,
				},
			},
		},
		{
			desc: "addresses without schema",
			conf: config.Config{
				ListenAddr:    "127.0.0.1:2306",
				TLSListenAddr: "127.0.0.1:2307",
				SocketPath:    "/socket/path",
			},
			exp: []starter.Config{
				{
					Name:              starter.TCP,
					Addr:              "127.0.0.1:2306",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.TLS,
					Addr:              "127.0.0.1:2307",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.Unix,
					Addr:              "/socket/path",
					HandoverOnUpgrade: true,
				},
			},
		},
		{
			desc: "addresses with/without schema",
			conf: config.Config{
				ListenAddr:    "127.0.0.1:2306",
				TLSListenAddr: "tls://127.0.0.1:2307",
				SocketPath:    "unix:///socket/path",
			},
			exp: []starter.Config{
				{
					Name:              starter.TCP,
					Addr:              "127.0.0.1:2306",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.TLS,
					Addr:              "127.0.0.1:2307",
					HandoverOnUpgrade: true,
				},
				{
					Name:              starter.Unix,
					Addr:              "/socket/path",
					HandoverOnUpgrade: true,
				},
			},
		},
		{
			desc: "secure and insecure can't be the same",
			conf: config.Config{
				ListenAddr:    "127.0.0.1:2306",
				TLSListenAddr: "127.0.0.1:2306",
			},
			expErr: errors.New(`same address can't be used for different schemas "127.0.0.1:2306"`),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			actual, err := getStarterConfigs(tc.conf)
			require.Equal(t, tc.expErr, err)
			require.ElementsMatch(t, tc.exp, actual)
		})
	}
}

func writeConfigToFile(tb testing.TB, conf config.Config) string {
	tb.Helper()
	confData, err := toml.Marshal(conf)
	require.NoError(tb, err)
	tmpDir := testhelper.TempDir(tb)
	confPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(tb, os.WriteFile(confPath, confData, perm.PrivateWriteOnceFile))
	return confPath
}
