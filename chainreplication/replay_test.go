package chainreplication

import "sync"

// recorder collects exactly-once write outcomes and is safe for concurrent
// use with the async transport.
type recorder struct {
	mu      sync.Mutex
	commits map[int]bool
	order   []int
}

func newRecorder() *recorder {
	return &recorder{commits: map[int]bool{}}
}

func (r *recorder) callback(seq int, committed bool, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.commits[seq]; dup {
		panic("double outcome for seq")
	}
	r.commits[seq] = committed
	r.order = append(r.order, seq)
}

func (r *recorder) outcome(seq int) (committed, seen bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.commits[seq]
	return v, ok
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.commits)
}

func (r *recorder) committedSeqs() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, 0)
	for seq, committed := range r.commits {
		if committed {
			out = append(out, seq)
		}
	}
	return out
}
