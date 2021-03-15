package cache_test

import (
	"context"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/cache"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()
	cleanup := testhelper.Configure()
	defer cleanup()
	return m.Run()
}

func TestStreamDBNaiveKeyer(t *testing.T) {
	cfg := testcfg.Build(t)

	testRepo1, _, _ := gittest.CloneRepoAtStorage(t, cfg.Storages[0], "repository-1")
	testRepo2, _, _ := gittest.CloneRepoAtStorage(t, cfg.Storages[0], "repository-2")

	keyer := cache.NewLeaseKeyer(config.NewLocator(cfg))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ctx = testhelper.SetCtxGrpcMethod(ctx, "InfoRefsUploadPack")

	db := cache.NewStreamDB(cache.NewLeaseKeyer(config.NewLocator(cfg)))

	req1 := &gitalypb.InfoRefsRequest{
		Repository: testRepo1,
	}
	req2 := &gitalypb.InfoRefsRequest{
		Repository: testRepo2,
	}

	expectGetMiss := func(req *gitalypb.InfoRefsRequest) {
		_, err := db.GetStream(ctx, req.Repository, req)
		require.Equal(t, cache.ErrReqNotFound, err)
	}

	expectGetHit := func(expectStr string, req *gitalypb.InfoRefsRequest) {
		actualStream, err := db.GetStream(ctx, req.Repository, req)
		require.NoError(t, err)
		actualBytes, err := ioutil.ReadAll(actualStream)
		require.NoError(t, err)
		require.Equal(t, expectStr, string(actualBytes))
	}

	invalidationEvent := func(repo *gitalypb.Repository) {
		lease, err := keyer.StartLease(repo)
		require.NoError(t, err)
		// imagine repo being modified here
		require.NoError(t, lease.EndLease(ctx))
	}

	storeAndRetrieve := func(expectStr string, req *gitalypb.InfoRefsRequest) {
		require.NoError(t, db.PutStream(ctx, req.Repository, req, strings.NewReader(expectStr)))
		expectGetHit(expectStr, req)
	}

	// cache is initially empty
	expectGetMiss(req1)
	expectGetMiss(req2)

	// populate cache
	repo1contents := "store and retrieve value in repo 1"
	storeAndRetrieve(repo1contents, req1)
	repo2contents := "store and retrieve value in repo 2"
	storeAndRetrieve(repo2contents, req2)

	// invalidation makes previous value stale and unreachable
	invalidationEvent(req1.Repository)
	expectGetMiss(req1)
	expectGetHit(repo2contents, req2) // repo1 invalidation doesn't affect repo2

	// store new value for same cache value but at new generation
	expectStream2 := "not what you were looking for"
	require.NoError(t, db.PutStream(ctx, req1.Repository, req1, strings.NewReader(expectStream2)))
	expectGetHit(expectStream2, req1)

	// enabled feature flags affect caching
	oldCtx := ctx
	ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, featureflag.FeatureFlag{"meow", false})
	expectGetMiss(req1)
	ctx = oldCtx
	expectGetHit(expectStream2, req1)

	// start critical section without closing
	repo1Lease, err := keyer.StartLease(req1.Repository)
	require.NoError(t, err)

	// accessing repo cache with open critical section should fail
	_, err = db.GetStream(ctx, req1.Repository, req1)
	require.Equal(t, err, cache.ErrPendingExists)
	err = db.PutStream(ctx, req1.Repository, req1, strings.NewReader(repo1contents))
	require.Equal(t, err, cache.ErrPendingExists)

	expectGetHit(repo2contents, req2) // other repo caches should be unaffected

	// opening and closing a new critical zone doesn't resolve the issue
	invalidationEvent(req1.Repository)
	_, err = db.GetStream(ctx, req1.Repository, req1)
	require.Equal(t, err, cache.ErrPendingExists)

	// only completing/removing the pending generation file will allow access
	require.NoError(t, repo1Lease.EndLease(ctx))
	expectGetMiss(req1)

	// creating a lease on a repo that doesn't exist yet should succeed
	req1.Repository.RelativePath += "-does-not-exist"
	_, err = keyer.StartLease(req1.Repository)
	require.NoError(t, err)
}

func TestLoserCount(t *testing.T) {
	// the test can be contaminate by other tests using the cache, so a
	// dedicated storage location should be used
	cfgBuilder := testcfg.NewGitalyCfgBuilder(testcfg.WithStorages("storage-1", "storage-2"))
	defer cfgBuilder.Cleanup()
	cfg := cfgBuilder.Build(t)

	db := cache.NewStreamDB(cache.NewLeaseKeyer(config.NewLocator(cfg)))

	req := &gitalypb.InfoRefsRequest{
		Repository: &gitalypb.Repository{
			RelativePath: "test",
			StorageName:  "storage-1",
		},
	}
	ctx := testhelper.SetCtxGrpcMethod(context.Background(), "InfoRefsUploadPack")

	leashes := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	errQ := make(chan error)

	wg := &sync.WaitGroup{}
	wg.Add(len(leashes))

	// Run streams concurrently for the same repo and request
	for _, l := range leashes {
		go func(l chan struct{}) { errQ <- db.PutStream(ctx, req.Repository, req, leashedReader{l, wg}) }(l)
		l <- struct{}{}
	}

	wg.Wait()

	start := cache.ExportMockLoserBytes.Count()

	for _, l := range leashes {
		close(l)
		require.NoError(t, <-errQ)
	}

	require.Equal(t, start+len(leashes)-1, cache.ExportMockLoserBytes.Count())
}

type leashedReader struct {
	q  <-chan struct{}
	wg *sync.WaitGroup
}

func (lr leashedReader) Read(p []byte) (n int, err error) {
	_, ok := <-lr.q

	if !ok {
		return 0, io.EOF // on channel close
	}

	lr.wg.Done()
	lr.wg.Wait() // wait for all other readers to sync

	return 1, nil // on receive, return 1 byte read
}
