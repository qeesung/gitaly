package housekeeping

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/context"
)

var whiteSpaceRegExp = regexp.MustCompile("^([\\w\\/]+)\\s+(f|d)\\s+(\\d+)\\s+(\\w+)\\s+(keep|delete)$")

type op func() error

type fixture struct {
	rootPath      string
	deleteEntries []string
}

func (f *fixture) check(t *testing.T) {
	for _, entry := range f.deleteEntries {
		_, err := os.Stat(entry)
		if err == nil {
			t.Errorf("Expected %v to have been deleted.", entry)
		}
	}
}

func parse(directoryStructure string) (*fixture, error) {
	tmpDir, err := ioutil.TempDir("", "test")
	if err != nil {
		return nil, err
	}

	result := &fixture{rootPath: tmpDir}
	lines := strings.Split(directoryStructure, "\n")

	// Sequenced operations
	ops := []op{}

	// First pass
	for _, line := range lines {
		line := strings.Trim(line, " \t")
		if line == "" {
			continue
		}

		matches := whiteSpaceRegExp.FindStringSubmatch(string(line))
		if len(matches) < 2 {
			return result, fmt.Errorf("Invalid line %v, %v", line, matches)
		}

		filename := filepath.Join(tmpDir, matches[1])
		isDirectory := matches[2] == "d"

		if isDirectory {
			err := os.Mkdir(filename, 0700)
			if err != nil {
				return result, err
			}
		}

		f, err := os.OpenFile(filename, os.O_RDONLY|os.O_CREATE, 0700)
		f.Close()
		if err != nil {
			return result, err
		}

		// Perform chmod and chtimes in reverse order
		mod := func() error {
			mode, err := strconv.ParseInt(matches[3], 0, 32)
			if err != nil {
				return err
			}

			err = os.Chmod(filename, os.FileMode(mode))
			if err != nil {
				return err
			}

			duration, err := time.ParseDuration(matches[4])
			if err != nil {
				return err
			}

			filetime := time.Now().Add(-duration)
			return os.Chtimes(filename, filetime, filetime)
		}
		ops = append([]op{mod}, ops...)

		if matches[5] == "delete" {
			result.deleteEntries = append(result.deleteEntries, filename)
		}
	}

	for _, op := range ops {
		err := op()
		if err != nil {
			return result, err
		}
	}

	return result, nil
}

func TestPerformHousekeeping(t *testing.T) {
	tests := []struct {
		name        string
		directoryIn string
		wantErr     bool
	}{
		{
			name: "clean",
			directoryIn: `
				a     f 0700   24h   keep
				b    f 0700   24h    keep
				c    f 0700   24h    keep
			`,
			wantErr: false,
		},
		{
			name: "emptyperms",
			directoryIn: `
				tmp_a f 0000   24h    delete
				b     f 0700   24h    keep
			`,
			wantErr: false,
		},
		{
			name: "oldtempfile",
			directoryIn: `
				tmp_b  f 0770   240h  delete
				b      f 0700   24h   keep
			`,
			wantErr: false,
		},
		{
			name: "oldtempfile",
			directoryIn: `
				tmp_b  f 0770   240h  delete
				b      f 0700   24h   keep
			`,
			wantErr: false,
		},
		{
			name: "subdir temp file",
			directoryIn: `
				a         d 0770   240h  keep
				a/tmp_b   f 0700   240h  delete
			`,
			wantErr: false,
		},
		{
			name: "inaccessible tmp directory",
			directoryIn: `
				tmp_a         d 0000   240h  delete
				tmp_a/tmp_b   f 0700   240h  delete
			`,
			wantErr: false,
		},
		{
			name: "deeply nested inaccessible tmp directory",
			directoryIn: `
				tmp_a       d 0000   240h    delete
				tmp_a/b     d 0000   24h     delete
				tmp_a/b/c   f 0000   24h     delete
			`,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := parse(tt.directoryIn)

			if f != nil {
				defer os.RemoveAll(f.rootPath)
			}

			if err != nil {
				t.Errorf("Setup failed: %v", err)
			}

			if err = PerformHousekeeping(context.Background(), f.rootPath); (err != nil) != tt.wantErr {
				t.Errorf("PerformHousekeeping() error = %v, wantErr %v", err, tt.wantErr)
			}

			f.check(t)
		})
	}
}

func Test_shouldUnlink(t *testing.T) {
	type args struct {
		path    string
		modTime time.Time
		mode    os.FileMode
		err     error
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "regular file",
			args: args{
				path:    "/tmp/objects",
				modTime: time.Now().Add(-1 * time.Hour),
				mode:    0700,
				err:     nil,
			},
			want: false,
		},
		{
			name: "directory",
			args: args{
				path:    "/tmp/",
				modTime: time.Now().Add(-1 * time.Hour),
				mode:    0770,
				err:     nil,
			},
			want: false,
		},
		{
			name: "recent time file",
			args: args{
				path:    "/tmp/tmp_DELETEME",
				modTime: time.Now().Add(-1 * time.Hour),
				mode:    0600,
				err:     nil,
			},
			want: false,
		},
		{
			name: "old temp file",
			args: args{
				path:    "/tmp/tmp_DELETEME",
				modTime: time.Now().Add(-8 * 24 * time.Hour),
				mode:    0600,
				err:     nil,
			},
			want: true,
		},
		{
			name: "old temp file",
			args: args{
				path:    "/tmp/tmp_DELETEME",
				modTime: time.Now().Add(-1 * time.Hour),
				mode:    0000,
				err:     nil,
			},
			want: true,
		},
		{
			name: "inaccessible file",
			args: args{
				path:    "/tmp/tmp_DELETEME",
				modTime: time.Now().Add(-1 * time.Hour),
				mode:    0000,
				err:     fmt.Errorf("file inaccessible"),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUnlink(tt.args.path, tt.args.modTime, tt.args.mode, tt.args.err); got != tt.want {
				t.Errorf("shouldUnlink() = %v, want %v", got, tt.want)
			}
		})
	}
}
