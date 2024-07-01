package stats

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

const (
	// StaleObjectsGracePeriod is time delta that is used to indicate cutoff wherein an object
	// would be considered old. Currently this is set to being 10 days.
	StaleObjectsGracePeriod = -10 * 24 * time.Hour

	// fullRepackTimestampFilename is the name of the file that is used as a timestamp for the
	// last repack that happened in the repository. Whenever a full repack happens, Gitaly will
	// touch this file so that its last-modified date can be used to tell how long ago the last
	// full repack happened.
	fullRepackTimestampFilename = ".gitaly-full-repack-timestamp"
)

// UpdateFullRepackTimestamp updates the timestamp that indicates when a repository has last been
// fully repacked to the current time.
func UpdateFullRepackTimestamp(repoPath string, timestamp time.Time) (returnedErr error) {
	timestampPath := filepath.Join(repoPath, fullRepackTimestampFilename)

	// We first try to update an existing file so that we don't rewrite the file in case it
	// already exists, but only update its atime and mtime.
	if err := os.Chtimes(timestampPath, timestamp, timestamp); err == nil {
		// The file exists and we have successfully updated its timestamp. We can thus exit
		// early.
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// Updating the timestamp failed, but not because the file doesn't exist. This
		// indicates an unexpected error and thus we return it to the caller.
		return err
	}

	// When the file does not yet exist we create it anew. Note that we do this via a temporary
	// file as it wouldn't otherwise be possible to atomically set the expected timestamp on it
	// because there is no API to create a file with a specific mtime.
	f, err := os.CreateTemp(repoPath, fullRepackTimestampFilename+"-*")
	if err != nil {
		return err
	}
	defer func() {
		// If the file still exists we try to remove it. We don't care for the error though:
		// when we have renamed the file into place it will fail, and that is expected. On
		// the other hand, if we didn't yet rename the file we know that we would already
		// return an error anyway. So in both cases, the error would be irrelevant.
		_ = os.Remove(f.Name())
	}()

	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Chtimes(f.Name(), timestamp, timestamp); err != nil {
		return err
	}

	if err := os.Rename(f.Name(), timestampPath); err != nil {
		return err
	}

	return nil
}

// FullRepackTimestamp reads the timestamp that indicates when a repository has last been fully
// repacked. If no such timestamp exists, the zero timestamp is returned without an error.
func FullRepackTimestamp(repoPath string) (time.Time, error) {
	stat, err := os.Stat(filepath.Join(repoPath, fullRepackTimestampFilename))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// It's fine if the file doesn't exist. We just leave the timestamp at the
			// zero date in that case.
			return time.Time{}, nil
		}

		return time.Time{}, err
	}

	return stat.ModTime(), nil
}

// PackfilesCount returns the number of packfiles a repository has.
func PackfilesCount(ctx context.Context, repo *localrepo.Repo) (uint64, error) {
	packfilesInfo, err := PackfilesInfoForRepository(ctx, repo)
	if err != nil {
		return 0, fmt.Errorf("deriving packfiles info: %w", err)
	}

	return packfilesInfo.Count, nil
}

// LooseObjects returns the number of loose objects that are not in a packfile.
func LooseObjects(ctx context.Context, repo *localrepo.Repo) (uint64, error) {
	objectsInfo, err := LooseObjectsInfoForRepository(ctx, repo, time.Now())
	if err != nil {
		return 0, err
	}

	return objectsInfo.Count, nil
}

// LogRepositoryInfo derives RepositoryInfo and calls its `Log()` function, if successful. Otherwise
// it logs an error.
func LogRepositoryInfo(ctx context.Context, logger log.Logger, repo *localrepo.Repo) {
	repoInfo, err := RepositoryInfoForRepository(ctx, repo)
	if err != nil {
		logger.WithError(err).WarnContext(ctx, "failed reading repository info")
	} else {
		repoInfo.Log(ctx, logger)
	}
}

// RepositoryInfo contains information about the repository.
type RepositoryInfo struct {
	// IsObjectPool determines whether the repository is an object pool or a normal repository.
	IsObjectPool bool `json:"is_object_pool"`
	// LooseObjects contains information about loose objects.
	LooseObjects LooseObjectsInfo `json:"loose_objects"`
	// Packfiles contains information about packfiles.
	Packfiles PackfilesInfo `json:"packfiles"`
	// References contains information about the repository's references.
	References ReferencesInfo `json:"references"`
	// CommitGraph contains information about the repository's commit-graphs.
	CommitGraph CommitGraphInfo `json:"commit_graph"`
	// Alternates contains information about alternate object directories.
	Alternates AlternatesInfo `json:"alternates"`
}

// RepositoryInfoForRepository computes the RepositoryInfo for a repository.
func RepositoryInfoForRepository(ctx context.Context, repo *localrepo.Repo) (RepositoryInfo, error) {
	var info RepositoryInfo
	var err error

	repoPath, err := repo.Path(ctx)
	if err != nil {
		return RepositoryInfo{}, err
	}

	info.IsObjectPool = storage.IsPoolRepository(repo)

	info.LooseObjects, err = LooseObjectsInfoForRepository(ctx, repo, time.Now().Add(StaleObjectsGracePeriod))
	if err != nil {
		return RepositoryInfo{}, fmt.Errorf("counting loose objects: %w", err)
	}

	info.Packfiles, err = PackfilesInfoForRepository(ctx, repo)
	if err != nil {
		return RepositoryInfo{}, fmt.Errorf("counting packfiles: %w", err)
	}

	info.References, err = ReferencesInfoForRepository(ctx, repo)
	if err != nil {
		return RepositoryInfo{}, fmt.Errorf("checking references: %w", err)
	}

	info.CommitGraph, err = CommitGraphInfoForRepository(repoPath)
	if err != nil {
		return RepositoryInfo{}, fmt.Errorf("checking commit-graph info: %w", err)
	}

	info.Alternates, err = AlternatesInfoForRepository(repoPath)
	if err != nil {
		return RepositoryInfo{}, fmt.Errorf("reading alterantes: %w", err)
	}

	return info, nil
}

// Log logs the repository information as a structured entry under the `repository_info` field.
func (i RepositoryInfo) Log(ctx context.Context, logger log.Logger) {
	logger.WithField("repository_info", i).InfoContext(ctx, "repository info")
}

// ReferencesInfo contains information about references.
type ReferencesInfo struct {
	// LooseReferencesCount is the number of unpacked, loose references that exist.
	LooseReferencesCount uint64 `json:"loose_references_count"`
	// PackedReferencesSize is the size of the packed-refs file in bytes.
	PackedReferencesSize uint64 `json:"packed_references_size"`
	// ReftableTables contains details of individual table files.
	ReftableTables []ReftableTable `json:"reftable_tables"`
	// ReftableUnrecognizedFilesCount is the number of files under the `reftables/`
	// directory that shouldn't exist, according to the entries in `tables.list`.
	ReftableUnrecognizedFilesCount uint64 `json:"reftable_unrecognized_files"`
	// ReferenceBackendName denotes the reference backend name of the repo.
	ReferenceBackendName string `json:"reference_backend"`
}

// ReftableTable contains information about an individual reftable table.
type ReftableTable struct {
	// Size is the size in bytes.
	Size uint64 `json:"size"`
	// UpdateIndexMin is the min_update_index of the reftable table. This is derived
	// from the filename only.
	UpdateIndexMin uint64 `json:"update_index_min"`
	// UpdateIndexMax is the max_update_index of the reftable table. This is derived
	// from the filename only.
	UpdateIndexMax uint64 `json:"update_index_max"`
}

// ReferencesInfoForRepository derives information about references in the repository.
func ReferencesInfoForRepository(ctx context.Context, repo *localrepo.Repo) (ReferencesInfo, error) {
	repoPath, err := repo.Path(ctx)
	if err != nil {
		return ReferencesInfo{}, fmt.Errorf("getting repository path: %w", err)
	}

	var info ReferencesInfo
	referenceBackend, err := repo.ReferenceBackend(ctx)
	if err != nil {
		return ReferencesInfo{}, fmt.Errorf("reference backend: %w", err)
	}
	info.ReferenceBackendName = referenceBackend.Name

	switch info.ReferenceBackendName {
	case git.ReferenceBackendFiles.Name:
		refsPath := filepath.Join(repoPath, "refs")
		if err := filepath.WalkDir(refsPath, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				// It may happen that references got deleted concurrently. This is fine and expected, so we just
				// ignore any such errors.
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}

				return err
			}

			if !entry.IsDir() {
				info.LooseReferencesCount++
			}

			return nil
		}); err != nil {
			return ReferencesInfo{}, fmt.Errorf("counting loose refs: %w", err)
		}

		if stat, err := os.Stat(filepath.Join(repoPath, "packed-refs")); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return ReferencesInfo{}, fmt.Errorf("getting packed-refs size: %w", err)
			}
		} else {
			info.PackedReferencesSize = uint64(stat.Size())
		}
	case git.ReferenceBackendReftables.Name:
		refsPath := filepath.Join(repoPath, "reftable")

		tablesList, err := os.Open(filepath.Join(refsPath, "tables.list"))
		if err != nil {
			return ReferencesInfo{}, fmt.Errorf("open tables.list: %w", err)
		}
		defer tablesList.Close()

		// Track the expected files under the `reftable/` directory.
		reftableRecognizedFiles := map[string]struct{}{
			"tables.list":      {},
			"tables.list.lock": {},
		}

		scanner := bufio.NewScanner(tablesList)
		scanner.Split(bufio.ScanLines)
		for scanner.Scan() {
			reftableName := scanner.Text()

			reftableRecognizedFiles[reftableName] = struct{}{}

			reftableStat, err := os.Stat(filepath.Join(refsPath, reftableName))
			if err != nil {
				return ReferencesInfo{}, fmt.Errorf("stat reftable table file: %w", err)
			}

			rt := ReftableTable{
				Size: uint64(reftableStat.Size()),
			}

			// Attempt to extract the min and max update indices from the reftable file name.
			//
			// e.g. 0x000000000001-0x000000000001-b54f3b59.ref would result in the following matches:
			//	- 000000000001 (UpdateIndexMin)
			//	- 000000000001 (UpdateIndexMax)
			//
			// See the reftable documentation at https://www.git-scm.com/docs/reftable#_layout for more
			// information.
			matches := regexp.MustCompile("0x([0-9]{12})-").FindAllStringSubmatch(reftableName, 2)
			if len(matches) != 2 || len(matches[0]) != 2 || len(matches[1]) != 2 {
				return ReferencesInfo{}, fmt.Errorf("reftable name %q malformed", reftableName)
			}

			// Skip error checking due to regexp matching above.
			rt.UpdateIndexMin, _ = strconv.ParseUint(matches[0][1], 10, 0)
			rt.UpdateIndexMax, _ = strconv.ParseUint(matches[1][1], 10, 0)

			info.ReftableTables = append(info.ReftableTables, rt)
		}

		reftableDir, err := os.ReadDir(refsPath)
		if err != nil {
			return ReferencesInfo{}, fmt.Errorf("read reftable dir: %w", err)
		}

		for _, fname := range reftableDir {
			if _, ok := reftableRecognizedFiles[fname.Name()]; !ok {
				info.ReftableUnrecognizedFilesCount++
			}
		}
	}

	return info, nil
}

// LooseObjectsInfo contains information about loose objects.
type LooseObjectsInfo struct {
	// Count is the number of loose objects.
	Count uint64 `json:"count"`
	// Size is the total size of all loose objects in bytes.
	Size uint64 `json:"size"`
	// StaleCount is the number of stale loose objects when taking into account the specified cutoff
	// date.
	StaleCount uint64 `json:"stale_count"`
	// StaleSize is the total size of stale loose objects when taking into account the specified
	// cutoff date.
	StaleSize uint64 `json:"stale_size"`
	// GarbageCount is the number of garbage files in the loose-objects shards.
	GarbageCount uint64 `json:"garbage_count"`
	// GarbageSize is the total size of garbage in the loose-objects shards.
	GarbageSize uint64 `json:"garbage_size"`
}

// LooseObjectsInfoForRepository derives information about loose objects in the repository. If a
// cutoff date is given, then this function will only take into account objects which are older than
// the given point in time.
func LooseObjectsInfoForRepository(ctx context.Context, repo *localrepo.Repo, cutoffDate time.Time) (LooseObjectsInfo, error) {
	repoPath, err := repo.Path(ctx)
	if err != nil {
		return LooseObjectsInfo{}, fmt.Errorf("getting repository path: %w", err)
	}

	var info LooseObjectsInfo
	for i := 0; i <= 0xFF; i++ {
		entries, err := os.ReadDir(filepath.Join(repoPath, "objects", fmt.Sprintf("%02x", i)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			return LooseObjectsInfo{}, fmt.Errorf("reading loose object shard: %w", err)
		}

		for _, entry := range entries {
			entryInfo, err := entry.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}

				return LooseObjectsInfo{}, fmt.Errorf("reading object info: %w", err)
			}

			if !isValidLooseObjectName(entry.Name()) {
				info.GarbageCount++
				info.GarbageSize += uint64(entryInfo.Size())
				continue
			}

			// Note: we don't `continue` here as we count stale objects into the total
			// number of objects.
			if entryInfo.ModTime().Before(cutoffDate) {
				info.StaleCount++
				info.StaleSize += uint64(entryInfo.Size())
			}

			info.Count++
			info.Size += uint64(entryInfo.Size())
		}
	}

	return info, nil
}

func isValidLooseObjectName(s string) bool {
	for _, c := range []byte(s) {
		if strings.IndexByte("0123456789abcdef", c) < 0 {
			return false
		}
	}
	return true
}

// PackfilesInfo contains information about packfiles.
type PackfilesInfo struct {
	// Count is the number of all packfiles, including stale and kept ones.
	Count uint64 `json:"count"`
	// Size is the total size of all packfiles in bytes, including stale and kept ones.
	Size uint64 `json:"size"`
	// ReverseIndexCount is the number of reverse indices.
	ReverseIndexCount uint64 `json:"reverse_index_count"`
	// CruftCount is the number of cruft packfiles which have a .mtimes file.
	CruftCount uint64 `json:"cruft_count"`
	// CruftSize is the size of cruft packfiles which have a .mtimes file.
	CruftSize uint64 `json:"cruft_size"`
	// KeepCount is the number of .keep packfiles.
	KeepCount uint64 `json:"keep_count"`
	// KeepSize is the size of .keep packfiles.
	KeepSize uint64 `json:"keep_size"`
	// GarbageCount is the number of garbage files.
	GarbageCount uint64 `json:"garbage_count"`
	// GarbageSize is the total size of all garbage files in bytes.
	GarbageSize uint64 `json:"garbage_size"`
	// Bitmap contains information about the bitmap, if any exists.
	Bitmap BitmapInfo `json:"bitmap"`
	// MultiPackIndex confains information about the multi-pack-index, if any exists.
	MultiPackIndex MultiPackIndexInfo `json:"multi_pack_index"`
	// MultiPackIndexBitmap contains information about the bitmap for the multi-pack-index, if
	// any exists.
	MultiPackIndexBitmap BitmapInfo `json:"multi_pack_index_bitmap"`
	// LastFullRepack indicates the last date at which a full repack has been performed. If the
	// date cannot be determined then this file is set to the zero time.
	LastFullRepack time.Time `json:"last_full_repack"`
}

// PackfilesInfoForRepository derives various information about packfiles for the given repository.
func PackfilesInfoForRepository(ctx context.Context, repo *localrepo.Repo) (PackfilesInfo, error) {
	repoPath, err := repo.Path(ctx)
	if err != nil {
		return PackfilesInfo{}, fmt.Errorf("getting repository path: %w", err)
	}
	packfilesPath := filepath.Join(repoPath, "objects", "pack")

	entries, err := os.ReadDir(packfilesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PackfilesInfo{}, nil
		}

		return PackfilesInfo{}, err
	}

	packfilesMetadata := classifyPackfiles(entries)

	var info PackfilesInfo
	for _, entry := range entries {
		entryName := entry.Name()

		switch {
		case hasPrefixAndSuffix(entryName, "pack-", ".pack"):
			size, err := entrySize(entry)
			if err != nil {
				return PackfilesInfo{}, fmt.Errorf("getting packfile size: %w", err)
			}

			info.Count++
			info.Size += size

			metadata := packfilesMetadata[entryName]
			switch {
			case metadata.hasKeep:
				info.KeepCount++
				info.KeepSize += size
			case metadata.hasMtimes:
				info.CruftCount++
				info.CruftSize += size
			}
		case hasPrefixAndSuffix(entryName, "pack-", ".idx"):
			// We ignore normal indices as every packfile would have one anyway, or
			// otherwise the repository would be corrupted.
		case hasPrefixAndSuffix(entryName, "pack-", ".keep"):
			// We classify .keep files above.
		case hasPrefixAndSuffix(entryName, "pack-", ".mtimes"):
			// We classify .mtimes files above.
		case hasPrefixAndSuffix(entryName, "pack-", ".rev"):
			info.ReverseIndexCount++
		case hasPrefixAndSuffix(entryName, "pack-", ".bitmap"):
			bitmap, err := BitmapInfoForPath(filepath.Join(packfilesPath, entryName))
			if err != nil {
				return PackfilesInfo{}, fmt.Errorf("reading bitmap info: %w", err)
			}

			info.Bitmap = bitmap
		case entryName == "multi-pack-index":
			midxInfo, err := MultiPackIndexInfoForPath(filepath.Join(packfilesPath, entryName))
			if err != nil {
				return PackfilesInfo{}, fmt.Errorf("reading multi-pack-index: %w", err)
			}

			info.MultiPackIndex = midxInfo
		case hasPrefixAndSuffix(entryName, "multi-pack-index-", ".bitmap"):
			bitmap, err := BitmapInfoForPath(filepath.Join(packfilesPath, entryName))
			if err != nil {
				return PackfilesInfo{}, fmt.Errorf("reading multi-pack-index bitmap info: %w", err)
			}

			info.MultiPackIndexBitmap = bitmap
		default:
			size, err := entrySize(entry)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					// Unrecognized files may easily be temporary files written
					// by Git. It is expected that these may get concurrently
					// removed, so we just ignore the case where they've gone
					// missing.
					continue
				}

				return PackfilesInfo{}, fmt.Errorf("getting garbage size: %w", err)
			}

			info.GarbageCount++
			info.GarbageSize += size
		}
	}

	lastFullRepack, err := FullRepackTimestamp(repoPath)
	if err != nil {
		return PackfilesInfo{}, fmt.Errorf("reading full-repack timestamp: %w", err)
	}
	info.LastFullRepack = lastFullRepack

	return info, nil
}

type packfileMetadata struct {
	hasKeep, hasMtimes bool
}

// classifyPackfiles classifies all directory entries that look like packfiles and derives whether
// they have specific metadata or not. It returns a map of packfile names with the respective
// metadata that has been found.
func classifyPackfiles(entries []fs.DirEntry) map[string]packfileMetadata {
	packfileInfos := map[string]packfileMetadata{}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "pack-") {
			continue
		}

		extension := filepath.Ext(entry.Name())
		packfileName := strings.TrimSuffix(entry.Name(), extension) + ".pack"

		packfileMetadata := packfileInfos[packfileName]
		switch extension {
		case ".keep":
			packfileMetadata.hasKeep = true
		case ".mtimes":
			packfileMetadata.hasMtimes = true
		}
		packfileInfos[packfileName] = packfileMetadata
	}

	return packfileInfos
}

func entrySize(entry fs.DirEntry) (uint64, error) {
	entryInfo, err := entry.Info()
	if err != nil {
		return 0, fmt.Errorf("getting file info: %w", err)
	}

	if entryInfo.Size() >= 0 {
		return uint64(entryInfo.Size()), nil
	}

	return 0, nil
}

func hasPrefixAndSuffix(s, prefix, suffix string) bool {
	return strings.HasPrefix(s, prefix) && strings.HasSuffix(s, suffix)
}

// AlternatesInfo contains information about altenrate object directories the repository is linked
// to.
type AlternatesInfo struct {
	// Exists determines whether the `info/alternates` file exists or not.
	Exists bool `json:"exists"`
	// Paths contains the list of paths to object directories that the repository is linked to.
	ObjectDirectories []string `json:"object_directories,omitempty"`
	// LastModified is the time when the alternates file has last been modified. Has the zero
	// value when the alternates file doesn't exist.
	LastModified time.Time `json:"last_modified"`
	// repoPath is the path to the repository that corresponds with the alternates file. It is only
	// used when converting the object directory paths to be absolute, thus it is not exposed.
	repoPath string
}

// AbsoluteObjectDirectories converts AlternatesInfo ObjectDirectories to absolute paths.
func (a AlternatesInfo) AbsoluteObjectDirectories() []string {
	alternatePaths := make([]string, 0, len(a.ObjectDirectories))
	for _, path := range a.ObjectDirectories {
		if filepath.IsAbs(path) {
			alternatePaths = append(alternatePaths, path)
		} else {
			alternatePaths = append(alternatePaths, filepath.Join(a.repoPath, "objects", path))
		}
	}

	return alternatePaths
}

// AlternatesFilePath returns the 'objects/info/alternates'
// file's path in the repository.
func AlternatesFilePath(repoPath string) string {
	return filepath.Join(repoPath, "objects", "info", "alternates")
}

// ReadAlternatesFile returns the repository's alternate object directory paths
// from '<repo>/objects/infop/alternates' and returns them. Returns a wrapped
// fs.ErrNotExist if the file doesn't exist.
func ReadAlternatesFile(repoPath string) ([]string, error) {
	file, err := os.Open(AlternatesFilePath(repoPath))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer file.Close()

	var alternatePaths []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()

		switch {
		case len(line) == 0:
			// Empty lines are skipped by Git.
			continue
		case bytes.HasPrefix(line, []byte("#")):
			// Lines starting with a '#' are comments and thus need to be skipped.
			continue
		default:
			alternatePaths = append(alternatePaths, scanner.Text())
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning alternate paths: %w", err)
	}

	return alternatePaths, nil
}

// AlternatesInfoForRepository reads the alternates file and returns information on it. This
// function does not return an error in case the alternates file doesn't exist. Existence can be
// checked via the `Exists` field of the returned `AlternatesInfo` structure.
func AlternatesInfoForRepository(repoPath string) (AlternatesInfo, error) {
	alternatePaths, err := ReadAlternatesFile(repoPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return AlternatesInfo{Exists: false}, nil
		}

		return AlternatesInfo{}, fmt.Errorf("read alternates file: %w", err)
	}

	stat, err := os.Stat(AlternatesFilePath(repoPath))
	if err != nil {
		return AlternatesInfo{}, fmt.Errorf("stat: %w", err)
	}

	return AlternatesInfo{
		Exists:            true,
		ObjectDirectories: alternatePaths,
		LastModified:      stat.ModTime(),
		repoPath:          repoPath,
	}, nil
}

// BitmapInfo contains information about a packfile or multi-pack-index bitmap.
type BitmapInfo struct {
	// Exists indicates whether the bitmap exists. This field would usually always be `true`
	// when read via `BitmapInfoForPath()`, but helps when the bitmap info is embedded into
	// another structure where it may only be conditionally read.
	Exists bool `json:"exists"`
	// Version is the version of the bitmap. Currently, this is expected to always be 1.
	Version uint16 `json:"version"`
	// HasHashCache indicates whether the name hash cache extension exists in the bitmap. This
	// extension records hashes of the path at which trees or blobs are found at the time of
	// writing the packfile so that it becomes possible to quickly find objects stored at the
	// same path. This mechanism is fed into the delta compression machinery to make the delta
	// heuristics more effective.
	HasHashCache bool `json:"has_hash_cache"`
	// HasLookupTable indicates whether the lookup table exists in the bitmap. Lookup tables
	// allow to defer loading bitmaps until required and thus speed up read-only bitmap
	// preparations.
	HasLookupTable bool `json:"has_lookup_table"`
}

// BitmapInfoForPath reads the bitmap at the given path and returns information on that bitmap.
func BitmapInfoForPath(path string) (BitmapInfo, error) {
	// The bitmap header is defined in
	// https://github.com/git/git/blob/master/Documentation/technical/bitmap-format.txt.
	bitmapHeader := []byte{
		0, 0, 0, 0, // 4-byte signature
		0, 0, // 2-byte version number in network byte order
		0, 0, // 2-byte flags in network byte order
	}

	file, err := os.Open(path)
	if err != nil {
		return BitmapInfo{}, fmt.Errorf("opening bitmap: %w", err)
	}
	defer file.Close()

	if _, err := io.ReadFull(file, bitmapHeader); err != nil {
		return BitmapInfo{}, fmt.Errorf("reading bitmap header: %w", err)
	}

	if !bytes.Equal(bitmapHeader[0:4], []byte{'B', 'I', 'T', 'M'}) {
		return BitmapInfo{}, fmt.Errorf("invalid bitmap signature: %q", string(bitmapHeader[0:4]))
	}

	version := binary.BigEndian.Uint16(bitmapHeader[4:6])
	if version != 1 {
		return BitmapInfo{}, fmt.Errorf("unsupported version: %d", version)
	}

	flags := binary.BigEndian.Uint16(bitmapHeader[6:8])

	return BitmapInfo{
		Exists:         true,
		Version:        version,
		HasHashCache:   flags&0x4 == 0x4,
		HasLookupTable: flags&0x10 == 0x10,
	}, nil
}

// MultiPackIndexInfo contains information about a multi-pack-index.
type MultiPackIndexInfo struct {
	// Exists determines whether the multi-pack-index exists or not.
	Exists bool `json:"exists"`
	// Version is the version of the multi-pack-index. Currently, Git only recognizes version 1.
	Version uint8 `json:"version"`
	// PackfileCount is the count of packfiles that the multi-pack-index tracks.
	PackfileCount uint64 `json:"packfile_count"`
}

// MultiPackIndexInfoForPath reads the multi-pack-index at the given path and returns information on
// it. Returns an error in case the file cannot be read or in case its format is not understood.
func MultiPackIndexInfoForPath(path string) (MultiPackIndexInfo, error) {
	// Please refer to gitformat-pack(5) for the definition of the multi-pack-index header.
	midxHeader := []byte{
		0, 0, 0, 0, // 4-byte signature
		0,          // 1-byte version number
		0,          // 1-byte object ID version
		0,          // 1-byte number of chunks
		0,          // 1-byte number of base multi-pack-index files
		0, 0, 0, 0, // 4-byte number of packfiles
	}

	file, err := os.Open(path)
	if err != nil {
		return MultiPackIndexInfo{}, fmt.Errorf("opening multi-pack-index: %w", err)
	}
	defer file.Close()

	if _, err := io.ReadFull(file, midxHeader); err != nil {
		return MultiPackIndexInfo{}, fmt.Errorf("reading header: %w", err)
	}

	if !bytes.Equal(midxHeader[0:4], []byte{'M', 'I', 'D', 'X'}) {
		return MultiPackIndexInfo{}, fmt.Errorf("invalid signature: %q", string(midxHeader[0:4]))
	}

	version := midxHeader[4]
	if version != 1 {
		return MultiPackIndexInfo{}, fmt.Errorf("invalid version: %d", version)
	}

	baseFiles := midxHeader[7]
	if baseFiles != 0 {
		return MultiPackIndexInfo{}, fmt.Errorf("unsupported number of base files: %d", baseFiles)
	}

	packfileCount := binary.BigEndian.Uint32(midxHeader[8:12])

	return MultiPackIndexInfo{
		Exists:        true,
		Version:       version,
		PackfileCount: uint64(packfileCount),
	}, nil
}
