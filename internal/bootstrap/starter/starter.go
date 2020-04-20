package starter

import (
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/bootstrap"
	"gitlab.com/gitlab-org/gitaly/internal/connectioncounter"
)

const (
	// TCP is the prefix for tcp
	TCP string = "tcp"
	// TLS is the prefix for tls
	TLS string = "tls"
	// Unix is the prefix for unix
	Unix string = "unix"
)

// Config represents a network type, and address
type Config struct {
	Name, Addr string
}

func (c *Config) isSecure() bool {
	return c.Name == TLS
}

func (c *Config) family() string {
	if c.isSecure() {
		return TCP
	}

	return c.Name
}

// New creates a new bootstrap.Starter from a config and a GracefulStoppableServer
func New(cfg Config, servers bootstrap.GracefulStoppableServer) bootstrap.Starter {
	return func(listen bootstrap.ListenFunc, errCh chan<- error) error {
		l, err := listen(cfg.family(), cfg.Addr)
		if err != nil {
			return err
		}

		logrus.WithField("address", cfg.Addr).Infof("listening at %s address", cfg.Name)
		l = connectioncounter.New(cfg.Name, l)

		go func() {
			errCh <- servers.Serve(l, cfg.isSecure())
		}()

		return nil
	}
}
