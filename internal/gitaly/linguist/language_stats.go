package linguist

import (
	"compress/zlib"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
)

const (
	// languageStatsFilename is the name of the file in the repo that stores
	// a cached version of the language statistics. The name is
	// intentionally different from what the linguist gem uses.
	languageStatsFilename = "gitaly-language.stats"
	languageStatsVersion  = "v2:gitaly"
)

// languageStats takes care of accumulating and caching language statistics for
// a repository.
type languageStats struct {
	// Version holds the file format version
	Version string `json:"version"`
	// CommitID holds the commit ID for the cached Totals
	CommitID string `json:"commit_id"`

	// m will protect concurrent writes to Totals & ByFile maps
	m sync.Mutex

	// Totals contains the total statistics for the CommitID
	Totals ByteCountPerLanguage `json:"totals"`
	// ByFile contains the statistics for a single file, where the filename
	// is its key.
	ByFile map[string]ByteCountPerLanguage `json:"by_file"`
}

// newLanguageStats creates a languageStats object and tries to load the
// optionally available stats from file.
func newLanguageStats(repo *localrepo.Repo) (*languageStats, error) {
	stats := languageStats{
		Totals: ByteCountPerLanguage{},
		ByFile: make(map[string]ByteCountPerLanguage),
	}

	objPath, err := repo.Path()
	if err != nil {
		return &stats, fmt.Errorf("new language stats get repo path: %w", err)
	}

	file, err := os.Open(filepath.Join(objPath, languageStatsFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return &stats, nil
		}
		return &stats, fmt.Errorf("new language stats open: %w", err)
	}
	defer file.Close()

	r, err := zlib.NewReader(file)
	if err != nil {
		return &stats, fmt.Errorf("new language stats zlib reader: %w", err)
	}

	var loaded languageStats
	if err = json.NewDecoder(r).Decode(&loaded); err != nil {
		return &stats, fmt.Errorf("new language stats json decode: %w", err)
	}

	if loaded.Version != languageStatsVersion {
		return &stats, fmt.Errorf("new language stats version mismatch %s vs %s", languageStatsVersion, loaded.Version)
	}

	return &loaded, nil
}

// add the statistics for the given filename
func (c *languageStats) add(filename, language string, size uint64) {
	c.m.Lock()
	defer c.m.Unlock()

	for k, v := range c.ByFile[filename] {
		c.Totals[k] -= v
		if c.Totals[k] <= 0 {
			delete(c.Totals, k)
		}
	}

	c.ByFile[filename] = ByteCountPerLanguage{language: size}
	if size > 0 {
		c.Totals[language] += size
	}
}

// drop statistics for the given files
func (c *languageStats) drop(filenames ...string) {
	c.m.Lock()
	defer c.m.Unlock()

	for _, f := range filenames {
		for k, v := range c.ByFile[f] {
			c.Totals[k] -= v
			if c.Totals[k] <= 0 {
				delete(c.Totals, k)
			}
		}
		delete(c.ByFile, f)
	}
}

// save the language stats to file in the repository
func (c *languageStats) save(repo *localrepo.Repo, commitID string) error {
	c.CommitID = commitID
	c.Version = languageStatsVersion

	repoPath, err := repo.Path()
	if err != nil {
		return fmt.Errorf("languageStats save get repo path: %w", err)
	}

	tempPath, err := repo.StorageTempDir()
	if err != nil {
		return fmt.Errorf("languageStats locate temp dir: %w", err)
	}

	file, err := os.CreateTemp(tempPath, languageStatsFilename)
	if err != nil {
		return fmt.Errorf("languageStats create temp file: %w", err)
	}
	defer func() {
		file.Close()
		_ = os.Remove(file.Name())
	}()

	w := zlib.NewWriter(file)
	defer func() {
		// We already check the error further down.
		_ = w.Close()
	}()

	if err = json.NewEncoder(w).Encode(c); err != nil {
		return fmt.Errorf("languageStats encode json: %w", err)
	}

	if err = w.Close(); err != nil {
		return fmt.Errorf("languageStats zlib write: %w", err)
	}
	if err = file.Sync(); err != nil {
		return fmt.Errorf("languageStats flush: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("languageStats close: %w", err)
	}

	if err = os.Rename(file.Name(), filepath.Join(repoPath, languageStatsFilename)); err != nil {
		return fmt.Errorf("languageStats rename: %w", err)
	}

	return nil
}
