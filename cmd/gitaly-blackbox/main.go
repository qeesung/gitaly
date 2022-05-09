package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/blackbox"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/version"
)

var flagVersion = flag.Bool("version", false, "Print version and exit")

func flagUsage() {
	fmt.Println(version.GetVersionString())
	fmt.Printf("Usage: %v [OPTIONS] configfile\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = flagUsage
	flag.Parse()

	// If invoked with -version
	if *flagVersion {
		fmt.Println(version.GetVersionString())
		os.Exit(0)
	}

	if flag.NArg() != 1 || flag.Arg(0) == "" {
		flag.Usage()
		os.Exit(1)
	}

	if err := run(flag.Arg(0)); err != nil {
		logrus.WithError(err).Fatal()
	}
}

func run(configPath string) error {
	configRaw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	config, err := blackbox.ParseConfig(string(configRaw))
	if err != nil {
		return err
	}

	bb := blackbox.New(config)
	prometheus.MustRegister(bb)

	log.Configure(log.Loggers, config.Logging.Format, config.Logging.Level)

	return bb.Run()
}
