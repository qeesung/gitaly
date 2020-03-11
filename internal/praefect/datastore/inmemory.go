package datastore

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// NewInMemoryReplicationEventQueue return in-memory implementation of the ReplicationEventQueue.
func NewInMemoryReplicationEventQueue() ReplicationEventQueue {
	return &inMemoryReplicationEventQueue{dequeued: map[uint64]struct{}{}}
}

// inMemoryReplicationEventQueue implements queue interface with in-memory implementation of storage
type inMemoryReplicationEventQueue struct {
	sync.RWMutex
	seq      uint64              // used to generate unique  identifiers for events
	queued   []ReplicationEvent  // all new events stored as queue
	dequeued map[uint64]struct{} // all events dequeued, but not yet acknowledged
}

// nextID returns a new sequential ID for new events.
// Needs to be called with lock protection.
func (s *inMemoryReplicationEventQueue) nextID() uint64 {
	s.seq++
	return s.seq
}

func (s *inMemoryReplicationEventQueue) Enqueue(_ context.Context, event ReplicationEvent) (ReplicationEvent, error) {
	event.Attempt = 3
	event.State = JobStateReady
	event.CreatedAt = time.Now().UTC()
	// event.LockID this doesn't make sense with memory data store as it is intended to synchronise multiple praefect instances

	s.Lock()
	event.ID = s.nextID()
	s.queued = append(s.queued, event)
	s.Unlock()
	return event, nil
}

func (s *inMemoryReplicationEventQueue) Dequeue(_ context.Context, nodeStorage string, count int) ([]ReplicationEvent, error) {
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

func (s *inMemoryReplicationEventQueue) Acknowledge(_ context.Context, state JobState, ids []uint64) ([]uint64, error) {
	switch state {
	case JobStateFailed, JobStateCompleted:
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

			if state == JobStateCompleted {
				// this event is fully processed and could be removed
				s.remove(i)
				break
			}

			if state == JobStateFailed {
				if s.queued[i].Attempt == 0 {
					// out of luck for this replication event, remove from queue as no more attempts available
					s.remove(i)
				}
			}
		}
	}

	return result, nil
}

// remove deletes i-th element from slice.
// It doesn't check 'i' for the out of rage and must be called with lock protection.
func (s *inMemoryReplicationEventQueue) remove(i int) {
	delete(s.dequeued, s.queued[i].ID)
	copy(s.queued, s.queued[:i])
	copy(s.queued[i:], s.queued[i+1:])
	s.queued = s.queued[:len(s.queued)-1]
}
