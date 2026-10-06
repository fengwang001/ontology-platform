package cg

// queuedWrite is a write held during freeze and replayed later in
// arrival order.
type queuedWrite struct {
	arrival int64
	data    string
}

// volume is a single member volume. All fields are guarded by
// Coordinator.mu.
type volume struct {
	id       string
	group    *group
	seq      uint64
	capacity int
	queue    []queuedWrite
}

func newVolume(id string, capacity int) *volume {
	return &volume{id: id, capacity: capacity}
}

// apply immediately applies one write and returns the new sequence number.
func (v *volume) apply(data string) uint64 {
	v.seq++
	return v.seq
}

// enqueue appends a write to the per-volume FIFO queue. Caller must check
// queueFull first.
func (v *volume) enqueue(at int64, data string) {
	v.queue = append(v.queue, queuedWrite{arrival: at, data: data})
}

func (v *volume) queueFull() bool { return len(v.queue) >= v.capacity }

// drain applies every queued write in arrival order, assigning sequence
// numbers, and clears the queue.
func (v *volume) drain() {
	for i := 0; i < len(v.queue); i++ {
		v.seq++
	}
	v.queue = v.queue[:0]
}
