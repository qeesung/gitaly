package packfile

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"

	"gitlab.com/gitlab-org/gitaly/internal/git/gitio"
)

// IndexBitmap is the in-memory representation of a .bitmap file.
type IndexBitmap struct {
	Commits       *Bitmap
	Trees         *Bitmap
	Blobs         *Bitmap
	Tags          *Bitmap
	bitmapCommits []*BitmapCommit
	flags         int
}

// BitmapCommit represents a bitmapped commit, i.e. a commit in the
// packfile plus a bitmap indicating which objects are reachable from
// that commit.
type BitmapCommit struct {
	OID string
	*Bitmap
	xorOffset byte
	flags     byte
}

func (idx *Index) LoadBitmap() error {
	if idx.IndexBitmap != nil {
		return nil
	}

	f, err := os.Open(idx.packBase + ".bitmap")
	if err != nil {
		return err
	}
	defer f.Close()

	r := bufio.NewReader(gitio.NewHashfileReader(f))

	ib := &IndexBitmap{}
	if err := ib.parseIndexBitmapHeader(r, idx); err != nil {
		return err
	}

	for _, ptr := range []**Bitmap{&ib.Commits, &ib.Trees, &ib.Blobs, &ib.Tags} {
		*ptr, err = ReadEWAH(r)
		if err != nil {
			return err
		}

		if err := (*ptr).Unpack(); err != nil {
			return err
		}
	}

	for i := range ib.bitmapCommits {
		header, err := readN(r, 6)
		if err != nil {
			return err
		}

		bc := &BitmapCommit{
			OID:       idx.Objects[binary.BigEndian.Uint32(header[:4])].OID,
			xorOffset: header[4],
			flags:     header[5],
		}

		if bc.Bitmap, err = ReadEWAH(r); err != nil {
			return err
		}

		ib.bitmapCommits[i] = bc
	}

	if ib.flags&BITMAP_OPT_HASH_CACHE > 0 {
		// Discard bitmap hash cache
		for range idx.Objects {
			if _, err := r.Discard(4); err != nil {
				return err
			}
		}
	}

	if _, err := r.Peek(1); err != io.EOF {
		return fmt.Errorf("expected EOF, got %v", err)
	}

	idx.IndexBitmap = ib
	return nil
}

const (
	BITMAP_OPT_FULL_DAG   = 1
	BITMAP_OPT_HASH_CACHE = 4
)

func (ib *IndexBitmap) parseIndexBitmapHeader(r io.Reader, idx *Index) error {
	const headerLen = 32
	header, err := readN(r, headerLen)
	if err != nil {
		return err
	}

	const sig = "BITM\x00\x01"
	if actualSig := string(header[:len(sig)]); actualSig != sig {
		return fmt.Errorf("unexpected signature %q", actualSig)
	}
	header = header[len(sig):]

	const flagLen = 2
	ib.flags = int(binary.BigEndian.Uint16(header[:flagLen]))
	header = header[flagLen:]

	const knownFlags = BITMAP_OPT_FULL_DAG | BITMAP_OPT_HASH_CACHE
	if ib.flags&^knownFlags != 0 || (ib.flags&BITMAP_OPT_FULL_DAG == 0) {
		return fmt.Errorf("invalid flags %x", ib.flags)
	}

	const countLen = 4
	count := binary.BigEndian.Uint32(header[:countLen])
	header = header[countLen:]
	ib.bitmapCommits = make([]*BitmapCommit, count)

	if s := hex.EncodeToString(header); s != idx.ID {
		return fmt.Errorf("unexpected pack ID in bitmap header: %s", s)
	}

	return nil
}

func (ib *IndexBitmap) NumBitmapCommits() int { return len(ib.bitmapCommits) }

func (ib *IndexBitmap) BitmapCommit(i int) (*BitmapCommit, error) {
	if i >= ib.NumBitmapCommits() {
		return nil, fmt.Errorf("bitmap commit index %d out of range", i)
	}

	// This is wasteful but correct: bitmap commit i may depend, via XOR, on
	// a chain of preceding commits j_0,..., j_m < i. Instead of finding that
	// chain we just build and XOR all commits up to and including i.
	for j, bc := range ib.bitmapCommits[:i+1] {
		if bc.Bitmap.bm != nil {
			continue
		}

		if err := bc.Bitmap.Unpack(); err != nil {
			return nil, err
		}

		if k := int(bc.xorOffset); k > 0 {
			bm := bc.Bitmap.bm
			bm.Xor(bm, ib.bitmapCommits[j-k].Bitmap.bm)
		}
	}

	return ib.bitmapCommits[i], nil
}

type Bitmap struct {
	bits  int
	words int
	raw   []byte
	bm    *big.Int
}

func ReadEWAH(r io.Reader) (*Bitmap, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	e := &Bitmap{}

	uBits := binary.BigEndian.Uint32(header[:4])
	if uBits > math.MaxInt32 {
		return nil, fmt.Errorf("too many bits in bitmap: %d", uBits)
	}
	e.bits = int(uBits)

	uWords := binary.BigEndian.Uint32(header[4:])
	if uWords > math.MaxInt32 {
		return nil, fmt.Errorf("too many words in bitmap: %d", uWords)
	}
	e.words = int(uWords)

	const ewahTrailerLen = 4
	rawSize := int64(e.words)*8 + ewahTrailerLen
	if rawSize > math.MaxInt32 {
		return nil, fmt.Errorf("Bitmap bitmap does not fit in Go slice")
	}

	e.raw = make([]byte, int(rawSize))

	if _, err := io.ReadFull(r, e.raw); err != nil {
		return nil, err
	}

	return e, nil
}

func (e *Bitmap) Unpack() error {
	if e.bm != nil {
		return nil
	}

	const (
		wordSize = 8
		wordBits = 8 * wordSize
	)

	nUnpackedWords := e.bits / wordBits
	if e.bits%wordBits > 0 {
		nUnpackedWords++
	}

	buf := make([]byte, nUnpackedWords*wordSize)
	bufPos := len(buf)

	fillOnes := bytes.Repeat([]byte{0xff}, wordSize)

	for i := 0; i < e.words; {
		header := binary.BigEndian.Uint64(e.raw[wordSize*i : wordSize*(i+1)])
		i++

		cleanBit := int(header & 1)
		nClean := uint32(header >> 1)
		nDirty := uint32(header >> 33)

		for ; nClean > 0; nClean-- {
			if cleanBit == 1 {
				copy(
					buf[bufPos-wordSize:bufPos],
					fillOnes,
				)
			} else {
				// No need to copy zeros into buf
			}

			bufPos -= wordSize
		}

		for ; nDirty > 0; nDirty-- {
			copy(
				buf[bufPos-wordSize:bufPos],
				e.raw[wordSize*i:wordSize*(i+1)],
			)
			bufPos -= wordSize
			i++
		}
	}

	e.bm = big.NewInt(0)
	e.bm.SetBytes(buf)

	return nil
}

func (e *Bitmap) Scan(f func(int) error) error {
	for i := 0; i < e.bits; i++ {
		if e.bm.Bit(i) == 1 {
			if err := f(i); err != nil {
				return err
			}
		}
	}

	return nil
}
