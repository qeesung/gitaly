package datastore

import (
	"context"
	"sync"
)

// MemoryRepositoryStore is an in-memory implementation of RepositoryStore.
// Refer to the interface for method documentation.
type MemoryRepositoryStore struct {
	m sync.Mutex

	storages map[string][]string
	virtualStorageState
	storageState
}

// virtualStorageStates represents the virtual storage's view of what state the repositories should be in.
// It structured as virtual-storage->relative_path->generation.
type virtualStorageState map[string]map[string]int

// storageState contains individual storage's repository states.
// It structured as virtual-storage->relative_path->storage->generation.
type storageState map[string]map[string]map[string]int

// NewMemoryRepositoryStore returns an in-memory implementation of RepositoryStore.
func NewMemoryRepositoryStore(storages map[string][]string) *MemoryRepositoryStore {
	return &MemoryRepositoryStore{
		storages:            storages,
		storageState:        make(storageState),
		virtualStorageState: make(virtualStorageState),
	}
}

func (m *MemoryRepositoryStore) GetGeneration(ctx context.Context, virtualStorage, relativePath, storage string) (int, error) {
	m.m.Lock()
	defer m.m.Unlock()

	return m.getStorageGeneration(virtualStorage, relativePath, storage), nil
}

func (m *MemoryRepositoryStore) IncrementGeneration(ctx context.Context, virtualStorage, relativePath, primary string, secondaries []string) (int, error) {
	m.m.Lock()
	defer m.m.Unlock()

	baseGen := m.getRepositoryGeneration(virtualStorage, relativePath)
	nextGen := baseGen + 1

	m.setGeneration(virtualStorage, relativePath, primary, nextGen)

	// If a secondary does not have a generation, it's in an undefined state. We'll only
	// pick secondaries on the same generation as the primary to ensure they begin from the
	// same starting state.
	if baseGen != GenerationUnknown {
		for _, secondary := range secondaries {
			currentGen := m.getStorageGeneration(virtualStorage, relativePath, secondary)
			// If the secondary is not on the same generation as the primary, the secondary
			// has failed a concurrent reference transaction. We won't increment its
			// generation as it has not applied writes in previous genereations, leaving
			// its state undefined.
			if currentGen != baseGen {
				continue
			}

			m.setGeneration(virtualStorage, relativePath, secondary, nextGen)
		}
	}

	return nextGen, nil
}

func (m *MemoryRepositoryStore) SetGeneration(ctx context.Context, virtualStorage, relativePath, storage string, generation int) error {
	m.m.Lock()
	defer m.m.Unlock()

	m.setGeneration(virtualStorage, relativePath, storage, generation)

	return nil
}

func (m *MemoryRepositoryStore) DeleteRepository(ctx context.Context, virtualStorage, relativePath, storage string) error {
	m.m.Lock()
	defer m.m.Unlock()

	latestGen := m.getRepositoryGeneration(virtualStorage, relativePath)
	storageGen := m.getStorageGeneration(virtualStorage, relativePath, storage)

	m.deleteRepository(virtualStorage, relativePath)
	m.deleteStorageRepository(virtualStorage, relativePath, storage)

	if latestGen == GenerationUnknown && storageGen == GenerationUnknown {
		return RepositoryNotExistsError{
			virtualStorage: virtualStorage,
			relativePath:   relativePath,
			storage:        storage,
		}
	}

	return nil
}

func (m *MemoryRepositoryStore) RenameRepository(ctx context.Context, virtualStorage, relativePath, storage, newRelativePath string) error {
	m.m.Lock()
	defer m.m.Unlock()

	latestGen := m.getRepositoryGeneration(virtualStorage, relativePath)
	storageGen := m.getStorageGeneration(virtualStorage, relativePath, storage)

	if latestGen != GenerationUnknown {
		m.deleteRepository(virtualStorage, relativePath)
		m.setRepositoryGeneration(virtualStorage, newRelativePath, latestGen)
	}

	if storageGen != GenerationUnknown {
		m.deleteStorageRepository(virtualStorage, relativePath, storage)
		m.setStorageGeneration(virtualStorage, newRelativePath, storage, storageGen)
	}

	if latestGen == GenerationUnknown && storageGen == GenerationUnknown {
		return RepositoryNotExistsError{
			virtualStorage: virtualStorage,
			relativePath:   relativePath,
			storage:        storage,
		}
	}

	return nil
}

func (m *MemoryRepositoryStore) EnsureUpgrade(ctx context.Context, virtualStorage, relativePath, storage string, generation int) error {
	m.m.Lock()
	defer m.m.Unlock()

	if current := m.getStorageGeneration(virtualStorage, relativePath, storage); current != GenerationUnknown && current >= generation {
		return downgradeAttemptedError{
			virtualStorage:      virtualStorage,
			relativePath:        relativePath,
			storage:             storage,
			currentGeneration:   current,
			attemptedGeneration: generation,
		}
	}

	return nil
}

func (m *MemoryRepositoryStore) getRepositoryGeneration(virtualStorage, relativePath string) int {
	gen, ok := m.virtualStorageState[virtualStorage][relativePath]
	if !ok {
		return GenerationUnknown
	}

	return gen
}

func (m *MemoryRepositoryStore) getStorageGeneration(virtualStorage, relativePath, storage string) int {
	gen, ok := m.storageState[virtualStorage][relativePath][storage]
	if !ok {
		return GenerationUnknown
	}

	return gen
}

func (m *MemoryRepositoryStore) deleteRepository(virtualStorage, relativePath string) {
	rels := m.virtualStorageState[virtualStorage]
	if rels == nil {
		return
	}

	delete(rels, relativePath)
	if len(rels) == 0 {
		delete(m.virtualStorageState, virtualStorage)
	}
}

func (m *MemoryRepositoryStore) deleteStorageRepository(virtualStorage, relativePath, storage string) {
	storages := m.storageState[virtualStorage][relativePath]
	if storages == nil {
		return
	}

	delete(storages, storage)
	if len(m.storageState[virtualStorage][relativePath]) == 0 {
		delete(m.storageState[virtualStorage], relativePath)
	}

	if len(m.storageState[virtualStorage]) == 0 {
		delete(m.storageState, virtualStorage)
	}
}

func (m *MemoryRepositoryStore) setGeneration(virtualStorage, relativePath, storage string, generation int) {
	m.setRepositoryGeneration(virtualStorage, relativePath, generation)
	m.setStorageGeneration(virtualStorage, relativePath, storage, generation)
}

func (m *MemoryRepositoryStore) setRepositoryGeneration(virtualStorage, relativePath string, generation int) {
	current := m.getRepositoryGeneration(virtualStorage, relativePath)
	if generation <= current {
		return
	}

	if m.virtualStorageState[virtualStorage] == nil {
		m.virtualStorageState[virtualStorage] = make(map[string]int)
	}

	m.virtualStorageState[virtualStorage][relativePath] = generation
}

func (m *MemoryRepositoryStore) setStorageGeneration(virtualStorage, relativePath, storage string, generation int) {
	if m.storageState[virtualStorage] == nil {
		m.storageState[virtualStorage] = make(map[string]map[string]int)
	}

	if m.storageState[virtualStorage][relativePath] == nil {
		m.storageState[virtualStorage][relativePath] = make(map[string]int)
	}

	m.storageState[virtualStorage][relativePath][storage] = generation
}
