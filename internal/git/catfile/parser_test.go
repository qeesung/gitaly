package catfile

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParser_ParseTag(t *testing.T) {
	t.Parallel()

	oid := gittest.DefaultObjectHash.EmptyTreeOID.String()

	for _, tc := range []struct {
		desc           string
		oid            git.ObjectID
		contents       string
		expectedTag    *gitalypb.Tag
		expectedTagged taggedObject
	}{
		{
			desc:     "tag without a message",
			contents: fmt.Sprintf("object %s\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200", oid),
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
				objectID:   oid,
				objectType: "commit",
			},
		},
		{
			desc:     "tag with message",
			contents: fmt.Sprintf("object %s\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\nmessage", oid),
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
				objectID:   oid,
				objectType: "commit",
			},
		},
		{
			desc:     "tag with empty message",
			oid:      "1234",
			contents: fmt.Sprintf("object %s\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\n", oid),
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
				objectID:   oid,
				objectType: "commit",
			},
		},
		{
			desc:     "tag with message with empty line",
			oid:      "1234",
			contents: fmt.Sprintf("object %s\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\nHello world\n\nThis is a message", oid),
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
				objectID:   oid,
				objectType: "commit",
			},
		},
		{
			desc:     "tag with message with empty line and right side new line",
			contents: fmt.Sprintf("object %s\ntype commit\ntag v2.6.16.28\ntagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200\n\nHello world\n\nThis is a message\n\n", oid),
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
				objectID:   oid,
				objectType: "commit",
			},
		},
		{
			desc:     "tag with missing date and body",
			contents: fmt.Sprintf("object %s\ntype commit\ntag syslinux-3.11-pre6\ntagger hpa <hpa>\n", oid),
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
				objectID:   oid,
				objectType: "commit",
			},
		},
		{
			desc: "tag signed with SSH",
			oid:  "1234",
			contents: fmt.Sprintf(`object %s
type commit
tag v2.6.16.28
tagger Adrian Bunk <bunk@stusta.de> 1156539089 +0200

This tag is signed with SSH
-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgtc+Qk8jhMwVZk/jFEFCM16LNQb
30q5kK30bbetfjyTMAAAADZ2l0AAAAAAAAAAZzaGE1MTIAAABTAAAAC3NzaC1lZDI1NTE5
AAAAQLSyv010gOFwIs9QTtDvlfIEWiAw2iQL/T9usGcxHXn/W5l0cOFCd7O+WaMDg0t0nW
fF3T79iV8paT4/OfX8Ygg=
-----END SSH SIGNATURE-----`, oid),
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
				objectID:   oid,
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
