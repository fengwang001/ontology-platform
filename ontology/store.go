package ontology

import "sync"

type Logger interface {
	Printf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

type Version[T any] struct {
	Timestamp uint64
	Value     T
}

type Snapshot[T any] struct {
	Point uint64
	Views map[string]T
}

type viewState[T any] struct {
	progress uint64
	versions []Version[T]
}

type Store[T any] struct {
	mu       sync.RWMutex
	retain   int
	views    map[string]*viewState[T]
	lastRead uint64
	logger   Logger
}

func NewStore[T any](retain int, logger Logger, viewNames ...string) *Store[T] {
	return nil
}

func (s *Store[T]) Apply(view string, timestamp uint64, value T) error {
	return nil
}

func (s *Store[T]) Heartbeat(view string, timestamp uint64) error {
	return nil
}

func (s *Store[T]) Read() (Snapshot[T], error) {
	return Snapshot[T]{}, nil
}

func (s *Store[T]) ReadAt(timestamp uint64) (Snapshot[T], error) {
	return Snapshot[T]{}, nil
}
