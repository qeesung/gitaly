package packfile

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/command"
)

const sumSize = sha1.Size

var (
	idxFileRegex = regexp.MustCompile(`\A(.*/pack-)([0-9a-f]{40})\.idx\z`)
)

type Index struct {
	ID            string
	packBase      string
	Objects       []*Object
	PackfileOrder []*Object
	oidMap        map[string]*Object
	*IndexBitmap
}

func ReadIndex(idxPath string) (*Index, error) {
	reMatches := idxFileRegex.FindStringSubmatch(idxPath)
	if len(reMatches) == 0 {
		return nil, fmt.Errorf("invalid idx filename: %q", idxPath)
	}

	idx := &Index{
		packBase: reMatches[1] + reMatches[2],
		ID:       reMatches[2],
	}

	f, err := os.Open(idx.packBase + ".idx")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(-2*sumSize, io.SeekEnd); err != nil {
		return nil, err
	}

	packID, err := readN(f, sumSize)
	if err != nil {
		return nil, err
	}

	if actual := hex.EncodeToString(packID); idx.ID != actual {
		return nil, fmt.Errorf("expected idx to go with pack %s, got %s", idx.ID, actual)
	}

	count, err := idx.numPackObjects()
	if err != nil {
		return nil, err
	}

	// TODO use a data structure other than a Go slice to hold the index
	// entries? Go slices use int as their index type, and int may not be
	// able to hold MaxUint32.
	if count > math.MaxInt32 {
		return nil, fmt.Errorf("too many objects in to fit in Go slice: %d", count)
	}
	idx.Objects = make([]*Object, count)
	idx.oidMap = make(map[string]*Object, count)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	showIndex, err := command.New(ctx, exec.Command("git", "show-index"), f, nil, nil)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(showIndex)
	i := 0
	for ; scanner.Scan(); i++ {
		line := scanner.Text()
		split := strings.SplitN(line, " ", 3)
		if len(split) != 3 {
			return nil, fmt.Errorf("unable to parse show-index line: %q", line)
		}

		offset, err := strconv.ParseUint(split[0], 10, 64)
		if err != nil {
			return nil, err
		}
		oid := split[1]

		idx.Objects[i] = &Object{OID: oid, Offset: offset}
		idx.oidMap[oid] = idx.Objects[i]
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if err := showIndex.Wait(); err != nil {
		return nil, err
	}

	if i != len(idx.Objects) {
		return nil, fmt.Errorf("expected %d objects in output of git show-index, got %d", len(idx.Objects), i)
	}

	return idx, nil
}

func (idx *Index) GetObject(oid string) (*Object, bool) {
	obj, ok := idx.oidMap[oid]
	return obj, ok
}

func (idx *Index) numPackObjects() (uint32, error) {
	f, err := idx.openPack()
	if err != nil {
		return 0, err
	}
	defer f.Close()

	const sizeOffset = 8
	if _, err := f.Seek(sizeOffset, io.SeekStart); err != nil {
		return 0, err
	}

	return readUint32(f)
}

func (idx *Index) openPack() (f *os.File, err error) {
	packPath := idx.packBase + ".pack"
	f, err = os.Open(packPath)
	if err != nil {
		return nil, err
	}

	defer func(f *os.File) {
		if err != nil {
			f.Close()
		}
	}(f) // Bind f early so that we can do "return nil, err".

	const headerLen = 8
	header, err := readN(f, headerLen)
	if err != nil {
		return nil, err
	}

	const sig = "PACK\x00\x00\x00\x02"
	if s := string(header); s != sig {
		return nil, fmt.Errorf("unexpected pack signature %q", s)
	}

	if _, err := f.Seek(-sumSize, io.SeekEnd); err != nil {
		return nil, err
	}

	sum, err := readN(f, sumSize)
	if err != nil {
		return nil, err
	}

	if s := hex.EncodeToString(sum); s != idx.ID {
		return nil, fmt.Errorf("unexpected trailing checksum in .pack: %s", s)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	return f, nil
}

func readUint32(r io.Reader) (uint32, error) {
	buf, err := readN(r, 4)
	if err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint32(buf), nil
}

func readN(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}

	return buf, nil
}

func (idx *Index) BuildPackfileOrder() {
	if len(idx.PackfileOrder) > 0 {
		return
	}

	idx.PackfileOrder = make([]*Object, len(idx.Objects))
	copy(idx.PackfileOrder, idx.Objects)
	sort.Sort(offsetOrder(idx.PackfileOrder))
}

type offsetOrder []*Object

func (oo offsetOrder) Len() int           { return len(oo) }
func (oo offsetOrder) Less(i, j int) bool { return oo[i].Offset < oo[j].Offset }
func (oo offsetOrder) Swap(i, j int)      { oo[i], oo[j] = oo[j], oo[i] }

func (idx *Index) LabelObjectTypes() error {
	if err := idx.LoadBitmap(); err != nil {
		return err
	}

	idx.BuildPackfileOrder()

	for _, t := range []struct {
		objectType ObjectType
		bmp        *Bitmap
	}{
		{TCommit, idx.IndexBitmap.Commits},
		{TTree, idx.IndexBitmap.Trees},
		{TBlob, idx.IndexBitmap.Blobs},
		{TTag, idx.IndexBitmap.Tags},
	} {
		if err := t.bmp.Scan(func(i int) error {
			obj := idx.PackfileOrder[i]
			if obj.Type != TUnknown {
				return fmt.Errorf("type already set for object %v", obj)
			}

			obj.Type = t.objectType

			return nil
		}); err != nil {
			return err
		}
	}

	for _, obj := range idx.PackfileOrder {
		if obj.Type == TUnknown {
			return fmt.Errorf("object missing type label: %v", obj)
		}
	}

	return nil
}
