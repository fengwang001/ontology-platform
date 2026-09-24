package check

import (
	"sort"

	"ontology/drr"
)

type Naive struct {
	flows   map[int]*naiveFlow
	active  []int
	head    int
	current bool
	queued  int
}

type naiveFlow struct {
	id, quantum, deficit, queued int
	blocks                       []int
	active                       bool
}

func NewNaive() *Naive {
	return &Naive{flows: make(map[int]*naiveFlow)}
}

func (n *Naive) AddFlow(id, quantum int) error {
	if quantum <= 0 {
		return drr.ErrBadSize
	}
	if _, ok := n.flows[id]; ok {
		return drr.ErrExists
	}
	n.flows[id] = &naiveFlow{id: id, quantum: quantum}
	return nil
}

func (n *Naive) Enqueue(id, size int) error {
	if size <= 0 || size > drr.MaxBlock {
		return drr.ErrBadSize
	}
	f, ok := n.flows[id]
	if !ok {
		return drr.ErrUnknownFlow
	}
	if n.queued >= drr.MaxQueued {
		return drr.ErrFull
	}
	if !f.active {
		pos := sort.SearchInts(n.active, id)
		n.active = append(n.active, 0)
		copy(n.active[pos+1:], n.active[pos:])
		n.active[pos] = id
		if pos <= n.head && len(n.active) > 1 {
			n.head++
		}
		f.active = true
	}
	f.blocks = append(f.blocks, size)
	f.queued++
	n.queued++
	return nil
}

func (n *Naive) Dequeue() (int, int, bool) {
	sizeActive := len(n.active)
	for range sizeActive {
		id := n.active[n.head]
		f := n.flows[id]
		if !n.current {
			f.deficit += f.quantum
		}
		if len(f.blocks) == 0 || f.blocks[0] > f.deficit {
			n.head = (n.head + 1) % sizeActive
			n.current = false
			continue
		}
		size := f.blocks[0]
		f.blocks = f.blocks[1:]
		f.deficit -= size
		f.queued--
		n.queued--
		if len(f.blocks) == 0 {
			f.deficit = 0
			f.active = false
			n.current = false
			n.active = append(n.active[:n.head], n.active[n.head+1:]...)
			if len(n.active) > 0 {
				n.head %= len(n.active)
			} else {
				n.head = 0
			}
		} else {
			n.current = true
		}
		return id, size, true
	}
	return 0, 0, false
}
