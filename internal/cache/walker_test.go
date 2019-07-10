package cache_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
)

func TestCleanWalker(t *testing.T) {
	tmpPath, err := ioutil.TempDir("", t.Name())
	require.NoError(t, err)
	defer func() { require.NoError(t, os.RemoveAll(tmpPath)) }()

	config.Config.Storages = []config.Storage{
		{
			Name: t.Name(),
			Path: tmpPath,
		},
	}

	satisfyConfigValidation(tmpPath)

	tests := []struct {
		name          string
		age           time.Duration
		expectRemoval bool
	}{
		{"0f/oldey", time.Hour, true},
		{"90/n00b", time.Minute, false},
		{"2b/ancient", 24 * time.Hour, true},
		{"cd/baby", time.Second, false},
	}

	var shouldExist, shouldNotExist []string

	for _, tt := range tests {
		path := filepath.Join(tmpPath, tt.name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))

		f, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, f.Close())

		os.Chtimes(path, time.Now(), time.Now().Add(-1*tt.age))

		if tt.expectRemoval {
			shouldExist = append(shouldExist, path)
			continue
		}
		shouldNotExist = append(shouldNotExist, path)
	}

	// config validation will trigger the file walkers to start
	require.NoError(t, config.Validate())

	timeout := time.After(time.Second)

	// poll prometheus metrics until expected walker stats appear
	for {
		select {
		case <-timeout:
			t.Fatal("timed out polling prometheus stats")
		default:
			// keep on truckin'
		}

		mFamilies, err := prometheus.DefaultGatherer.Gather()
		require.NoError(t, err)

		var checkCount, removalCount *io_prometheus_client.Counter

		for _, mf := range mFamilies {
			if mf.GetName() == "gitaly_diskcache_walker" {
				for _, metric := range mf.GetMetric() {
					for _, label := range metric.GetLabel() {
						switch label.GetValue() {
						case "check":
							checkCount = metric.GetCounter()
						case "removal":
							removalCount = metric.GetCounter()
						}
					}
				}
				break
			}
		}

		if checkCount.GetValue() == 4 && removalCount.GetValue() == 2 {
			t.Log("Found expected counter metrics:", checkCount, removalCount)
			return
		}

		time.Sleep(time.Millisecond)
	}

	for _, p := range shouldExist {
		assert.FileExists(t, p)
	}

	for _, p := range shouldNotExist {
		_, err := os.Stat(p)
		require.True(t, os.IsNotExist(err))
	}
}

// satisfyConfigValidation puts garbage values in the config file to satisfy
// validation
func satisfyConfigValidation(tmpPath string) {
	config.Config.ListenAddr = "meow"
	config.Config.GitlabShell = config.GitlabShell{
		Dir: tmpPath,
	}
	config.Config.Ruby = config.Ruby{
		Dir: tmpPath,
	}
}
