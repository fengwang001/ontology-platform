// Package check 提供朴素参照实现：每步线性扫描所有作业。
package check

import "ontology/mlfq"

type rec struct{ left, lvl, used, seq int }

type Naive struct {
	cfg  mlfq.Config
	jobs map[int]*rec
	seq  int
	tick int
	done int
}

func New(cfg mlfq.Config) *Naive { return &Naive{cfg: cfg, jobs: map[int]*rec{}} }

func (n *Naive) Submit(id, work int) error {
	if _, ok := n.jobs[id]; ok {
		return mlfq.ErrDuplicate
	}
	if len(n.jobs) >= n.cfg.MaxJobs {
		return mlfq.ErrFull
	}
	n.seq++
	n.jobs[id] = &rec{left: work, seq: n.seq}
	return nil
}

func (n *Naive) Step() (int, bool) {
	id, ok := -1, false
	for i, r := range n.jobs {
		if !ok || r.lvl < n.jobs[id].lvl || r.lvl == n.jobs[id].lvl && r.seq < n.jobs[id].seq {
			id, ok = i, true
		}
	}
	if !ok {
		return 0, false
	}
	r := n.jobs[id]
	r.left, r.used, n.tick = r.left-1, r.used+1, n.tick+1
	if r.left == 0 {
		delete(n.jobs, id)
		n.done++
	} else if r.used >= n.cfg.Quotas[r.lvl] {
		r.used = 0
		if r.lvl < n.cfg.Levels-1 {
			r.lvl++
		}
		n.seq++
		r.seq = n.seq
	}
	if n.cfg.Boost > 0 && n.tick%n.cfg.Boost == 0 {
		n.boost()
	}
	return id, true
}

func (n *Naive) Yield(id int) error {
	r, ok := n.jobs[id]
	if !ok {
		return mlfq.ErrUnknown
	}
	for _, o := range n.jobs {
		if o.lvl == r.lvl && o.seq < r.seq {
			return nil
		}
	}
	n.seq++
	r.seq = n.seq
	return nil
}

func (n *Naive) Done() int   { return n.done }
func (n *Naive) Active() int { return len(n.jobs) }

// boost 按（级别, seq）顺序收集所有作业，依次置回最高级并清零配额账。
func (n *Naive) boost() {
	var moved []*rec
	for lvl := 0; lvl < n.cfg.Levels; lvl++ {
		for {
			best, ok := -1, false
			for id, r := range n.jobs {
				if r.lvl == lvl && (!ok || r.seq < n.jobs[best].seq) {
					best, ok = id, true
				}
			}
			if !ok {
				break
			}
			n.jobs[best].lvl = -1
			moved = append(moved, n.jobs[best])
		}
	}
	for _, r := range moved {
		r.lvl, r.used = 0, 0
		n.seq++
		r.seq = n.seq
	}
}
