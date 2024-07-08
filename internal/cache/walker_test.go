package cache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestDiskCacheObjectWalker(t *testing.T) {
	cfg := testcfg.Build(t)
	locator := config.NewLocator(cfg)

	var shouldExist, shouldNotExist []string

	for _, tt := range []struct {
		name          string
		age           time.Duration
		expectRemoval bool
	}{
		{"0f/oldey", time.Hour, true},
		{"90/n00b", time.Minute, false},
		{"2b/ancient", 24 * time.Hour, true},
		{"cd/baby", time.Second, false},
	} {
		cacheDir, err := locator.CacheDir(cfg.Storages[0].Name)
		require.NoError(t, err)

		path := filepath.Join(cacheDir, tt.name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), perm.PrivateDir))

		f, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, f.Close())

		require.NoError(t, os.Chtimes(path, time.Now(), time.Now().Add(-1*tt.age)))

		if tt.expectRemoval {
			shouldNotExist = append(shouldNotExist, path)
		} else {
			shouldExist = append(shouldExist, path)
		}
	}

	cache := New(cfg, locator, testhelper.SharedLogger(t), withDisabledMoveAndClear())
	require.NoError(t, cache.StartWalkers())
	defer cache.StopWalkers()

	require.Eventually(t, func() bool {
		count := int(promtest.ToFloat64(cache.walkerRemovalTotal))
		return count == 4
	}, time.Minute, time.Millisecond)

	for _, p := range shouldExist {
		assert.FileExists(t, p)
	}

	for _, p := range shouldNotExist {
		require.NoFileExists(t, p)
	}
}

func TestDiskCacheInitialClear(t *testing.T) {
	cfg := testcfg.Build(t)
	locator := config.NewLocator(cfg)

	cacheDir, err := locator.CacheDir(cfg.Storages[0].Name)
	require.NoError(t, err)

	canary := filepath.Join(cacheDir, "canary.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(canary), perm.PrivateDir))
	require.NoError(t, os.WriteFile(canary, []byte("chirp chirp"), perm.PrivateWriteOnceFile))

	cache := New(cfg, locator, testhelper.SharedLogger(t), withDisabledWalker())
	require.NoError(t, cache.StartWalkers())
	defer cache.StopWalkers()

	require.NoFileExists(t, canary)
}

func TestCleanWalkDirNotExists(t *testing.T) {
	cfg := testcfg.Build(t)

	cache := New(cfg, config.NewLocator(cfg), testhelper.SharedLogger(t))

	err := cache.cleanWalk("/path/that/does/not/exist")
	assert.NoError(t, err, "cleanWalk returned an error for a non existing directory")
}

func TestCleanWalkEmptyDirs(t *testing.T) {
	tmp := testhelper.TempDir(t)

	for _, tt := range []struct {
		path  string
		stale bool
	}{
		{path: "a/b/c/"},
		{path: "a/b/c/1", stale: true},
		{path: "a/b/c/2", stale: true},
		{path: "a/b/d/"},
		{path: "e/"},
		{path: "e/1"},
		{path: "f/"},
	} {
		p := filepath.Join(tmp, tt.path)
		if strings.HasSuffix(tt.path, "/") {
			require.NoError(t, os.MkdirAll(p, perm.PrivateDir))
		} else {
			require.NoError(t, os.WriteFile(p, nil, perm.SharedFile))
			if tt.stale {
				require.NoError(t, os.Chtimes(p, time.Now(), time.Now().Add(-time.Hour)))
			}
		}
	}

	cfg := testcfg.Build(t)
	cache := New(cfg, config.NewLocator(cfg), testhelper.SharedLogger(t))

	require.NoError(t, cache.cleanWalk(tmp))

	actual := findFiles(t, tmp)
	expect := `.
./e
./e/1
`
	require.Equal(t, expect, actual)
}

func findFiles(tb testing.TB, path string) string {
	cmd := exec.Command("find", ".")
	cmd.Dir = path
	out, err := cmd.Output()
	require.NoError(tb, err)
	return string(out)
}
