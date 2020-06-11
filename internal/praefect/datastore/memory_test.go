package datastore

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func ContractTestCountDeadReplicationJobs(t *testing.T, q ReplicationEventQueue) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	// take the time here to include the also the
	// completed and cancelled jobs in timerange
	beforeOldest := time.Now()

	const virtual = "praefect"
	const target = "target"
	ackJobsToDeath := func(t *testing.T) {
		t.Helper()

		for {
			jobs, err := q.Dequeue(ctx, virtual, target, 1)
			require.NoError(t, err)
			if len(jobs) == 0 {
				break
			}

			for _, job := range jobs {
				state := JobStateFailed
				if job.Attempt == 0 {
					state = JobStateDead
				}

				_, err := q.Acknowledge(ctx, state, []uint64{job.ID})
				require.NoError(t, err)
			}
		}
	}

	// postgres only handles timestamps with a microsecond resolution thus
	// we have to work with the time in microsecond sized steps
	const tick = time.Microsecond

	// add some other job states to the datastore to ensure they are not counted
	for relPath, state := range map[string]JobState{"repo/completed-job": JobStateCompleted, "repo/cancelled-job": JobStateCancelled} {
		_, err := q.Enqueue(ctx, ReplicationEvent{Job: ReplicationJob{RelativePath: relPath, VirtualStorage: virtual, TargetNodeStorage: target}})
		require.NoError(t, err)

		jobs, err := q.Dequeue(ctx, virtual, target, 1)
		require.NoError(t, err)

		_, err = q.Acknowledge(ctx, state, []uint64{jobs[0].ID})
		require.NoError(t, err)
	}

	oldest, err := q.Enqueue(ctx, ReplicationEvent{Job: ReplicationJob{RelativePath: "old", VirtualStorage: virtual, TargetNodeStorage: target}})
	require.NoError(t, err)

	afterOldest := oldest.CreatedAt.Add(tick)

	dead, err := q.CountDeadReplicationJobs(ctx, beforeOldest, afterOldest)
	require.NoError(t, err)
	require.Empty(t, dead, "should not include ready jobs")

	jobs, err := q.Dequeue(ctx, virtual, target, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	_, err = q.Acknowledge(ctx, JobStateFailed, []uint64{jobs[0].ID})
	require.NoError(t, err)

	dead, err = q.CountDeadReplicationJobs(ctx, beforeOldest, afterOldest)
	require.NoError(t, err)
	require.Empty(t, dead, "should not include failed jobs")

	ackJobsToDeath(t)
	dead, err = q.CountDeadReplicationJobs(ctx, beforeOldest, afterOldest)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"old": 1}, dead, "should include dead job")

	middle, err := q.Enqueue(ctx, ReplicationEvent{Job: ReplicationJob{RelativePath: "new", VirtualStorage: virtual, TargetNodeStorage: target}})
	require.NoError(t, err)

	ackJobsToDeath(t)
	dead, err = q.CountDeadReplicationJobs(ctx, beforeOldest, middle.CreatedAt.Add(tick))
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"old": 1, "new": 1}, dead, "should include both dead jobs")

	newest, err := q.Enqueue(ctx, ReplicationEvent{Job: ReplicationJob{RelativePath: "new", VirtualStorage: virtual, TargetNodeStorage: target}})
	require.NoError(t, err)

	ackJobsToDeath(t)
	dead, err = q.CountDeadReplicationJobs(ctx, beforeOldest, newest.CreatedAt.Add(tick))
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"old": 1, "new": 2}, dead, "dead job are grouped by relative path")

	dead, err = q.CountDeadReplicationJobs(ctx, middle.CreatedAt, newest.CreatedAt.Add(-tick))
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"new": 1}, dead, "should only count the in-between dead job")
}

func TestMemoryCountDeadReplicationJobs(t *testing.T) {
	ContractTestCountDeadReplicationJobs(t, NewMemoryReplicationEventQueue(config.Config{}))
}

func TestMemoryCountDeadReplicationJobsLimit(t *testing.T) {
	q := NewMemoryReplicationEventQueue(config.Config{}).(*memoryReplicationEventQueue)
	q.maxDeadJobs = 2

	ctx, cancel := testhelper.Context()
	defer cancel()

	const virtual = "praefect"
	const target = "target"

	beforeAll := time.Now()
	for i := 0; i < q.maxDeadJobs+1; i++ {
		job, err := q.Enqueue(ctx, ReplicationEvent{Job: ReplicationJob{RelativePath: fmt.Sprintf("job-%d", i), VirtualStorage: virtual, TargetNodeStorage: target}})
		require.NoError(t, err)

		for i := 0; i < job.Attempt; i++ {
			_, err := q.Dequeue(ctx, virtual, target, 1)
			require.NoError(t, err)

			state := JobStateFailed
			if i == job.Attempt-1 {
				state = JobStateDead
			}

			_, err = q.Acknowledge(ctx, state, []uint64{job.ID})
			require.NoError(t, err)
		}
	}

	dead, err := q.CountDeadReplicationJobs(ctx, beforeAll, time.Now())
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"job-1": 1, "job-2": 1}, dead, "should only include the last two dead jobs")
}

func contractTestQueueGetOutdatedRepositories(t *testing.T, rq ReplicationEventQueue, setState func(testing.TB, []ReplicationEvent)) {
	const (
		virtualStorage = "test-virtual-storage"
		oldPrimary     = "old-primary"
		newPrimary     = "new-primary"
		secondary      = "secondary"
	)

	now := time.Now()
	offset := func(d time.Duration) *time.Time {
		t := now.Add(d)
		return &t
	}

	for _, tc := range []struct {
		desc     string
		events   []ReplicationEvent
		error    error
		expected map[string][]string
	}{
		{
			desc: "basic scenarios work",
			events: []ReplicationEvent{
				{
					State: JobStateReady,
					Job: ReplicationJob{
						VirtualStorage:    "wrong-virtual-storage",
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
				{
					State: JobStateDead,
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: "wrong-source-node",
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
				{
					State: JobStateCompleted,
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "completed-job-ignored",
					},
				},
				{
					State: JobStateDead,
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
				{
					State: JobStateInProgress,
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-2",
					},
				},
				{
					State: JobStateFailed,
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: secondary,
						RelativePath:      "repo-2",
					},
				},
			},
			expected: map[string][]string{
				"repo-1": {newPrimary},
				"repo-2": {newPrimary, secondary},
			},
		},
		{
			desc: "search considers null updated_at as latest",
			events: []ReplicationEvent{
				{
					State:     JobStateCompleted,
					UpdatedAt: offset(0),
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
				{
					State: JobStateReady,
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
			},
			expected: map[string][]string{
				"repo-1": []string{newPrimary},
			},
		},
		{
			desc: "jobs targeting reference are ignored",
			events: []ReplicationEvent{
				{
					State:     JobStateDead,
					UpdatedAt: offset(0),
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: secondary,
						TargetNodeStorage: oldPrimary,
						RelativePath:      "repo-1",
					},
				},
			},
			expected: map[string][]string{},
		},
		{
			// completed job from a secondary indicates the new primary's
			// state does not originate from the previous writable primary.
			// This might indicate data loss, if the secondary is not up to
			// date with the previous writable primary.
			desc: "completed job from secondary",
			events: []ReplicationEvent{
				{
					State:     JobStateCompleted,
					UpdatedAt: offset(0),
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
				{
					State:     JobStateCompleted,
					UpdatedAt: offset(time.Second),
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: secondary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
			},
			expected: map[string][]string{
				"repo-1": {newPrimary},
			},
		},
		{
			// Node that experienced data loss on failover but was later
			// reconciled from the previous writable primary should
			// contain complete data.
			desc: "up to date with earlier failed job from old primary",
			events: []ReplicationEvent{
				{
					State:     JobStateDead,
					UpdatedAt: offset(0),
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
				{
					State:     JobStateCompleted,
					UpdatedAt: offset(time.Second),
					Job: ReplicationJob{
						VirtualStorage:    virtualStorage,
						SourceNodeStorage: oldPrimary,
						TargetNodeStorage: newPrimary,
						RelativePath:      "repo-1",
					},
				},
			},
			expected: map[string][]string{},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			setState(t, tc.events)

			actual, err := rq.GetOutdatedRepositories(ctx, virtualStorage, oldPrimary)
			require.NoError(t, err)
			require.Equal(t, tc.expected, actual)
		})
	}
}

func TestMemoryReplicationEventQueue_GetOutdatedRepositories(t *testing.T) {
	rq := NewMemoryReplicationEventQueue(config.Config{}).(*memoryReplicationEventQueue)

	contractTestQueueGetOutdatedRepositories(t, rq,
		func(t testing.TB, events []ReplicationEvent) {
			rq.lastEventByDest = map[eventDestination]ReplicationEvent{}
			for _, event := range events {
				rq.lastEventByDest[eventDestination{
					virtual:      event.Job.VirtualStorage,
					storage:      event.Job.TargetNodeStorage,
					relativePath: event.Job.RelativePath,
				}] = event
			}
		},
	)
}

func TestMemoryReplicationEventQueue(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	queue := NewMemoryReplicationEventQueue(config.Config{})

	noEvents, err := queue.Dequeue(ctx, "praefect", "storage-1", 100500)
	require.NoError(t, err)
	require.Empty(t, noEvents, "no events as queue is empty")

	noAcknowledged, err := queue.Acknowledge(ctx, JobStateCompleted, []uint64{1, 2, 3})
	require.NoError(t, err)
	require.Empty(t, noAcknowledged, "no acknowledged ids as queue is empty")

	job1 := ReplicationJob{
		Change:            UpdateRepo,
		RelativePath:      "/project/path-1",
		VirtualStorage:    "praefect",
		TargetNodeStorage: "storage-1",
		SourceNodeStorage: "storage-0",
		Params:            nil,
	}

	eventType1 := ReplicationEvent{Job: job1}

	event1, err := queue.Enqueue(ctx, eventType1)
	require.NoError(t, err)

	expEvent1 := ReplicationEvent{
		ID:        1,
		State:     JobStateReady,
		Attempt:   3,
		LockID:    "praefect|storage-1|/project/path-1",
		CreatedAt: event1.CreatedAt, // it is a hack to have same time for both
		Job:       job1,
	}
	require.Equal(t, expEvent1, event1)

	notAcknowledged1, err := queue.Acknowledge(ctx, JobStateCompleted, []uint64{event1.ID})
	require.NoError(t, err)
	require.Empty(t, notAcknowledged1, "no acknowledged ids as events were not dequeued")

	job2 := ReplicationJob{
		Change:            UpdateRepo,
		RelativePath:      "/project/path-1",
		VirtualStorage:    "praefect",
		TargetNodeStorage: "storage-2",
		SourceNodeStorage: "storage-0",
		Params:            nil,
	}
	eventType2 := ReplicationEvent{Job: job2}

	event2, err := queue.Enqueue(ctx, eventType2)
	require.NoError(t, err)

	expEvent2 := ReplicationEvent{
		ID:        2,
		State:     JobStateReady,
		Attempt:   3,
		LockID:    "praefect|storage-2|/project/path-1",
		CreatedAt: event2.CreatedAt, // it is a hack to have same time for both
		Job:       job2,
	}
	require.Equal(t, expEvent2, event2)

	dequeuedAttempt1, err := queue.Dequeue(ctx, "praefect", "storage-1", 100500)
	require.NoError(t, err)
	require.Len(t, dequeuedAttempt1, 1, "only single event must be fetched for this storage")

	expAttempt1 := ReplicationEvent{
		ID:        1,
		State:     JobStateInProgress,
		Attempt:   2,
		LockID:    "praefect|storage-1|/project/path-1",
		CreatedAt: event1.CreatedAt,              // it is a hack to have same time for both
		UpdatedAt: dequeuedAttempt1[0].UpdatedAt, // it is a hack to have same time for both
		Job:       job1,
	}
	require.Equal(t, expAttempt1, dequeuedAttempt1[0])

	acknowledgedAttempt1, err := queue.Acknowledge(ctx, JobStateFailed, []uint64{event1.ID, event2.ID})
	require.NoError(t, err)
	require.Equal(t, []uint64{event1.ID}, acknowledgedAttempt1, "one event must be acknowledged")

	dequeuedAttempt2, err := queue.Dequeue(ctx, "praefect", "storage-1", 100500)
	require.NoError(t, err)
	require.Len(t, dequeuedAttempt2, 1, "only single event must be fetched for this storage")

	expAttempt2 := ReplicationEvent{
		ID:        1,
		State:     JobStateInProgress,
		Attempt:   1,
		LockID:    "praefect|storage-1|/project/path-1",
		CreatedAt: event1.CreatedAt,              // it is a hack to have same time for both
		UpdatedAt: dequeuedAttempt2[0].UpdatedAt, // it is a hack to have same time for both
		Job:       job1,
	}
	require.Equal(t, expAttempt2, dequeuedAttempt2[0])

	acknowledgedAttempt2, err := queue.Acknowledge(ctx, JobStateFailed, []uint64{event1.ID})
	require.NoError(t, err)
	require.Equal(t, []uint64{event1.ID}, acknowledgedAttempt2, "one event must be acknowledged")

	dequeuedAttempt3, err := queue.Dequeue(ctx, "praefect", "storage-1", 100500)
	require.NoError(t, err)
	require.Len(t, dequeuedAttempt3, 1, "only single event must be fetched for this storage")

	expAttempt3 := ReplicationEvent{
		ID:        1,
		State:     JobStateInProgress,
		Attempt:   0,
		LockID:    "praefect|storage-1|/project/path-1",
		CreatedAt: event1.CreatedAt,              // it is a hack to have same time for both
		UpdatedAt: dequeuedAttempt3[0].UpdatedAt, // it is a hack to have same time for both
		Job:       job1,
	}
	require.Equal(t, expAttempt3, dequeuedAttempt3[0])

	ackFailedNoAttemptsLeft, err := queue.Acknowledge(ctx, JobStateFailed, []uint64{event1.ID})
	require.Error(t, errDeadAckedAsFailed, err)
	require.Empty(t, ackFailedNoAttemptsLeft)

	acknowledgedAttempt3, err := queue.Acknowledge(ctx, JobStateDead, []uint64{event1.ID})
	require.NoError(t, err)
	require.Equal(t, []uint64{event1.ID}, acknowledgedAttempt3, "one event must be acknowledged")

	dequeuedAttempt4, err := queue.Dequeue(ctx, "praefect", "storage-1", 100500)
	require.NoError(t, err)
	require.Empty(t, dequeuedAttempt4, "all attempts to process job were used")

	dequeuedAttempt5, err := queue.Dequeue(ctx, "praefect", "storage-2", 100500)
	require.NoError(t, err)
	require.Len(t, dequeuedAttempt5, 1, "only single event must be fetched for this storage")

	expAttempt5 := ReplicationEvent{
		ID:        2,
		State:     JobStateInProgress,
		Attempt:   2,
		LockID:    "praefect|storage-2|/project/path-1",
		CreatedAt: event2.CreatedAt,              // it is a hack to have same time for both
		UpdatedAt: dequeuedAttempt5[0].UpdatedAt, // it is a hack to have same time for both
		Job:       job2,
	}
	require.Equal(t, expAttempt5, dequeuedAttempt5[0])

	acknowledgedAttempt5, err := queue.Acknowledge(ctx, JobStateDead, []uint64{event2.ID})
	require.NoError(t, err)
	require.Equal(t, []uint64{event2.ID}, acknowledgedAttempt5, "one event must be acknowledged")

	dequeuedAttempt6, err := queue.Dequeue(ctx, "praefect", "storage-2", 100500)
	require.NoError(t, err)
	require.Empty(t, dequeuedAttempt6, "all jobs marked as completed for this storage")
}

func TestMemoryReplicationEventQueue_ConcurrentAccess(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	queue := NewMemoryReplicationEventQueue(config.Config{})

	job1 := ReplicationJob{
		Change:            UpdateRepo,
		RelativePath:      "/project/path-1",
		VirtualStorage:    "praefect",
		TargetNodeStorage: "storage-1",
		SourceNodeStorage: "storage-0",
	}

	job2 := ReplicationJob{
		Change:            UpdateRepo,
		RelativePath:      "/project/path-1",
		VirtualStorage:    "praefect",
		TargetNodeStorage: "storage-2",
		SourceNodeStorage: "storage-0",
	}

	eventType1 := ReplicationEvent{Job: job1}
	eventType2 := ReplicationEvent{Job: job2}

	var checkScenario = func(wg *sync.WaitGroup, event ReplicationEvent, state JobState) {
		defer wg.Done()

		created, err := queue.Enqueue(ctx, event)
		require.NoError(t, err)

		dequeued, err := queue.Dequeue(ctx, "praefect", created.Job.TargetNodeStorage, 100500)
		require.NoError(t, err)
		require.Len(t, dequeued, 1)
		require.Equal(t, created.Job, dequeued[0].Job)

		ackIDs, err := queue.Acknowledge(ctx, state, []uint64{created.ID})
		require.NoError(t, err)
		require.Len(t, ackIDs, 1)
		require.Equal(t, created.ID, ackIDs[0])

		nothing, err := queue.Dequeue(ctx, "praefect", created.Job.TargetNodeStorage, 100500)
		require.NoError(t, err)
		require.Len(t, nothing, 0)
	}

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go checkScenario(wg, eventType1, JobStateCompleted)
	go checkScenario(wg, eventType2, JobStateCancelled)
	wg.Wait()
}
