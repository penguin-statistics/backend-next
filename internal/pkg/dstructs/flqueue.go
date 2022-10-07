package dstructs

import "sync"

// FlQueue is a manual-flush queue that is thread-safe.
type FlQueue[T any] struct {
	mu    sync.Mutex
	queue []*T
}

func NewFlQueue[T any]() *FlQueue[T] {
	return &FlQueue[T]{
		queue: make([]*T, 0, 100),
	}
}

func (m *FlQueue[T]) Push(r *T) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, r)
}

func (m *FlQueue[T]) Flush() []*T {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queue
	m.queue = m.queue[:0]
	return q
}
