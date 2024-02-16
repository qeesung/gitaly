package wal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReferenceTree(t *testing.T) {
	t.Parallel()

	rt := newReferenceTree()

	for _, ref := range []string{
		"refs/root-branch-1",
		"refs/root-branch-2",
		"refs/heads/branch-1",
		"refs/heads/branch-2",
		"refs/heads/subdir/branch-3",
		"refs/heads/subdir/branch-4",
		"refs/tags/tag-1",
		"refs/tags/tag-2",
		"refs/tags/subdir/tag-3",
		"refs/tags/subdir/tag-4",
	} {
		require.NoError(t, rt.Insert(ref))
	}

	require.Equal(t,
		&referenceTree{
			component: "refs",
			children: map[string]*referenceTree{
				"root-branch-1": {component: "root-branch-1"},
				"root-branch-2": {component: "root-branch-2"},
				"heads": {
					component: "heads",
					children: map[string]*referenceTree{
						"branch-1": {component: "branch-1"},
						"branch-2": {component: "branch-2"},
						"subdir": {
							component: "subdir",
							children: map[string]*referenceTree{
								"branch-3": {component: "branch-3"},
								"branch-4": {component: "branch-4"},
							},
						},
					},
				},
				"tags": {
					component: "tags",
					children: map[string]*referenceTree{
						"tag-1": {component: "tag-1"},
						"tag-2": {component: "tag-2"},
						"subdir": {
							component: "subdir",
							children: map[string]*referenceTree{
								"tag-3": {component: "tag-3"},
								"tag-4": {component: "tag-4"},
							},
						},
					},
				},
			},
		},
		rt,
	)

	t.Run("insert requires fully-qualified reference", func(t *testing.T) {
		t.Parallel()

		require.Equal(t,
			rt.Insert("non-refs/prefixed"),
			errors.New("expected a fully qualified reference"),
		)
	})

	t.Run("insert detects directory-file conflicts", func(t *testing.T) {
		t.Parallel()

		require.Equal(t,
			rt.Insert("refs/heads/branch-1/child"),
			errors.New("directory-file conflict"),
		)
	})

	t.Run("insert fails if the path already exists", func(t *testing.T) {
		require.Equal(t,
			errors.New("path already exists"),
			rt.Insert("refs/heads/branch-1"),
		)
	})

	t.Run("contains", func(t *testing.T) {
		t.Parallel()

		for _, entry := range []struct {
			reference string
			contains  bool
			isDir     bool
		}{
			{reference: "refs", contains: true},
			{reference: "refs/root-branch-1", contains: true},
			{reference: "refs/root-branch-2", contains: true},
			{reference: "refs/heads", contains: true},
			{reference: "refs/heads/branch-1", contains: true},
			{reference: "refs/heads/branch-2", contains: true},
			{reference: "refs/heads/subdir", contains: true},
			{reference: "refs/heads/subdir/branch-3", contains: true},
			{reference: "refs/heads/subdir/branch-4", contains: true},
			{reference: "refs/tags", contains: true},
			{reference: "refs/tags/tag-1", contains: true},
			{reference: "refs/tags/tag-2", contains: true},
			{reference: "refs/tags/subdir", contains: true},
			{reference: "refs/tags/subdir/tag-3", contains: true},
			{reference: "refs/tags/subdir/tag-4", contains: true},
			{reference: "non-existent"},
			{reference: "refs/non-existent"},
			{reference: "refs/heads/non-existent"},
			{reference: "refs/heads/subdir/non-existent"},
			{reference: "refs/heads/subdir/branch-4/non-existent"},
			{reference: "refs/tags/non-existent"},
			{reference: "refs/tags/subdir/non-existent"},
			{reference: "refs/tags/subdir/tag-4/non-existent"},
		} {
			require.Equal(t, rt.Contains(entry.reference), entry.contains)
		}
	})

	t.Run("walk", func(t *testing.T) {
		t.Parallel()

		type result struct {
			path  string
			isDir bool
		}

		errSentinel := errors.New("sentinel")

		for _, tc := range []struct {
			desc            string
			walk            func(walkCallback) error
			pathToFailOn    string
			expectedResults []result
		}{
			{
				desc: "pre-order",
				walk: rt.WalkPreOrder,
				expectedResults: []result{
					{path: "refs", isDir: true},
					{path: "refs/heads", isDir: true},
					{path: "refs/heads/branch-1"},
					{path: "refs/heads/branch-2"},
					{path: "refs/heads/subdir", isDir: true},
					{path: "refs/heads/subdir/branch-3"},
					{path: "refs/heads/subdir/branch-4"},
					{path: "refs/root-branch-1"},
					{path: "refs/root-branch-2"},
					{path: "refs/tags", isDir: true},
					{path: "refs/tags/subdir", isDir: true},
					{path: "refs/tags/subdir/tag-3"},
					{path: "refs/tags/subdir/tag-4"},
					{path: "refs/tags/tag-1"},
					{path: "refs/tags/tag-2"},
				},
			},
			{
				desc:         "pre-order failure on directory",
				walk:         rt.WalkPreOrder,
				pathToFailOn: "refs/heads/subdir",
				expectedResults: []result{
					{path: "refs", isDir: true},
					{path: "refs/heads", isDir: true},
					{path: "refs/heads/branch-1"},
					{path: "refs/heads/branch-2"},
					{path: "refs/heads/subdir", isDir: true},
				},
			},
			{
				desc:         "pre-order failure on file",
				walk:         rt.WalkPreOrder,
				pathToFailOn: "refs/heads/subdir/branch-3",
				expectedResults: []result{
					{path: "refs", isDir: true},
					{path: "refs/heads", isDir: true},
					{path: "refs/heads/branch-1"},
					{path: "refs/heads/branch-2"},
					{path: "refs/heads/subdir", isDir: true},
					{path: "refs/heads/subdir/branch-3"},
				},
			},
			{
				desc: "post-order",
				walk: rt.WalkPostOrder,
				expectedResults: []result{
					{path: "refs/heads/branch-1"},
					{path: "refs/heads/branch-2"},
					{path: "refs/heads/subdir/branch-3"},
					{path: "refs/heads/subdir/branch-4"},
					{path: "refs/heads/subdir", isDir: true},
					{path: "refs/heads", isDir: true},
					{path: "refs/root-branch-1"},
					{path: "refs/root-branch-2"},
					{path: "refs/tags/subdir/tag-3"},
					{path: "refs/tags/subdir/tag-4"},
					{path: "refs/tags/subdir", isDir: true},
					{path: "refs/tags/tag-1"},
					{path: "refs/tags/tag-2"},
					{path: "refs/tags", isDir: true},
					{path: "refs", isDir: true},
				},
			},
			{
				desc:         "post-order failure on directory",
				walk:         rt.WalkPostOrder,
				pathToFailOn: "refs/heads/subdir",
				expectedResults: []result{
					{path: "refs/heads/branch-1"},
					{path: "refs/heads/branch-2"},
					{path: "refs/heads/subdir/branch-3"},
					{path: "refs/heads/subdir/branch-4"},
					{path: "refs/heads/subdir", isDir: true},
				},
			},
			{
				desc:         "post-order failure on file",
				walk:         rt.WalkPostOrder,
				pathToFailOn: "refs/heads/subdir/branch-3",
				expectedResults: []result{
					{path: "refs/heads/branch-1"},
					{path: "refs/heads/branch-2"},
					{path: "refs/heads/subdir/branch-3"},
				},
			},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()

				var actualResults []result
				err := tc.walk(func(path string, isDir bool) error {
					actualResults = append(actualResults, result{
						path:  path,
						isDir: isDir,
					})

					if path == tc.pathToFailOn {
						return errSentinel
					}

					return nil
				})

				if tc.pathToFailOn != "" {
					require.ErrorIs(t, err, errSentinel)
				} else {
					require.NoError(t, err)
				}

				require.Equal(t, tc.expectedResults, actualResults)
			})
		}
	})

	t.Run("string formatting", func(t *testing.T) {
		require.Equal(t, `refs
 heads
  branch-1
  branch-2
  subdir
   branch-3
   branch-4
 root-branch-1
 root-branch-2
 tags
  subdir
   tag-3
   tag-4
  tag-1
  tag-2
`, rt.String())
	})
}
