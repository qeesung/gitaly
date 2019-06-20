package gittest

import (
	"encoding/binary"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBitmapHasHashcache checks if the named pack bitmap file contains
// "hash cache" data. See
// https://github.com/git/git/blob/master/Documentation/technical/bitmap-format.txt
func TestBitmapHasHashcache(t *testing.T, bitmap string) {
	bitmapFile, err := os.Open(bitmap)
	require.NoError(t, err)
	defer bitmapFile.Close()

	b := make([]byte, 8)
	_, err = io.ReadFull(bitmapFile, b)
	require.NoError(t, err)

	const hashCacheFlag = 0x4
	flags := binary.BigEndian.Uint16(b[6:])
	require.Equal(t, uint16(hashCacheFlag), flags&hashCacheFlag, "expect BITMAP_OPT_HASH_CACHE to be set")
}
