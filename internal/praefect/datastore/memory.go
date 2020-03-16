package datastore

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// NewMemoryReplicationEventQueue return in-memory implementation of the ReplicationEventQueue.
func NewMemoryReplicationEventQueue() ReplicationEventQueue {
	return &memoryReplicationEventQueue{dequeued: map[uint64]struct{}{}}
}

// memoryReplicationEventQueue implements queue interface with in-memory implementation of storage
type memoryReplicationEventQueue struct {
	sync.RWMutex
	seq      uint64              // used to generate unique  identifiers for events
	queued   []ReplicationEvent  // all new events stored as queue
	dequeued map[uint64]struct{} // all events dequeued, but not yet acknowledged
}

// nextID returns a new sequential ID for new events.
// Needs to be called with lock protection.
func (s *memoryReplicationEventQueue) nextID() uint64 {
	s.seq++
	return s.seq
}

func (s *memoryReplicationEventQueue) Enqueue(_ context.Context, event ReplicationEvent) (ReplicationEvent, error) {
	event.Attempt = 3
	event.State = JobStateReady
	event.CreatedAt = time.Now().UTC()
	// event.LockID is unnecessary with an in memory data store as it is intended to synchronize multiple praefect instances

	s.Lock()
	defer s.Unlock()
	event.ID = s.nextID()
	s.queued = append(s.queued, event)
	return event, nil
}

func (s *memoryReplicationEventQueue) Dequeue(_ context.Context, nodeStorage string, count int) ([]ReplicationEvent, error) {
	s.Lock()
	defer s.Unlock()

	var result []ReplicationEvent
	for i := 0; i < len(s.queued); i++ {
		event := s.queued[i]
		if event.Attempt > 0 && event.Job.TargetNodeStorage == nodeStorage && (event.State == JobStateReady || event.State == JobStateFailed) {
			updatedAt := time.Now().UTC()
			event.Attempt--
			event.State = JobStateInProgress
			event.UpdatedAt = &updatedAt

			s.queued[i] = event
			s.dequeued[event.ID] = struct{}{}
			result = append(result, event)

			if len(result) >= count {
				break
			}
		}
	}

	return result, nil
}

func (s *memoryReplicationEventQueue) Acknowledge(_ context.Context, state JobState, ids []uint64) ([]uint64, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	switch state {
	case JobStateCompleted, JobStateFailed, JobStateCancelled:
		// proceed with acknowledgment
	default:
		return nil, fmt.Errorf("event state is not supported: %q", state)
	}

	s.Lock()
	defer s.Unlock()

	var result []uint64
	for _, id := range ids {
		if _, found := s.dequeued[id]; !found {
			// event was not dequeued from the queue, so it can't be acknowledged
			continue
		}

		for i := 0; i < len(s.queued); i++ {
			if s.queued[i].ID != id {
				continue
			}

			if s.queued[i].State != JobStateInProgress {
				return nil, fmt.Errorf("event not in progress, can't be acknowledged: %d [%s]", s.queued[i].ID, s.queued[i].State)
			}

			updatedAt := time.Now().UTC()
			s.queued[i].State = state
			s.queued[i].UpdatedAt = &updatedAt

			result = append(result, id)

			switch state {
			case JobStateCompleted:
				// this event is fully processed and could be removed
				s.remove(i)
			case JobStateFailed:
				if s.queued[i].Attempt == 0 {
					// out of luck for this replication event, remove from queue as no more attempts available
					s.remove(i)
				}
			case JobStateCancelled:
				// out of luck for this replication event, remove from queue as no more attempts available
				s.remove(i)
			}
			break
		}
	}

	return result, nil
}

// remove deletes i-th element from slice and from tracking map.
// It doesn't check 'i' for the out of range and must be called with lock protection.
func (s *memoryReplicationEventQueue) remove(i int) {
	delete(s.dequeued, s.queued[i].ID)
	s.queued = append(s.queued[:i], s.queued[i+1:]...)
}
