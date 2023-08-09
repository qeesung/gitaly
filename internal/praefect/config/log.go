package config

import (
	"os"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

// ConfigureLogger applies the settings from the configuration file to the
// logger, setting the log level and format.
func (c Config) ConfigureLogger() *logrus.Entry {
	log.Configure(os.Stdout, c.Logging.Format, c.Logging.Level)

	return log.Default()
}
