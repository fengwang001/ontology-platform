package api

import "errors"
import "sync"
import "ontology/rep"

var ErrEmptyKey = errors.New("api: key must not be empty")
var ErrReplicaIndex = errors.New("api: replica index out of range [0,2]")
var ErrReplicaOffline = errors.New("api: target replica is offline")

const N = 3

type Cluster struct {
	mu     sync.RWMutex
	lg     *rep.Log
	reps   [N]*rep.Replica
	online [N]bool
	ref    map[string]string
}

func New() *Cluster {
	c := &Cluster{lg: rep.NewLog(), ref: map[string]string{}}
	for i := range c.reps {
		c.reps[i], c.online[i] = rep.New(), true
	}
	return c
}

type Session struct{ Replica, Seen int }
type ReplicaState struct {
	Applied int
	Online  bool
	Data    map[string]string
}
type State struct {
	LSN      int
	Ref      map[string]string
	Replicas [N]ReplicaState
}

func (c *Cluster) Write(key, val string) (int, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.lg.Append(key, val)
	c.ref[key] = val
	for i := range c.reps {
		if c.online[i] {
			c.reps[i].Apply(e)
		}
	}
	return e.LSN, nil
}
func (c *Cluster) setStatus(idx int, on bool) error {
	if idx < 0 || idx >= N {
		return ErrReplicaIndex
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.online[idx] = on
	return nil
}
func (c *Cluster) Offline(idx int) error     { return c.setStatus(idx, false) }
func (c *Cluster) Online(idx int) error      { return c.setStatus(idx, true) }
func (c *Cluster) Open(replica int) *Session { return &Session{Replica: replica} }

// Read catches the replica up to Seen (no-op if ahead), then Seen = max.
func (c *Cluster) Read(s *Session, key string) (string, int, error) {
	if key == "" {
		return "", 0, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.reps[s.Replica]
	r.CatchUp(c.lg, s.Seen)
	v, _ := r.Get(key)
	s.Seen = max(s.Seen, r.Applied())
	return v, r.Applied(), nil
}

// SwitchReplica validates before any mutation, catches idx up, then rebinds.
func (c *Cluster) SwitchReplica(s *Session, idx int) error {
	if idx < 0 || idx >= N {
		return ErrReplicaIndex
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.online[idx] {
		return ErrReplicaOffline
	}
	c.reps[idx].CatchUp(c.lg, s.Seen)
	s.Replica = idx
	return nil
}
func (c *Cluster) View() *State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st := &State{LSN: c.lg.Len(), Ref: map[string]string{}}
	for k, v := range c.ref {
		st.Ref[k] = v
	}
	for i := range c.reps {
		a, d := c.reps[i].Snapshot()
		st.Replicas[i] = ReplicaState{a, c.online[i], d}
	}
	return st
}

// SelfCheck builds the 5/3/2 setup, runs the five-step sequence, checks inv. 1-4.
func (c *Cluster) SelfCheck() error {
	h := New()
	h.Write("k", "A")
	h.Write("k", "B")
	h.Offline(2)
	h.Write("k", "C")
	h.Offline(1)
	h.Write("k", "D")
	h.Write("k", "E")
	h.Online(1)
	h.Online(2)
	s := h.Open(0)
	if a := h.View().Replicas; a[0].Applied != 5 || a[1].Applied != 3 || a[2].Applied != 2 {
		return errors.New("setup must be R0/R1/R2 applied 5/3/2")
	}
	prev := 0
	for _, t := range [5]int{-1, 1, -1, 2, -1} { // -1: Read; else switch target
		if t < 0 { // invariants 1 (monotonic) and 2 (== naive reference)
			v, l, _ := h.Read(s, "k")
			if v != "E" || v != h.View().Ref["k"] || l != 5 || l < prev {
				return errors.New("monotonic/reference read mismatch")
			}
			prev = l
		} else if err := h.SwitchReplica(s, t); err != nil || h.reps[t].Applied() < s.Seen {
			return errors.New("invariant 3: failover target behind seen")
		}
	}
	g := New() // invariant 4: rejected switch moves neither replica nor session
	sg := g.Open(0)
	g.Write("k", "v")
	g.Offline(1)
	b, sb := g.reps[1].Applied(), *sg
	if err := g.SwitchReplica(sg, 1); !errors.Is(err, ErrReplicaOffline) ||
		g.reps[1].Applied() != b || *sg != sb {
		return errors.New("rejected switch left a trace or returned wrong error")
	}
	return nil
}
