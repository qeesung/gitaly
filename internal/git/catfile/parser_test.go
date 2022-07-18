//go:build !gitaly_test_sha256

package catfile

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParser_ParseCommit(t *testing.T) {
	info := &ObjectInfo{
		Oid:  "a984dfa4dee018c6d5f5f57ffec0d0e22763df16",
		Type: "commit",
	}

	// Valid-but-interesting commits should be test at the FindCommit level.
	// Invalid objects (that Git would complain about during fsck) can be
	// tested here.
	//
	// Once a repository contains a pathological object it can be hard to get
	// rid of it. Because of this I think it's nicer to ignore such objects
	// than to throw hard errors.
	testCases := []struct {
		desc string
		in   string
		out  *gitalypb.GitCommit
	}{
		{
			desc: "empty commit object",
			in:   "",
			out:  &gitalypb.GitCommit{Id: info.Oid.String()},
		},
		{
			desc: "no email",
			in:   "author Jane Doe",
			out: &gitalypb.GitCommit{
				Id:     info.Oid.String(),
				Author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe")},
			},
		},
		{
			desc: "unmatched <",
			in:   "author Jane Doe <janedoe@example.com",
			out: &gitalypb.GitCommit{
				Id:     info.Oid.String(),
				Author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe")},
			},
		},
		{
			desc: "unmatched >",
			in:   "author Jane Doe janedoe@example.com>",
			out: &gitalypb.GitCommit{
				Id:     info.Oid.String(),
				Author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe janedoe@example.com>")},
			},
		},
		{
			desc: "missing date",
			in:   "author Jane Doe <janedoe@example.com> ",
			out: &gitalypb.GitCommit{
				Id:     info.Oid.String(),
				Author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe"), Email: []byte("janedoe@example.com")},
			},
		},
		{
			desc: "date too high",
			in:   "author Jane Doe <janedoe@example.com> 9007199254740993 +0200",
			out: &gitalypb.GitCommit{
				Id: info.Oid.String(),
				Author: &gitalypb.CommitAuthor{
					Name:     []byte("Jane Doe"),
					Email:    []byte("janedoe@example.com"),
					Date:     &timestamppb.Timestamp{Seconds: 9223371974719179007},
					Timezone: []byte("+0200"),
				},
			},
		},
		{
			desc: "date negative",
			in:   "author Jane Doe <janedoe@example.com> -1 +0200",
			out: &gitalypb.GitCommit{
				Id: info.Oid.String(),
				Author: &gitalypb.CommitAuthor{
					Name:     []byte("Jane Doe"),
					Email:    []byte("janedoe@example.com"),
					Date:     &timestamppb.Timestamp{Seconds: 9223371974719179007},
					Timezone: []byte("+0200"),
				},
			},
		},
		{
			desc: "ssh signature",
			in: `gpgsig -----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgtc+Qk8jhMwVZk/jFEFCM16LNQb
30q5kK30bbetfjyTMAAAADZ2l0AAAAAAAAAAZzaGE1MTIAAABTAAAAC3NzaC1lZDI1NTE5
AAAAQLSyv010gOFwIs9QTtDvlfIEWiAw2iQL/T9usGcxHXn/W5l0cOFCd7O+WaMDg0t0nW
fF3T79iV8paT4/OfX8Ygg=
-----END SSH SIGNATURE-----`,
			out: &gitalypb.GitCommit{
				Id:            info.Oid.String(),
				SignatureType: gitalypb.SignatureType_SSH,
			},
		},
		{
			desc: "huge",
			in:   "author " + strings.Repeat("A", 100000),
			out: &gitalypb.GitCommit{
				Id: info.Oid.String(),
				Author: &gitalypb.CommitAuthor{
					Name: bytes.Repeat([]byte("A"), 100000),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			info.Size = int64(len(tc.in))
			out, err := NewParser().ParseCommit(newStaticObject(tc.in, "commit", info.Oid))
			require.NoError(t, err, "parse error")
			require.Equal(t, tc.out, out)
		})
	}
}

func TestParseCommitAuthor(t *testing.T) {
	for _, tc := range []struct {
		desc     string
		author   string
		expected *gitalypb.CommitAuthor
	}{
		{
			desc:     "empty author",
			author:   "",
			expected: &gitalypb.CommitAuthor{},
		},
		{
			desc:   "normal author",
			author: "Au Thor <au.thor@example.com> 1625121079 +0000",
			expected: &gitalypb.CommitAuthor{
				Name:     []byte("Au Thor"),
				Email:    []byte("au.thor@example.com"),
				Date:     timestamppb.New(time.Unix(1625121079, 0)),
				Timezone: []byte("+0000"),
			},
		},
		{
			desc:   "author with missing mail",
			author: "Au Thor <> 1625121079 +0000",
			expected: &gitalypb.CommitAuthor{
				Name:     []byte("Au Thor"),
				Date:     timestamppb.New(time.Unix(1625121079, 0)),
				Timezone: []byte("+0000"),
			},
		},
		{
			desc:   "author with missing date",
			author: "Au Thor <au.thor@example.com>",
			expected: &gitalypb.CommitAuthor{
				Name:  []byte("Au Thor"),
				Email: []byte("au.thor@example.com"),
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			testhelper.ProtoEqual(t, tc.expected, parseCommitAuthor(tc.author))
		})
	}
}

func TestParser_ParseTag(t *testing.T) {
	for _, tc := range []struct {
		desc           string
		oid            git.ObjectID
		contents       string
		expectedTag    *gitalypb.Tag
		expectedTagged taggedObject
	}{
		{
			desc:     "tag without a message",
			contents: "object c92faf3e0a557270141be67f206d7cdb99bfc3a2\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200",
			oid:      "1234",
			expectedTag: &gitalypb.Tag{
				Id:   "1234",
				Name: []byte("v2.6.16.28"),
				Tagger: &gitalypb.CommitAuthor{
					Name:  []byte("Adrian Bunk"),
					Email: []byte("bunk@stusta.de"),
					Date: &timestamppb.Timestamp{
						Seconds: 1156539089,
					},
					Timezone: []byte("+0200"),
				},
			},
			expectedTagged: taggedObject{
				objectID:   "c92faf3e0a557270141be67f206d7cdb99bfc3a2",
				objectType: "commit",
			},
		},
		{
			desc:     "tag with message",
			contents: "object c92faf3e0a557270141be67f206d7cdb99bfc3a2\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\nmessage",
			oid:      "1234",
			expectedTag: &gitalypb.Tag{
				Id:          "1234",
				Name:        []byte("v2.6.16.28"),
				Message:     []byte("message"),
				MessageSize: 7,
				Tagger: &gitalypb.CommitAuthor{
					Name:  []byte("Adrian Bunk"),
					Email: []byte("bunk@stusta.de"),
					Date: &timestamppb.Timestamp{
						Seconds: 1156539089,
					},
					Timezone: []byte("+0200"),
				},
			},
			expectedTagged: taggedObject{
				objectID:   "c92faf3e0a557270141be67f206d7cdb99bfc3a2",
				objectType: "commit",
			},
		},
		{
			desc:     "tag with empty message",
			oid:      "1234",
			contents: "object c92faf3e0a557270141be67f206d7cdb99bfc3a2\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\n",
			expectedTag: &gitalypb.Tag{
				Id:      "1234",
				Name:    []byte("v2.6.16.28"),
				Message: []byte{},
				Tagger: &gitalypb.CommitAuthor{
					Name:  []byte("Adrian Bunk"),
					Email: []byte("bunk@stusta.de"),
					Date: &timestamppb.Timestamp{
						Seconds: 1156539089,
					},
					Timezone: []byte("+0200"),
				},
			},
			expectedTagged: taggedObject{
				objectID:   "c92faf3e0a557270141be67f206d7cdb99bfc3a2",
				objectType: "commit",
			},
		},
		{
			desc:     "tag with message with empty line",
			oid:      "1234",
			contents: "object c92faf3e0a557270141be67f206d7cdb99bfc3a2\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\nHello world\n\nThis is a message",
			expectedTag: &gitalypb.Tag{
				Id:          "1234",
				Name:        []byte("v2.6.16.28"),
				Message:     []byte("Hello world\n\nThis is a message"),
				MessageSize: 30,
				Tagger: &gitalypb.CommitAuthor{
					Name:  []byte("Adrian Bunk"),
					Email: []byte("bunk@stusta.de"),
					Date: &timestamppb.Timestamp{
						Seconds: 1156539089,
					},
					Timezone: []byte("+0200"),
				},
			},
			expectedTagged: taggedObject{
				objectID:   "c92faf3e0a557270141be67f206d7cdb99bfc3a2",
				objectType: "commit",
			},
		},
		{
			desc:     "tag with message with empty line and right side new line",
			contents: "object c92faf3e0a557270141be67f206d7cdb99bfc3a2\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\nHello world\n\nThis is a message\n\n",
			oid:      "1234",
			expectedTag: &gitalypb.Tag{
				Id:          "1234",
				Name:        []byte("v2.6.16.28"),
				Message:     []byte("Hello world\n\nThis is a message\n\n"),
				MessageSize: 32,
				Tagger: &gitalypb.CommitAuthor{
					Name:  []byte("Adrian Bunk"),
					Email: []byte("bunk@stusta.de"),
					Date: &timestamppb.Timestamp{
						Seconds: 1156539089,
					},
					Timezone: []byte("+0200"),
				},
			},
			expectedTagged: taggedObject{
				objectID:   "c92faf3e0a557270141be67f206d7cdb99bfc3a2",
				objectType: "commit",
			},
		},
		{
			desc:     "tag with missing date and body",
			contents: "object 422081655f743e03b01ee29a2eaf26aab0ee7eda\ntype commit\ntag syslinux-3.11-pre6\ntagger hpa <hpa>\n",
			oid:      "1234",
			expectedTag: &gitalypb.Tag{
				Id:   "1234",
				Name: []byte("syslinux-3.11-pre6"),
				Tagger: &gitalypb.CommitAuthor{
					Name:  []byte("hpa"),
					Email: []byte("hpa"),
				},
			},
			expectedTagged: taggedObject{
				objectID:   "422081655f743e03b01ee29a2eaf26aab0ee7eda",
				objectType: "commit",
			},
		},
		{
			desc: "tag signed with SSH",
			oid:  "1234",
			contents: `object c92faf3e0a557270141be67f206d7cdb99bfc3a2
type commit
tag v2.6.16.28
tagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200

This tag is signed with SSH
-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgtc+Qk8jhMwVZk/jFEFCM16LNQb
30q5kK30bbetfjyTMAAAADZ2l0AAAAAAAAAAZzaGE1MTIAAABTAAAAC3NzaC1lZDI1NTE5
AAAAQLSyv010gOFwIs9QTtDvlfIEWiAw2iQL/T9usGcxHXn/W5l0cOFCd7O+WaMDg0t0nW
fF3T79iV8paT4/OfX8Ygg=
-----END SSH SIGNATURE-----`,
			expectedTag: &gitalypb.Tag{
				Id:   "1234",
				Name: []byte("v2.6.16.28"),
				Message: []byte(`This tag is signed with SSH
-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgtc+Qk8jhMwVZk/jFEFCM16LNQb
30q5kK30bbetfjyTMAAAADZ2l0AAAAAAAAAAZzaGE1MTIAAABTAAAAC3NzaC1lZDI1NTE5
AAAAQLSyv010gOFwIs9QTtDvlfIEWiAw2iQL/T9usGcxHXn/W5l0cOFCd7O+WaMDg0t0nW
fF3T79iV8paT4/OfX8Ygg=
-----END SSH SIGNATURE-----`),
				MessageSize: 321,
				Tagger: &gitalypb.CommitAuthor{
					Name:  []byte("Adrian Bunk"),
					Email: []byte("bunk@stusta.de"),
					Date: &timestamppb.Timestamp{
						Seconds: 1156539089,
					},
					Timezone: []byte("+0200"),
				},
				SignatureType: gitalypb.SignatureType_SSH,
			},
			expectedTagged: taggedObject{
				objectID:   "c92faf3e0a557270141be67f206d7cdb99bfc3a2",
				objectType: "commit",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tag, tagged, err := newParser().parseTag(newStaticObject(tc.contents, "tag", tc.oid), nil)
			require.NoError(t, err)
			require.Equal(t, tc.expectedTag, tag)
			require.Equal(t, tc.expectedTagged, tagged)
		})
	}
}
