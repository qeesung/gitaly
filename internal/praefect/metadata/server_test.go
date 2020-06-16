package metadata

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"google.golang.org/grpc/peer"
)

func tcpPeer(t *testing.T, ip string, port int) *peer.Peer {
	parsedAddress := net.ParseIP(ip)
	require.NotNil(t, parsedAddress)

	return &peer.Peer{
		Addr: &net.TCPAddr{
			IP:   parsedAddress,
			Port: port,
		},
	}
}

func unixPeer(t *testing.T, socket string) *peer.Peer {
	return &peer.Peer{
		Addr: &net.UnixAddr{
			Name: socket,
		},
	}
}

func TestPraefect_InjectMetadata(t *testing.T) {
	testcases := []struct {
		desc            string
		listenAddress   string
		socketPath      string
		peer            *peer.Peer
		expectedAddress string
	}{
		{
			desc:            "wildcard listen address",
			listenAddress:   "0.0.0.0:1234",
			peer:            tcpPeer(t, "1.2.3.4", 4321),
			expectedAddress: "tcp://1.2.3.4:1234",
		},
		{
			desc:            "explicit listen address",
			listenAddress:   "127.0.0.1:1234",
			peer:            tcpPeer(t, "1.2.3.4", 4321),
			expectedAddress: "tcp://1.2.3.4:1234",
		},
		{
			desc:            "explicit listen address with explicit prefix",
			listenAddress:   "tcp://127.0.0.1:1234",
			peer:            tcpPeer(t, "1.2.3.4", 4321),
			expectedAddress: "tcp://1.2.3.4:1234",
		},
		{
			desc:            "named host listen address",
			listenAddress:   "example.com:1234",
			peer:            tcpPeer(t, "1.2.3.4", 4321),
			expectedAddress: "tcp://1.2.3.4:1234",
		},
		{
			desc:            "named host listen address with IPv6 peer",
			listenAddress:   "example.com:1234",
			peer:            tcpPeer(t, "2001:1db8:ac10:fe01::", 4321),
			expectedAddress: "tcp://[2001:1db8:ac10:fe01::]:1234",
		},
		{
			desc:            "Unix socket path",
			socketPath:      "/tmp/socket",
			peer:            unixPeer(t, "@"),
			expectedAddress: "unix:///tmp/socket",
		},
		{
			desc:            "Unix socket path with explicit prefix",
			socketPath:      "unix:///tmp/socket",
			peer:            unixPeer(t, "@"),
			expectedAddress: "unix:///tmp/socket",
		},
		{
			desc:            "both addresses configured with TCP peer",
			listenAddress:   "0.0.0.0:1234",
			socketPath:      "/tmp/socket",
			peer:            tcpPeer(t, "1.2.3.4", 4321),
			expectedAddress: "tcp://1.2.3.4:1234",
		},
		{
			desc:            "both addresses configured with Unix peer",
			listenAddress:   "0.0.0.0:1234",
			socketPath:      "/tmp/socket",
			peer:            unixPeer(t, "@"),
			expectedAddress: "unix:///tmp/socket",
		},
		{
			desc:            "listen address with Unix peer",
			listenAddress:   "0.0.0.0:1234",
			peer:            unixPeer(t, "@"),
			expectedAddress: "",
		},
		{
			desc:            "socket path with TCP peer",
			socketPath:      "/tmp/socket",
			peer:            tcpPeer(t, "1.2.3.4", 4321),
			expectedAddress: "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			cfg := config.Config{
				ListenAddr: tc.listenAddress,
				SocketPath: tc.socketPath,
			}

			ctx = peer.NewContext(ctx, tc.peer)
			ctx, err := InjectPraefectServer(ctx, cfg)
			require.NoError(t, err)

			server, err := PraefectFromContext(ctx)
			if tc.expectedAddress == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				address, err := server.Address()
				require.NoError(t, err)
				require.Equal(t, tc.expectedAddress, address)
			}
		})
	}
}
