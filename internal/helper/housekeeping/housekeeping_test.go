package housekeeping

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
)

const delete = true
const keep = false

type entry interface {
	create(t *testing.T, parent string)
	validate(t *testing.T, parent string)
}

// fileEntry is an entry implementation for a file
type fileEntry struct {
	name    string
	mode    os.FileMode
	age     time.Duration
	deleted bool
}

func (f *fileEntry) create(t *testing.T, parent string) {
	filename := filepath.Join(parent, f.name)
	ff, err := os.OpenFile(filename, os.O_RDONLY|os.O_CREATE, 0700)
	assert.NoError(t, err, "file creation failed: %v", filename)
	ff.Close()

	f.chmod(t, filename)
	f.chtimes(t, filename)
}

func (f *fileEntry) validate(t *testing.T, parent string) {
	filename := filepath.Join(parent, f.name)
	f.checkExistence(t, filename)
}

func (f *fileEntry) chmod(t *testing.T, filename string) {
	fmt.Printf("chmod %v,%v", filename, f.mode)
	err := os.Chmod(filename, f.mode)
	assert.NoError(t, err, "chmod failed")
}

func (f *fileEntry) chtimes(t *testing.T, filename string) {
	filetime := time.Now().Add(-f.age)
	err := os.Chtimes(filename, filetime, filetime)
	assert.NoError(t, err, "chtimes failed")
}

func (f *fileEntry) checkExistence(t *testing.T, filename string) {
	_, err := os.Stat(filename)
	if err == nil && f.deleted {
		t.Errorf("Expected %v to have been deleted.", filename)
	} else if err != nil && !f.deleted {
		t.Errorf("Expected %v to not have been deleted.", filename)
	}
}

// dirEntry is an entry implementation for a directory. A file with entries
type dirEntry struct {
	fileEntry
	entries []entry
}

func (d *dirEntry) create(t *testing.T, parent string) {
	dirname := filepath.Join(parent, d.name)
	err := os.Mkdir(dirname, 0700)
	assert.NoError(t, err, "mkdir failed: %v", dirname)

	for _, e := range d.entries {
		e.create(t, dirname)
	}

	// Apply permissions and times after the children have been created
	d.chmod(t, dirname)
	d.chtimes(t, dirname)
}

func (d *dirEntry) validate(t *testing.T, parent string) {
	dirname := filepath.Join(parent, d.name)
	d.checkExistence(t, dirname)

	for _, e := range d.entries {
		e.validate(t, dirname)
	}
}

func f(name string, mode os.FileMode, age time.Duration, deleted bool) entry {
	return &fileEntry{name, mode, age, deleted}
}

func d(name string, mode os.FileMode, age time.Duration, deleted bool, entries []entry) entry {
	return &dirEntry{fileEntry{name, mode, age, deleted}, entries}
}

func TestPerformHousekeeping(t *testing.T) {
	tests := []struct {
		name    string
		entries []entry
		wantErr bool
	}{
		{
			name: "clean",
			entries: []entry{
				f("a", os.FileMode(0700), 24*time.Hour, keep),
				f("b", os.FileMode(0700), 24*time.Hour, keep),
				f("c", os.FileMode(0700), 24*time.Hour, keep),
			},
			wantErr: false,
		},
		{
			name: "emptyperms",
			entries: []entry{
				f("b", os.FileMode(0700), 24*time.Hour, keep),
				f("tmp_a", os.FileMode(0000), 2*time.Hour, delete),
			},
			wantErr: false,
		},
		{
			name: "emptytempdir",
			entries: []entry{
				d("tmp_d", os.FileMode(0000), 24*time.Hour, delete, []entry{}),
				f("b", os.FileMode(0700), 24*time.Hour, keep),
			},
			wantErr: false,
		},
		{
			name: "oldtempfile",
			entries: []entry{
				f("tmp_a", os.FileMode(0770), 240*time.Hour, delete),
				f("b", os.FileMode(0700), 24*time.Hour, keep),
			},
			wantErr: false,
		},
		{
			name: "subdir temp file",
			entries: []entry{
				d("a", os.FileMode(0770), 240*time.Hour, keep, []entry{
					f("tmp_b", os.FileMode(0700), 240*time.Hour, delete),
				}),
			},
			wantErr: false,
		},
		{
			name: "inaccessible tmp directory",
			entries: []entry{
				d("tmp_a", os.FileMode(0000), 240*time.Hour, delete, []entry{
					f("tmp_b", os.FileMode(0700), 240*time.Hour, delete),
				}),
			},
			wantErr: false,
		},
		{
			name: "deeply nested inaccessible tmp directory",
			entries: []entry{
				d("tmp_a", os.FileMode(0000), 240*time.Hour, delete, []entry{
					d("tmp_a", os.FileMode(0000), 24*time.Hour, delete, []entry{
						f("tmp_b", os.FileMode(0000), 24*time.Hour, delete),
					}),
				}),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootPath, err := ioutil.TempDir("", "test")
			assert.NoError(t, err, "TempDir creation failed")
			defer os.RemoveAll(rootPath)

			for _, e := range tt.entries {
				e.create(t, rootPath)
			}

			if err = Perform(context.Background(), rootPath); (err != nil) != tt.wantErr {
				t.Errorf("PerformHousekeeping() error = %v, wantErr %v", err, tt.wantErr)
			}

			for _, e := range tt.entries {
				e.validate(t, rootPath)
			}
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
