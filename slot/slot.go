package slot

// State identifies the lifecycle state of one fixed table slot.
type State uint8

const (
	// Free can be returned by the freelist.
	Free State = iota
	// Live currently stores a value and owns a generation.
	Live
	// Exhausted can never be reused because its generation reached the limit.
	Exhausted
)

const InitialGeneration = 1

// Slot stores a value, its current generation, and lifecycle state.
type Slot[T any] struct {
	value      T
	generation uint64
	state      State
}

// New allocates a free slot using the first valid generation.
func New[T any]() *Slot[T] {
	return &Slot[T]{generation: InitialGeneration}
}

func (s *Slot[T]) State() State          { return s.state }
func (s *Slot[T]) Generation() uint64    { return s.generation }
func (s *Slot[T]) Value() T              { return s.value }
func (s *Slot[T]) Free() bool            { return s.state == Free }
func (s *Slot[T]) Live() bool            { return s.state == Live }
func (s *Slot[T]) Exhausted() bool       { return s.state == Exhausted }
func (s *Slot[T]) Holds(gen uint64) bool { return s.state == Live && s.generation == gen }

// Allocate binds a value to a free slot without changing its generation.
func (s *Slot[T]) Allocate(value T) bool {
	if s.state != Free {
		return false
	}
	s.value = value
	s.state = Live
	return true
}

// Release advances the generation and frees a live slot.
func (s *Slot[T]) Release(maxGeneration uint64) bool {
	if s.state != Live {
		return false
	}
	var zero T
	s.value = zero
	if s.generation >= maxGeneration {
		s.state = Exhausted
		return false
	}
	s.generation++
	s.state = Free
	return true
}

// Retire is a test-only transition for forcing a live slot to the limit.
func (s *Slot[T]) Retire(maxGeneration uint64) bool {
	if s.state != Live {
		return false
	}
	s.generation = maxGeneration
	s.state = Exhausted
	return true
}
