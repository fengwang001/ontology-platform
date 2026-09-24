// Package check provides a naive reference MLFQ scheduler: every Step
// linearly scans all jobs to pick the runnable one. Used to validate mlfq.
package check

import "sort"

type Naive struct {
	Quotas     []int
	Boost      int
	jobs       map[int]*nj
	seq, clock int
}

type nj struct{ remain, level, used, seq int }

func NewNaive(quotas []int, boost int) *Naive {
	return &Naive{Quotas: quotas, Boost: boost, jobs: map[int]*nj{}}
}

func (n *Naive) Submit(id, work int) {
	n.jobs[id] = &nj{remain: work, seq: n.seq}
	n.seq++
}

// best scans every job and returns the head of the highest non-empty level:
// the job with the smallest (level, seq) pair.
func (n *Naive) best() (int, *nj) {
	bi, bj := 0, (*nj)(nil)
	for id, j := range n.jobs {
		if bj == nil || j.level < bj.level || j.level == bj.level && j.seq < bj.seq {
			bi, bj = id, j
		}
	}
	return bi, bj
}

func (n *Naive) Step() (int, bool) {
	id, j := n.best()
	if j == nil {
		return 0, false
	}
	j.remain, j.used, n.clock = j.remain-1, j.used+1, n.clock+1
	if j.remain == 0 {
		delete(n.jobs, id)
	} else if j.used >= n.Quotas[j.level] {
		j.used = 0
		j.level = min(j.level+1, len(n.Quotas)-1)
		j.seq, n.seq = n.seq, n.seq+1
	}
	if n.clock%n.Boost == 0 {
		n.boost()
	}
	return id, true
}

// Yield moves id to the tail of its level iff it is at the level's front.
func (n *Naive) Yield(id int) {
	j, ok := n.jobs[id]
	if !ok {
		return
	}
	front := true
	for _, o := range n.jobs {
		if o.level == j.level && o.seq < j.seq {
			front = false
		}
	}
	if front {
		j.seq, n.seq = n.seq, n.seq+1
	}
}

// boost: every job back to level 0, quota accounts cleared, FIFO order kept.
func (n *Naive) boost() {
	ids := make([]int, 0, len(n.jobs))
	for id := range n.jobs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool {
		x, y := n.jobs[ids[a]], n.jobs[ids[b]]
		return x.level < y.level || x.level == y.level && x.seq < y.seq
	})
	for _, id := range ids {
		n.jobs[id].level, n.jobs[id].used = 0, 0
		n.jobs[id].seq, n.seq = n.seq, n.seq+1
	}
}
