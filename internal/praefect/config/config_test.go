package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/errors/cfgerror"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/duration"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestConfigValidation(t *testing.T) {
	vs1Nodes := []*Node{
		{Storage: "internal-1.0", Address: "localhost:23456", Token: "secret-token-1"},
		{Storage: "internal-2.0", Address: "localhost:23457", Token: "secret-token-1"},
		{Storage: "internal-3.0", Address: "localhost:23458", Token: "secret-token-1"},
	}

	vs2Nodes := []*Node{
		// storage can have same name as storage in another virtual storage, but all addresses must be unique
		{Storage: "internal-1.0", Address: "localhost:33456", Token: "secret-token-2"},
		{Storage: "internal-2.1", Address: "localhost:33457", Token: "secret-token-2"},
		{Storage: "internal-3.1", Address: "localhost:33458", Token: "secret-token-2"},
	}

	testCases := []struct {
		desc         string
		changeConfig func(*Config)
		errMsg       string
	}{
		{
			desc:         "Valid config with ListenAddr",
			changeConfig: func(*Config) {},
		},
		{
			desc: "Valid config with local elector",
			changeConfig: func(cfg *Config) {
				cfg.Failover.ElectionStrategy = ElectionStrategyLocal
			},
		},
		{
			desc: "Valid config with per repository elector",
			changeConfig: func(cfg *Config) {
				cfg.Failover.ElectionStrategy = ElectionStrategyPerRepository
			},
		},
		{
			desc: "Invalid election strategy",
			changeConfig: func(cfg *Config) {
				cfg.Failover.ElectionStrategy = "invalid-strategy"
			},
			errMsg: `invalid election strategy: "invalid-strategy"`,
		},
		{
			desc: "Valid config with TLSListenAddr",
			changeConfig: func(cfg *Config) {
				cfg.ListenAddr = ""
				cfg.TLSListenAddr = "tls://localhost:4321"
			},
		},
		{
			desc: "Valid config with SocketPath",
			changeConfig: func(cfg *Config) {
				cfg.ListenAddr = ""
				cfg.SocketPath = "/tmp/praefect.socket"
			},
		},
		{
			desc: "Invalid replication batch size",
			changeConfig: func(cfg *Config) {
				cfg.Replication = Replication{BatchSize: 0}
			},
			errMsg: "replication batch size was 0 but must be >=1",
		},
		{
			desc: "No ListenAddr or SocketPath or TLSListenAddr",
			changeConfig: func(cfg *Config) {
				cfg.ListenAddr = ""
			},
			errMsg: "no listen address or socket path configured",
		},
		{
			desc: "No virtual storages",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = nil
			},
			errMsg: "no virtual storages configured",
		},
		{
			desc: "duplicate storage",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = []*VirtualStorage{
					{
						Name: "default",
						Nodes: append(vs1Nodes, &Node{
							Storage: vs1Nodes[0].Storage,
							Address: vs1Nodes[1].Address,
						}),
					},
				}
			},
			errMsg: `virtual storage "default": internal gitaly storages are not unique`,
		},
		{
			desc: "Node storage has no name",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = []*VirtualStorage{
					{
						Name: "default",
						Nodes: []*Node{
							{
								Storage: "",
								Address: "localhost:23456",
								Token:   "secret-token-1",
							},
						},
					},
				}
			},
			errMsg: `virtual storage "default": all gitaly nodes must have a storage`,
		},
		{
			desc: "Node storage has no address",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = []*VirtualStorage{
					{
						Name: "default",
						Nodes: []*Node{
							{
								Storage: "internal",
								Address: "",
								Token:   "secret-token-1",
							},
						},
					},
				}
			},
			errMsg: `virtual storage "default": all gitaly nodes must have an address`,
		},
		{
			desc: "Virtual storage has no name",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = []*VirtualStorage{
					{Name: "", Nodes: vs1Nodes},
				}
			},
			errMsg: `virtual storages must have a name`,
		},
		{
			desc: "Virtual storage not unique",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = []*VirtualStorage{
					{Name: "default", Nodes: vs1Nodes},
					{Name: "default", Nodes: vs2Nodes},
				}
			},
			errMsg: `virtual storage "default": virtual storages must have unique names`,
		},
		{
			desc: "Virtual storage has no nodes",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = []*VirtualStorage{
					{Name: "default", Nodes: vs1Nodes},
					{Name: "secondary", Nodes: nil},
				}
			},
			errMsg: `virtual storage "secondary": no primary gitaly backends configured`,
		},
		{
			desc: "default replication factor too high",
			changeConfig: func(cfg *Config) {
				cfg.VirtualStorages = []*VirtualStorage{
					{
						Name:                     "default",
						DefaultReplicationFactor: 2,
						Nodes: []*Node{
							{
								Storage: "storage-1",
								Address: "localhost:23456",
							},
						},
					},
				}
			},
			errMsg: `virtual storage "default" has a default replication factor (2) which is higher than the number of storages (1)`,
		},
		{
			desc: "repositories_cleanup minimal duration is too low",
			changeConfig: func(cfg *Config) {
				cfg.RepositoriesCleanup.CheckInterval = duration.Duration(minimalSyncCheckInterval - time.Nanosecond)
			},
			errMsg: `repositories_cleanup.check_interval is less then 1m0s, which could lead to a database performance problem`,
		},
		{
			desc: "repositories_cleanup minimal duration is too low",
			changeConfig: func(cfg *Config) {
				cfg.RepositoriesCleanup.RunInterval = duration.Duration(minimalSyncRunInterval - time.Nanosecond)
			},
			errMsg: `repositories_cleanup.run_interval is less then 1m0s, which could lead to a database performance problem`,
		},
		{
			desc: "yamux.maximum_stream_window_size_bytes is too low",
			changeConfig: func(cfg *Config) {
				cfg.Yamux.MaximumStreamWindowSizeBytes = 16
			},
			errMsg: `yamux.maximum_stream_window_size_bytes must be at least 262144 but it was 16`,
		},
		{
			desc: "yamux.maximum_stream_window_size_bytes is too low",
			changeConfig: func(cfg *Config) {
				cfg.Yamux.AcceptBacklog = 0
			},
			errMsg: `yamux.accept_backlog must be at least 1 but it was 0`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			config := Config{
				ListenAddr:  "localhost:1234",
				Replication: DefaultReplicationConfig(),
				VirtualStorages: []*VirtualStorage{
					{Name: "default", Nodes: vs1Nodes},
					{Name: "secondary", Nodes: vs2Nodes},
				},
				Failover:            Failover{ElectionStrategy: ElectionStrategySQL},
				RepositoriesCleanup: DefaultRepositoriesCleanup(),
				Yamux:               DefaultYamuxConfig(),
			}

			tc.changeConfig(&config)

			err := config.Validate()
			if tc.errMsg == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errMsg)
		})
	}
}

func TestConfigParsing(t *testing.T) {
	testCases := []struct {
		desc        string
		filePath    string
		expected    Config
		expectedErr error
	}{
		{
			desc:     "check all configuration values",
			filePath: "testdata/config.toml",
			expected: Config{
				TLSListenAddr: "0.0.0.0:2306",
				TLS: config.TLS{
					CertPath: "/home/git/cert.cert",
					KeyPath:  "/home/git/key.pem",
				},
				Logging: log.Config{
					Level:  "info",
					Format: "json",
				},
				Sentry: sentry.Config{
					DSN:         "abcd123",
					Environment: "production",
				},
				VirtualStorages: []*VirtualStorage{
					{
						Name:                     "praefect",
						DefaultReplicationFactor: 2,
						Nodes: []*Node{
							{
								Address: "tcp://gitaly-internal-1.example.com",
								Storage: "praefect-internal-1",
							},
							{
								Address: "tcp://gitaly-internal-2.example.com",
								Storage: "praefect-internal-2",
							},
							{
								Address: "tcp://gitaly-internal-3.example.com",
								Storage: "praefect-internal-3",
							},
						},
					},
				},
				Prometheus: prometheus.Config{
					ScrapeTimeout:      duration.Duration(time.Second),
					GRPCLatencyBuckets: []float64{0.1, 0.2, 0.3},
				},
				DB: DB{
					Host:        "1.2.3.4",
					Port:        5432,
					User:        "praefect",
					Password:    "db-secret",
					DBName:      "praefect_production",
					SSLMode:     "require",
					SSLCert:     "/path/to/cert",
					SSLKey:      "/path/to/key",
					SSLRootCert: "/path/to/root-cert",
					SessionPooled: DBConnection{
						Host:        "2.3.4.5",
						Port:        6432,
						User:        "praefect_sp",
						Password:    "db-secret-sp",
						DBName:      "praefect_production_sp",
						SSLMode:     "prefer",
						SSLCert:     "/path/to/sp/cert",
						SSLKey:      "/path/to/sp/key",
						SSLRootCert: "/path/to/sp/root-cert",
					},
				},
				MemoryQueueEnabled:  true,
				GracefulStopTimeout: duration.Duration(30 * time.Second),
				Reconciliation: Reconciliation{
					SchedulingInterval: duration.Duration(time.Minute),
					HistogramBuckets:   []float64{1, 2, 3, 4, 5},
				},
				Replication: Replication{BatchSize: 1, ParallelStorageProcessingWorkers: 2},
				Failover: Failover{
					Enabled:                  true,
					ElectionStrategy:         ElectionStrategyPerRepository,
					ErrorThresholdWindow:     duration.Duration(20 * time.Second),
					WriteErrorThresholdCount: 1500,
					ReadErrorThresholdCount:  100,
					BootstrapInterval:        duration.Duration(1 * time.Second),
					MonitorInterval:          duration.Duration(3 * time.Second),
				},
				RepositoriesCleanup: RepositoriesCleanup{
					CheckInterval:       duration.Duration(time.Second),
					RunInterval:         duration.Duration(3 * time.Second),
					RepositoriesInBatch: 10,
				},
				BackgroundVerification: BackgroundVerification{
					VerificationInterval: duration.Duration(24 * time.Hour),
					DeleteInvalidRecords: false,
				},
				Yamux: Yamux{
					MaximumStreamWindowSizeBytes: 1000,
					AcceptBacklog:                2000,
				},
			},
		},
		{
			desc:     "overwriting default values in the config",
			filePath: "testdata/config.overwritedefaults.toml",
			expected: Config{
				GracefulStopTimeout: duration.Duration(time.Minute),
				Reconciliation: Reconciliation{
					SchedulingInterval: 0,
					HistogramBuckets:   []float64{1, 2, 3, 4, 5},
				},
				Prometheus:  prometheus.DefaultConfig(),
				Replication: Replication{BatchSize: 1, ParallelStorageProcessingWorkers: 2},
				Failover: Failover{
					Enabled:           false,
					ElectionStrategy:  "local",
					BootstrapInterval: duration.Duration(5 * time.Second),
					MonitorInterval:   duration.Duration(10 * time.Second),
				},
				RepositoriesCleanup: RepositoriesCleanup{
					CheckInterval:       duration.Duration(time.Second),
					RunInterval:         duration.Duration(4 * time.Second),
					RepositoriesInBatch: 11,
				},
				BackgroundVerification: DefaultBackgroundVerificationConfig(),
				Yamux:                  DefaultYamuxConfig(),
			},
		},
		{
			desc:     "empty config yields default values",
			filePath: "testdata/config.empty.toml",
			expected: Config{
				GracefulStopTimeout: duration.Duration(time.Minute),
				Prometheus:          prometheus.DefaultConfig(),
				Reconciliation:      DefaultReconciliationConfig(),
				Replication:         DefaultReplicationConfig(),
				Failover: Failover{
					Enabled:           true,
					ElectionStrategy:  ElectionStrategyPerRepository,
					BootstrapInterval: duration.Duration(time.Second),
					MonitorInterval:   duration.Duration(3 * time.Second),
				},
				RepositoriesCleanup: RepositoriesCleanup{
					CheckInterval:       duration.Duration(30 * time.Minute),
					RunInterval:         duration.Duration(24 * time.Hour),
					RepositoriesInBatch: 16,
				},
				BackgroundVerification: DefaultBackgroundVerificationConfig(),
				Yamux:                  DefaultYamuxConfig(),
			},
		},
		{
			desc:        "config file does not exist",
			filePath:    "testdata/config.invalid-path.toml",
			expectedErr: os.ErrNotExist,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, err := FromFile(tc.filePath)
			require.True(t, errors.Is(err, tc.expectedErr), "actual error: %v", err)
			require.Equal(t, tc.expected, cfg)
		})
	}
}

func TestVirtualStorageNames(t *testing.T) {
	conf := Config{VirtualStorages: []*VirtualStorage{{Name: "praefect-1"}, {Name: "praefect-2"}}}
	require.Equal(t, []string{"praefect-1", "praefect-2"}, conf.VirtualStorageNames())
}

func TestStorageNames(t *testing.T) {
	conf := Config{
		VirtualStorages: []*VirtualStorage{
			{Name: "virtual-storage-1", Nodes: []*Node{{Storage: "gitaly-1"}, {Storage: "gitaly-2"}}},
			{Name: "virtual-storage-2", Nodes: []*Node{{Storage: "gitaly-3"}, {Storage: "gitaly-4"}}},
		},
	}
	require.Equal(t, map[string][]string{
		"virtual-storage-1": {"gitaly-1", "gitaly-2"},
		"virtual-storage-2": {"gitaly-3", "gitaly-4"},
	}, conf.StorageNames())
}

func TestDefaultReplicationFactors(t *testing.T) {
	for _, tc := range []struct {
		desc                      string
		virtualStorages           []*VirtualStorage
		defaultReplicationFactors map[string]int
	}{
		{
			desc: "replication factors set on some",
			virtualStorages: []*VirtualStorage{
				{Name: "virtual-storage-1", DefaultReplicationFactor: 0},
				{Name: "virtual-storage-2", DefaultReplicationFactor: 1},
			},
			defaultReplicationFactors: map[string]int{
				"virtual-storage-1": 0,
				"virtual-storage-2": 1,
			},
		},
		{
			desc:                      "returns always initialized map",
			virtualStorages:           []*VirtualStorage{},
			defaultReplicationFactors: map[string]int{},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t,
				tc.defaultReplicationFactors,
				Config{VirtualStorages: tc.virtualStorages}.DefaultReplicationFactors(),
			)
		})
	}
}

func TestNeedsSQL(t *testing.T) {
	testCases := []struct {
		desc     string
		config   Config
		expected bool
	}{
		{
			desc:     "default",
			config:   Config{},
			expected: true,
		},
		{
			desc:     "Memory queue enabled",
			config:   Config{MemoryQueueEnabled: true},
			expected: false,
		},
		{
			desc:     "Failover enabled with default election strategy",
			config:   Config{Failover: Failover{Enabled: true}},
			expected: true,
		},
		{
			desc:     "Failover enabled with SQL election strategy",
			config:   Config{Failover: Failover{Enabled: true, ElectionStrategy: ElectionStrategyPerRepository}},
			expected: true,
		},
		{
			desc:     "Both PostgresQL and SQL election strategy enabled",
			config:   Config{Failover: Failover{Enabled: true, ElectionStrategy: ElectionStrategyPerRepository}},
			expected: true,
		},
		{
			desc:     "Both PostgresQL and SQL election strategy enabled but failover disabled",
			config:   Config{Failover: Failover{Enabled: false, ElectionStrategy: ElectionStrategyPerRepository}},
			expected: true,
		},
		{
			desc:     "Both PostgresQL and per_repository election strategy enabled but failover disabled",
			config:   Config{Failover: Failover{Enabled: false, ElectionStrategy: ElectionStrategyPerRepository}},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.config.NeedsSQL())
		})
	}
}

func TestSerialization(t *testing.T) {
	out := &bytes.Buffer{}
	encoder := toml.NewEncoder(out)

	t.Run("completely empty", func(t *testing.T) {
		out.Reset()
		require.NoError(t, encoder.Encode(Config{}))
		require.Empty(t, out.Bytes())
	})

	t.Run("partially set", func(t *testing.T) {
		out.Reset()
		require.NoError(t, encoder.Encode(Config{ListenAddr: "localhost:5640"}))
		require.Equal(t, "listen_addr = 'localhost:5640'\n", out.String())
	})
}

func TestFailover_Validate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		failover    Failover
		expectedErr error
	}{
		{
			name:     "empty disabled config",
			failover: Failover{},
		},
		{
			name: "all set valid",
			failover: Failover{
				Enabled:                  true,
				ElectionStrategy:         ElectionStrategySQL,
				ErrorThresholdWindow:     duration.Duration(1),
				WriteErrorThresholdCount: 1,
				ReadErrorThresholdCount:  1,
				BootstrapInterval:        duration.Duration(1),
				MonitorInterval:          duration.Duration(1),
			},
		},
		{
			name: "all valid with disabled error threshold",
			failover: Failover{
				ElectionStrategy:  ElectionStrategySQL,
				BootstrapInterval: duration.Duration(1),
				MonitorInterval:   duration.Duration(1),
			},
		},
		{
			name: "all set invalid except ErrorThresholdWindow",
			failover: Failover{
				Enabled:                  true,
				ElectionStrategy:         ElectionStrategy("bad"),
				ErrorThresholdWindow:     duration.Duration(-1),
				WriteErrorThresholdCount: 0,
				ReadErrorThresholdCount:  0,
				BootstrapInterval:        duration.Duration(-1),
				MonitorInterval:          duration.Duration(-1),
			},
			expectedErr: cfgerror.ValidationErrors{
				{
					Key:   []string{"election_strategy"},
					Cause: fmt.Errorf(`%w: "bad"`, cfgerror.ErrUnsupportedValue),
				},
				{
					Key:   []string{"bootstrap_interval"},
					Cause: fmt.Errorf("%w: -1ns is not greater than or equal to 0s", cfgerror.ErrNotInRange),
				},
				{
					Key:   []string{"monitor_interval"},
					Cause: fmt.Errorf("%w: -1ns is not greater than or equal to 0s", cfgerror.ErrNotInRange),
				},
				{
					Key:   []string{"error_threshold_window"},
					Cause: fmt.Errorf("%w: -1ns is not greater than or equal to 0s", cfgerror.ErrNotInRange),
				},
				{
					Key:   []string{"write_error_threshold_count"},
					Cause: cfgerror.ErrNotSet,
				},
				{
					Key:   []string{"read_error_threshold_count"},
					Cause: cfgerror.ErrNotSet,
				},
			},
		},
		{
			name: "invalid error threshold",
			failover: Failover{
				Enabled:                  true,
				ElectionStrategy:         ElectionStrategySQL,
				ErrorThresholdWindow:     duration.Duration(0),
				WriteErrorThresholdCount: 1,
				ReadErrorThresholdCount:  1,
			},
			expectedErr: cfgerror.ValidationErrors{
				{
					Key:   []string{"error_threshold_window"},
					Cause: cfgerror.ErrNotSet,
				},
			},
		},
		{
			name: "set invalid but disabled",
			failover: Failover{
				Enabled:          false,
				ElectionStrategy: ElectionStrategy("bad"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.failover.Validate()
			require.Equal(t, tc.expectedErr, errs)
		})
	}
}

func TestBackgroundVerification_Validate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		verification BackgroundVerification
		expectedErr  error
	}{
		{
			name:         "empty is valid",
			verification: BackgroundVerification{},
		},
		{
			name: "valid",
			verification: BackgroundVerification{
				DeleteInvalidRecords: true,
				VerificationInterval: duration.Duration(1),
			},
		},
		{
			name: "zero",
			verification: BackgroundVerification{
				DeleteInvalidRecords: true,
				VerificationInterval: duration.Duration(0),
			},
		},
		{
			name: "invalid",
			verification: BackgroundVerification{
				VerificationInterval: duration.Duration(-1),
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: -1ns is not greater than or equal to 0s", cfgerror.ErrNotInRange),
					"verification_interval",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.verification.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestReconciliation_Validate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		reconciliation Reconciliation
		expectedErr    error
	}{
		{
			name:           "empty is valid",
			reconciliation: Reconciliation{},
		},
		{
			name: "valid",
			reconciliation: Reconciliation{
				SchedulingInterval: duration.Duration(1),
				HistogramBuckets:   []float64{-1, 0, 1},
			},
		},
		{
			name: "invalid",
			reconciliation: Reconciliation{
				SchedulingInterval: duration.Duration(-1),
				HistogramBuckets:   []float64{-1, 1, 0},
			},
			expectedErr: cfgerror.ValidationErrors{{
				Key:   []string{"scheduling_interval"},
				Cause: fmt.Errorf("%w: -1ns is not greater than or equal to 0s", cfgerror.ErrNotInRange),
			}, {
				Key:   []string{"histogram_buckets"},
				Cause: cfgerror.ErrBadOrder,
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.reconciliation.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestReplication_Validate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		replication Replication
		expectedErr error
	}{
		{
			name: "valid",
			replication: Replication{
				BatchSize:                        1,
				ParallelStorageProcessingWorkers: 1,
			},
		},
		{
			name: "invalid",
			replication: Replication{
				BatchSize:                        0,
				ParallelStorageProcessingWorkers: 0,
			},
			expectedErr: cfgerror.ValidationErrors{{
				Key:   []string{"batch_size"},
				Cause: fmt.Errorf("%w: 0 is not greater than or equal to 1", cfgerror.ErrNotInRange),
			}, {
				Key:   []string{"parallel_storage_processing_workers"},
				Cause: fmt.Errorf("%w: 0 is not greater than or equal to 1", cfgerror.ErrNotInRange),
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.replication.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestVirtualStorage_Validate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		vs          VirtualStorage
		expectedErr error
	}{
		{
			name: "ok",
			vs: VirtualStorage{
				Name:  "vs",
				Nodes: []*Node{{Storage: "st", Address: "addr"}},
			},
		},
		{
			name: "invalid node",
			vs: VirtualStorage{
				Name:  "vs",
				Nodes: []*Node{{Storage: "", Address: "addr"}},
			},
			expectedErr: cfgerror.ValidationErrors{
				{
					Key:   []string{"node", "[0]", "storage"},
					Cause: cfgerror.ErrBlankOrEmpty,
				},
			},
		},
		{
			name: "invalid",
			vs:   VirtualStorage{},
			expectedErr: cfgerror.ValidationErrors{
				{
					Key:   []string{"name"},
					Cause: cfgerror.ErrBlankOrEmpty,
				},
				{
					Key:   []string{"node"},
					Cause: cfgerror.ErrNotSet,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.vs.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestRepositoriesCleanup_Validate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		cleanup     RepositoriesCleanup
		expectedErr error
	}{
		{
			name: "valid",
			cleanup: RepositoriesCleanup{
				CheckInterval:       duration.Duration(10 * time.Minute),
				RunInterval:         duration.Duration(1 * time.Minute),
				RepositoriesInBatch: 10,
			},
		},
		{
			name: "noop because run interval is 0",
			cleanup: RepositoriesCleanup{
				CheckInterval:       -duration.Duration(10 * time.Minute),
				RunInterval:         0,
				RepositoriesInBatch: 10,
			},
		},
		{
			name: "invalid",
			cleanup: RepositoriesCleanup{
				CheckInterval:       duration.Duration(1),
				RunInterval:         duration.Duration(50 * time.Second),
				RepositoriesInBatch: 0,
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 1ns is not greater than or equal to 1m0s", cfgerror.ErrNotInRange),
					"check_interval",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 50s is not greater than or equal to 1m0s", cfgerror.ErrNotInRange),
					"run_interval",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 0 is not greater than or equal to 1", cfgerror.ErrNotInRange),
					"repositories_in_batch",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cleanup.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestYamux_Validate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		yamux       Yamux
		expectedErr error
	}{
		{
			name: "valid",
			yamux: Yamux{
				MaximumStreamWindowSizeBytes: 1024 * 1024,
				AcceptBacklog:                5,
			},
		},
		{
			name: "invalid",
			yamux: Yamux{
				MaximumStreamWindowSizeBytes: 1024,
				AcceptBacklog:                0,
			},
			expectedErr: cfgerror.ValidationErrors{
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 1024 is not greater than or equal to 262144", cfgerror.ErrNotInRange),
					"maximum_stream_window_size_bytes",
				),
				cfgerror.NewValidationError(
					fmt.Errorf("%w: 0 is not greater than or equal to 1", cfgerror.ErrNotInRange),
					"accept_backlog",
				),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.yamux.Validate()
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestConfig_ValidateV2(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		cfg := Config{
			Replication: Replication{
				BatchSize:                        1,
				ParallelStorageProcessingWorkers: 1,
			},
			ListenAddr: "localhost",
			VirtualStorages: []*VirtualStorage{{
				Name: "name",
				Nodes: []*Node{{
					Storage: "storage",
					Address: "localhost",
				}},
			}},
			Yamux: Yamux{
				MaximumStreamWindowSizeBytes: 300000,
				AcceptBacklog:                1,
			},
		}
		require.NoError(t, cfg.ValidateV2())
	})

	t.Run("invalid", func(t *testing.T) {
		tmpDir := testhelper.TempDir(t)
		tmpFile := filepath.Join(tmpDir, "file")
		require.NoError(t, os.WriteFile(tmpFile, nil, perm.PublicFile))
		cfg := Config{
			BackgroundVerification: BackgroundVerification{
				VerificationInterval: duration.Duration(-1),
			},
			Reconciliation: Reconciliation{
				SchedulingInterval: duration.Duration(-1),
			},
			Replication: Replication{
				BatchSize:                        0,
				ParallelStorageProcessingWorkers: 1,
			},
			Prometheus: prometheus.Config{
				ScrapeTimeout:      duration.Duration(-1),
				GRPCLatencyBuckets: []float64{1},
			},
			TLS: config.TLS{
				CertPath: "/doesnt/exist",
				KeyPath:  tmpFile,
			},
			Failover: Failover{
				Enabled:          true,
				ElectionStrategy: ElectionStrategy("invalid"),
			},
			GracefulStopTimeout: duration.Duration(-1),
			RepositoriesCleanup: RepositoriesCleanup{
				CheckInterval:       duration.Duration(time.Hour),
				RunInterval:         duration.Duration(1),
				RepositoriesInBatch: 1,
			},
			Yamux: Yamux{
				MaximumStreamWindowSizeBytes: 0,
				AcceptBacklog:                1,
			},
		}
		err := cfg.ValidateV2()

		negativeDurationErr := fmt.Errorf("%w: -1ns is not greater than or equal to 0s", cfgerror.ErrNotInRange)
		require.Equal(t, cfgerror.ValidationErrors{
			cfgerror.NewValidationError(errors.New(`none of "socket_path", "listen_addr" or "tls_listen_addr" is set`)),
			cfgerror.NewValidationError(negativeDurationErr, "background_verification", "verification_interval"),
			cfgerror.NewValidationError(negativeDurationErr, "reconciliation", "scheduling_interval"),
			cfgerror.NewValidationError(fmt.Errorf("%w: 0 is not greater than or equal to 1", cfgerror.ErrNotInRange), "replication", "batch_size"),
			cfgerror.NewValidationError(negativeDurationErr, "prometheus", "scrape_timeout"),
			cfgerror.NewValidationError(fmt.Errorf(`%w: "/doesnt/exist"`, cfgerror.ErrDoesntExist), "tls", "certificate_path"),
			cfgerror.NewValidationError(fmt.Errorf(`%w: "invalid"`, cfgerror.ErrUnsupportedValue), "failover", "election_strategy"),
			cfgerror.NewValidationError(negativeDurationErr, "graceful_stop_timeout"),
			cfgerror.NewValidationError(fmt.Errorf("%w: 1ns is not greater than or equal to 1m0s", cfgerror.ErrNotInRange), "repositories_cleanup", "run_interval"),
			cfgerror.NewValidationError(fmt.Errorf("%w: 0 is not greater than or equal to 262144", cfgerror.ErrNotInRange), "yamux", "maximum_stream_window_size_bytes"),
			cfgerror.NewValidationError(cfgerror.ErrNotSet, "virtual_storage"),
		}, err)
	})
}

func TestConfig_ConfigCommand(t *testing.T) {
	t.Parallel()

	modifyDefaultConfig := func(modify func(cfg *Config)) Config {
		cfg, err := FromReader(strings.NewReader(""))
		require.NoError(t, err)
		modify(&cfg)
		return cfg
	}

	writeScript := func(t *testing.T, script string) string {
		return testhelper.WriteExecutable(t,
			filepath.Join(testhelper.TempDir(t), "script"),
			[]byte("#!/bin/sh\n"+script),
		)
	}

	type setupData struct {
		cfg         Config
		expectedErr string
		expectedCfg Config
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "nonexistent executable",
			setup: func(t *testing.T) setupData {
				return setupData{
					cfg: Config{
						ConfigCommand: "/does/not/exist",
					},
					expectedErr: "running config command: fork/exec /does/not/exist: no such file or directory",
				}
			},
		},
		{
			desc: "command points to non-executable file",
			setup: func(t *testing.T) setupData {
				cmd := filepath.Join(testhelper.TempDir(t), "script")
				require.NoError(t, os.WriteFile(cmd, nil, perm.PrivateWriteOnceFile))

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
					},
					expectedErr: fmt.Sprintf(
						"running config command: fork/exec %s: permission denied", cmd,
					),
				}
			},
		},
		{
			desc: "executable returns error",
			setup: func(t *testing.T) setupData {
				return setupData{
					cfg: Config{
						ConfigCommand: writeScript(t, "echo error >&2 && exit 1"),
					},
					expectedErr: "running config command: exit status 1, stderr: \"error\\n\"",
				}
			},
		},
		{
			desc: "invalid JSON",
			setup: func(t *testing.T) setupData {
				return setupData{
					cfg: Config{
						ConfigCommand: writeScript(t, "echo 'this is not json'"),
					},
					expectedErr: "unmarshalling generated config: invalid character 'h' in literal true (expecting 'r')",
				}
			},
		},
		{
			desc: "mixed stdout and stderr",
			setup: func(t *testing.T) setupData {
				// We want to verify that we're able to correctly parse the output
				// even if the process writes to both its stdout and stderr.
				cmd := writeScript(t, "echo error >&2 && echo '{}'")

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
					}),
				}
			},
		},
		{
			desc: "empty script",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, "echo '{}'")

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
					}),
				}
			},
		},
		{
			desc: "unknown value",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"key_does_not_exist":"value"}'`)

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
					}),
				}
			},
		},
		{
			desc: "generated value",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"socket_path": "value"}'`)

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
						cfg.SocketPath = "value"
					}),
				}
			},
		},
		{
			desc: "overridden value",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"socket_path": "overridden_value"}'`)

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
						SocketPath:    "initial_value",
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
						cfg.SocketPath = "overridden_value"
					}),
				}
			},
		},
		{
			desc: "mixed configuration",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"listen_addr": "listen_addr"}'`)

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
						SocketPath:    "socket_path",
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
						cfg.SocketPath = "socket_path"
						cfg.ListenAddr = "listen_addr"
					}),
				}
			},
		},
		{
			desc: "override default value",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `echo '{"storage": []}'`)

				return setupData{
					cfg: Config{
						ConfigCommand:       cmd,
						GracefulStopTimeout: duration.Duration(time.Second),
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
						cfg.GracefulStopTimeout = duration.Duration(time.Second)
					}),
				}
			},
		},
		{
			desc: "subsections are being merged",
			setup: func(t *testing.T) setupData {
				cmd := writeScript(t, `cat <<-EOF
					{
						"failover": {
							"bootstrap_interval": "13m"
						}
					}
					EOF
				`)

				return setupData{
					cfg: Config{
						ConfigCommand: cmd,
						Failover: Failover{
							MonitorInterval: duration.Duration(14 * time.Minute),
						},
					},
					expectedCfg: modifyDefaultConfig(func(cfg *Config) {
						cfg.ConfigCommand = cmd
						cfg.Failover.BootstrapInterval = duration.Duration(13 * time.Minute)
						cfg.Failover.MonitorInterval = duration.Duration(14 * time.Minute)
					}),
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			t.Run("FromReader", func(t *testing.T) {
				var cfgBuffer bytes.Buffer
				require.NoError(t, toml.NewEncoder(&cfgBuffer).Encode(setup.cfg))

				cfg, err := FromReader(&cfgBuffer)

				// We can't use `require.Equal()` for the error as it's basically impossible
				// to reproduce the exact `exec.ExitError`.
				if setup.expectedErr != "" {
					require.EqualError(t, err, setup.expectedErr)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, setup.expectedCfg, cfg)
			})

			t.Run("FromFile", func(t *testing.T) {
				cfgPath := filepath.Join(testhelper.TempDir(t), "praefect.toml")

				cfgFile, err := os.Create(cfgPath)
				require.NoError(t, err)
				require.NoError(t, toml.NewEncoder(cfgFile).Encode(setup.cfg))
				testhelper.MustClose(t, cfgFile)

				cfg, err := FromFile(cfgPath)

				// We can't use `require.Equal()` for the error as it's basically impossible
				// to reproduce the exact `exec.ExitError`.
				if setup.expectedErr != "" {
					require.EqualError(t, err, setup.expectedErr)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, setup.expectedCfg, cfg)
			})
		})
	}
}
