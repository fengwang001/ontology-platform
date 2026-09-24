// Package api is the public facade over raft: three in-memory replicas
// with Append, term/index replication and CommitIndex. Dependency
// direction is api -> raft -> entry only.
package api

import (
	"slices"

	"ontology/entry"
	"ontology/raft"
)

// Entry is the public log entry (alias: no conversion at the boundary).
type Entry = entry.Entry

// Distinguishable sentinel errors, re-exported for errors.Is callers.
var (
	ErrEmptyCmd            = raft.ErrEmptyCmd
	ErrPrevIndexOutOfRange = raft.ErrPrevIndexOutOfRange
	ErrPrevTermMismatch    = raft.ErrPrevTermMismatch
	ErrUnknownServer       = raft.ErrUnknownServer
)

// API is a three-replica (S1,S2,S3) in-memory system, quorum 2.
type API struct{ c *raft.Cluster }

// New creates S1, S2, S3 at term 0 with empty logs.
func New() *API { return &API{c: raft.NewCluster("S1", "S2", "S3")} }

// SetTerm records a scenario-driven election (election out of scope).
func (a *API) SetTerm(server string, term int) error { return a.c.SetTerm(server, term) }

// Append appends cmd at server's current term; empty cmd changes nothing.
func (a *API) Append(server, cmd string) error {
	_, err := a.c.Append(server, cmd)
	return err
}

// Replicate ships leader's suffix after prevIndex with the term anchor;
// a range/term rejection leaves the follower untouched.
func (a *API) Replicate(leader, follower string, prevIndex int) error {
	return a.c.Replicate(leader, follower, prevIndex)
}

// CommitIndex is the leader's largest committed index; -1 if unknown.
func (a *API) CommitIndex(leader string) int {
	v, err := a.c.CommitIndex(leader)
	if err != nil {
		return -1
	}
	return v
}

// Log returns a point-in-time defensive snapshot of server's log.
func (a *API) Log(server string) []Entry {
	v, err := a.c.Log(server)
	if err != nil {
		return nil
	}
	return v
}

// SelfCheck replays the canonical six-step script and checks the four
// invariants, the three distinct rejections and large-m incremental
// commit observability; the scan counter is never read here.
func (a *API) SelfCheck() bool {
	ok := true
	must := func(e error) {
		if e != nil {
			panic(e)
		}
	}
	nm := []string{"S1", "S2", "S3"}
	E := func(t, i int, c string) Entry { return Entry{Term: t, Index: i, Cmd: c} }
	// Invariant 1: for any pair, equal term at a shared index forces an
	// identical prefix through it.
	lm := func() bool {
		for x := 0; x < 3; x++ {
			for y := x + 1; y < 3; y++ {
				p, q := a.Log(nm[x]), a.Log(nm[y])
				for k := 1; k <= len(p) && k <= len(q); k++ {
					if p[k-1].Term == q[k-1].Term && !slices.Equal(p[:k], q[:k]) {
						return false
					}
				}
			}
		}
		return true
	}
	must(a.SetTerm("S1", 1))
	must(a.Append("S1", "a"))
	must(a.Replicate("S1", "S2", 0))
	ok = ok && a.CommitIndex("S1") == 1 && lm() // step 1
	must(a.Append("S1", "b"))
	must(a.Replicate("S1", "S2", 1)) // step 2: no check, S1 crashes
	must(a.SetTerm("S3", 2))
	must(a.Append("S3", "x"))
	must(a.Append("S3", "y"))
	ok = ok && a.CommitIndex("S3") == 0 && lm() // steps 3-4
	must(a.SetTerm("S1", 3))
	must(a.Replicate("S1", "S2", 1))
	must(a.Append("S1", "z"))
	must(a.Replicate("S1", "S2", 2))
	committed := []Entry{E(1, 1, "a"), E(1, 2, "b"), E(3, 3, "z")}
	ok = ok && a.CommitIndex("S1") == 3 && lm() // step 5: z commits, b rides along
	must(a.Replicate("S1", "S3", 0))
	// Invariant 2: every committed entry survives, unchanged, on each
	// correctly replicated server after S3 rejoins and truncates.
	for _, n := range nm {
		ok = ok && slices.Equal(a.Log(n), committed)
	}

	// Invariant 4: three distinct rejections, no trace, still reusable.
	b := New()
	must(b.SetTerm("S1", 1))
	must(b.SetTerm("S2", 2))
	must(b.Append("S1", "a"))
	must(b.Append("S2", "x"))
	e0 := b.Append("S2", "")
	e1 := b.Replicate("S1", "S2", -1)
	e2 := b.Replicate("S1", "S2", 5)
	e3 := b.Replicate("S1", "S2", 1)
	ok = ok && e0 == ErrEmptyCmd && e1 == ErrPrevIndexOutOfRange &&
		e2 == ErrPrevIndexOutOfRange && e3 == ErrPrevTermMismatch
	ok = ok && slices.Equal(b.Log("S2"), []Entry{E(2, 1, "x")})
	must(b.Append("S2", "q"))
	ok = ok && slices.Equal(b.Log("S2"), []Entry{E(2, 1, "x"), E(2, 2, "q")})

	// Incrementality via the public surface; the exact scanned==1 count
	// is pinned by the white-box test in package raft.
	for _, m := range []int{100, 1000, 10000} {
		g := New()
		must(g.SetTerm("S1", 1))
		for range m {
			must(g.Append("S1", "c"))
		}
		must(g.Replicate("S1", "S2", 0))
		v1 := g.CommitIndex("S1")
		must(g.Append("S1", "c"))
		must(g.Replicate("S1", "S2", m))
		ok = ok && v1 == m && g.CommitIndex("S1") == m+1
	}
	return ok
}

// SelfCheckFree is the package-level form requested by the specification.
func SelfCheck() bool { return New().SelfCheck() }
