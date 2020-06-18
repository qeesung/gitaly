package server

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	gitaly_x509 "gitlab.com/gitlab-org/gitaly/internal/x509"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestGitalyServerFactory(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	t.Run("insecure", func(t *testing.T) {
		sf := NewGitalyServerFactory(nil)

		// start gitaly serving on public endpoint
		listener, err := net.Listen(starter.TCP, ":0")
		require.NoError(t, err)
		defer func() { require.NoError(t, listener.Close()) }()
		go sf.Serve(listener, false)

		addr, err := starter.Compose(starter.TCP, listener.Addr().String())
		require.NoError(t, err)

		cc, err := client.Dial(addr, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, cc.Close()) }()

		healthClient := healthpb.NewHealthClient(cc)

		resp, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.Status)
	})

	t.Run("secure", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("extending of system certificates implemented only for darwin")
		}

		certFile, keyFile, remove := testhelper.GenerateTestCerts(t)
		defer remove()

		defer func(old config.TLS) { config.Config.TLS = old }(config.Config.TLS)
		config.Config.TLS = config.TLS{
			CertPath: certFile,
			KeyPath:  keyFile,
		}
		defer testhelper.ModifyEnvironment(t, gitaly_x509.SSLCertFile, config.Config.TLS.CertPath)()

		sf := NewGitalyServerFactory(nil)

		// start gitaly serving on public endpoint
		listener, err := net.Listen(starter.TCP, ":0")
		require.NoError(t, err)
		defer func() { require.NoError(t, listener.Close()) }()
		go sf.Serve(listener, true)

		addr, err := starter.Compose(starter.TLS, fmt.Sprintf("localhost:%d", listener.Addr().(*net.TCPAddr).Port))
		require.NoError(t, err)

		cc, err := client.Dial(addr, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, cc.Close()) }()

		healthClient := healthpb.NewHealthClient(cc)

		resp, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.Status)
	})

	t.Run("all services must be stopped", func(t *testing.T) {
		sf := NewGitalyServerFactory(nil)

		// start gitaly serving on public endpoint
		tcpListener, err := net.Listen(starter.TCP, ":0")
		require.NoError(t, err)
		defer tcpListener.Close()
		go sf.Serve(tcpListener, false)

		tcpAddr, err := starter.Compose(starter.TCP, fmt.Sprintf("localhost:%d", tcpListener.Addr().(*net.TCPAddr).Port))
		require.NoError(t, err)

		tcpCC, err := client.Dial(tcpAddr, nil)
		require.NoError(t, err)

		tcpHealthClient := healthpb.NewHealthClient(tcpCC)

		tcpResp, err := tcpHealthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, tcpResp.Status)

		socket := testhelper.GetTemporaryGitalySocketFileName()
		defer func() { require.NoError(t, os.RemoveAll(socket)) }()
		socketListener, err := net.Listen(starter.Unix, socket)
		require.NoError(t, err)
		defer socketListener.Close()
		go sf.Serve(socketListener, false)

		socketAddr, err := starter.Compose(starter.Unix, socketListener.Addr().String())
		require.NoError(t, err)

		socketCC, err := client.Dial(socketAddr, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, socketCC.Close()) }()

		socketHealthClient := healthpb.NewHealthClient(socketCC)

		socketResp, err := socketHealthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, socketResp.Status)

		sf.GracefulStop() // stops all started servers(listeners)

		_, tcpErr := tcpHealthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.Equal(t, codes.Unavailable, status.Code(tcpErr))

		_, socketErr := socketHealthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.Equal(t, codes.Unavailable, status.Code(socketErr))
	})
}
