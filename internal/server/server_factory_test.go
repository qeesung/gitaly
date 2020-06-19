package server

import (
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

	checkHealth := func(t *testing.T, sf *GitalyServerFactory, schema, addr string) (healthpb.HealthClient, testhelper.Cleanup) {
		var cleanups []testhelper.Cleanup

		lschema := schema
		secure := false
		if schema == starter.TLS {
			lschema = starter.TCP
			secure = true
		}
		listener, err := net.Listen(lschema, addr)
		require.NoError(t, err)
		cleanups = append(cleanups, func() { listener.Close() })

		go sf.Serve(listener, secure)

		endpoint, err := starter.Compose(schema, listener.Addr().String())
		require.NoError(t, err)

		cc, err := client.Dial(endpoint, nil)
		require.NoError(t, err)
		cleanups = append(cleanups, func() { cc.Close() })

		healthClient := healthpb.NewHealthClient(cc)

		resp, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.Status)
		return healthClient, func() {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}

	t.Run("insecure", func(t *testing.T) {
		sf := NewGitalyServerFactory(nil)

		_, cleanup := checkHealth(t, sf, starter.TCP, ":0")
		defer cleanup()
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
		defer sf.GracefulStop()

		_, cleanup := checkHealth(t, sf, starter.TLS, ":0")
		defer cleanup()
	})

	t.Run("all services must be stopped", func(t *testing.T) {
		sf := NewGitalyServerFactory(nil)
		defer sf.GracefulStop()

		tcpHealthClient, tcpCleanup := checkHealth(t, sf, starter.TCP, ":0")
		defer tcpCleanup()

		socket := testhelper.GetTemporaryGitalySocketFileName()
		defer func() { require.NoError(t, os.RemoveAll(socket)) }()

		socketHealthClient, unixCleanup := checkHealth(t, sf, starter.Unix, socket)
		defer unixCleanup()

		sf.GracefulStop() // stops all started servers(listeners)

		_, tcpErr := tcpHealthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.Equal(t, codes.Unavailable, status.Code(tcpErr))

		_, socketErr := socketHealthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.Equal(t, codes.Unavailable, status.Code(socketErr))
	})
}
