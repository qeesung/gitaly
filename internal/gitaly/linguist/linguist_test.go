package linguist

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func TestInstance_Stats_successful(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, _, repoPath := testcfg.BuildWithRepo(t)

	ling, err := New(cfg, gittest.NewCommandFactory(t, cfg))
	require.NoError(t, err)

	counts, err := ling.Stats(ctx, repoPath, "1e292f8fedd741b75372e19097c76d327140c312")
	require.NoError(t, err)
	require.Equal(t, uint64(2943), counts["Ruby"])
}

func TestInstance_Stats_unmarshalJSONError(t *testing.T) {
	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	ling, err := New(cfg, gittest.NewCommandFactory(t, cfg))
	require.NoError(t, err)

	// When an error occurs, this used to trigger JSON marshelling of a plain string
	// the new behaviour shouldn't do that, and return an command error
	_, err = ling.Stats(ctx, "/var/empty", "deadbeef")
	require.Error(t, err)

	_, ok := err.(*json.SyntaxError)
	require.False(t, ok, "expected the error not be a json Syntax Error")
}

func TestNew(t *testing.T) {
	cfg := testcfg.Build(t, testcfg.WithRealLinguist())

	ling, err := New(cfg, gittest.NewCommandFactory(t, cfg))
	require.NoError(t, err)

	require.Equal(t, "#701516", ling.Color("Ruby"), "color value for 'Ruby'")
}

func TestNew_loadLanguagesCustomPath(t *testing.T) {
	jsonPath, err := filepath.Abs("testdata/fake-languages.json")
	require.NoError(t, err)

	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{Ruby: config.Ruby{LinguistLanguagesPath: jsonPath}}))

	ling, err := New(cfg, gittest.NewCommandFactory(t, cfg))
	require.NoError(t, err)

	require.Equal(t, "foo color", ling.Color("FooBar"))
}
