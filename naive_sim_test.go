package ontology

import (
	"fmt"
	"sort"
)

// naiveSim is an independent, deliberately straightforward implementation of
// the same specification; the differential test replays identical call
// sequences against it and against Manager.
type naiveSim struct {
	k         int
	committed []int64
	granted   []map[int]nLock
	queues    [][]nReq
	txns      map[int]*nTxn
	next      int
	log       []string
}

type nLock struct {
	mode nMode
	val  int64
}

type nMode int

const (
	nS nMode = iota
	nX
	nC
)

type nReq struct {
	txn  int
	mode nMode
	val  int64
}

type nTxn struct {
	status nStatus
}

type nStatus int

const (
	nActive nStatus = iota
	nWaiting
	nCommitting
	nDead
)

type nEvent struct {
	grant          bool
	txn, key       int
	mode           nMode
	val            int64
	committedKey   int // -1 for read-only commit event; >=0 per-key commit
	committedValue int64
}

type nResult struct {
	rejected string
	deadlock bool
	value    int64
	status   nStatus
	events   []nEvent
}

func newNaive(k int) *naiveSim {
	s := &naiveSim{
		k:         k,
		committed: make([]int64, k),
		granted:   make([]map[int]nLock, k),
		queues:    make([][]nReq, k),
		txns:      map[int]*nTxn{},
	}
	for i := range s.granted {
		s.granted[i] = map[int]nLock{}
	}
	return s
}

func nCompat(a, b nMode) bool {
	return a != nC && b != nC && !(a == nX && b == nX)
}

func (s *naiveSim) begin() int {
	id := s.next + 1
	s.next = id
	s.txns[id] = &nTxn{status: nActive}
	s.log = append(s.log, fmt.Sprintf("Begin -> T%d", id))
	return id
}

func (s *naiveSim) reject(why string) nResult {
	s.log = append(s.log, "  -> REJECTED "+why)
	return nResult{rejected: why}
}

func (s *naiveSim) check(t, k int, checkKey bool) (*nTxn, nResult) {
	tr := s.txns[t]
	if tr == nil {
		return nil, s.reject("unknown txn")
	}
	if tr.status != nActive {
		return nil, s.reject("not active")
	}
	if checkKey && (k < 0 || k >= s.k) {
		return nil, s.reject("bad key")
	}
	return tr, nResult{}
}

func (s *naiveSim) grantOK(k int, mode nMode, self int) bool {
	for holder, lk := range s.granted[k] {
		if holder == self {
			continue
		}
		if !nCompat(mode, lk.mode) {
			return false
		}
	}
	return true
}

func (s *naiveSim) read(t, k int) nResult {
	tr, rr := s.check(t, k, true)
	if rr.rejected != "" {
		return rr
	}
	if lk, ok := s.granted[k][t]; ok {
		if lk.mode == nX {
			s.log = append(s.log, fmt.Sprintf("Read T%d k%d -> buffered %d", t, k+1, lk.val))
			return nResult{value: lk.val, status: nActive}
		}
		s.log = append(s.log, fmt.Sprintf("Read T%d k%d -> committed %d", t, k+1, s.committed[k]))
		return nResult{value: s.committed[k], status: nActive}
	}
	if !s.grantOK(k, nS, t) || len(s.queues[k]) > 0 {
		s.queues[k] = append(s.queues[k], nReq{txn: t, mode: nS})
		s.log = append(s.log, fmt.Sprintf("Read T%d k%d -> queued S", t, k+1))
		if s.detect(t) {
			return s.killResult(t, false)
		}
		tr.status = nWaiting
		return nResult{status: nWaiting}
	}
	s.granted[k][t] = nLock{mode: nS}
	s.log = append(s.log, fmt.Sprintf("Read T%d k%d -> granted S committed=%d", t, k+1, s.committed[k]))
	return nResult{value: s.committed[k], status: nActive}
}

func (s *naiveSim) write(t, k int, x int64) nResult {
	tr, rr := s.check(t, k, true)
	if rr.rejected != "" {
		return rr
	}
	if lk, ok := s.granted[k][t]; ok && lk.mode == nX {
		lk.val = x
		s.granted[k][t] = lk
		s.log = append(s.log, fmt.Sprintf("Write T%d k%d=%d -> overwrite buffer", t, k+1, x))
		return nResult{status: nActive}
	}
	if s.grantOK(k, nX, t) && len(s.queues[k]) == 0 {
		s.granted[k][t] = nLock{mode: nX, val: x}
		s.log = append(s.log, fmt.Sprintf("Write T%d k%d=%d -> granted X", t, k+1, x))
		return nResult{status: nActive}
	}
	s.queues[k] = append(s.queues[k], nReq{txn: t, mode: nX, val: x})
	s.log = append(s.log, fmt.Sprintf("Write T%d k%d=%d -> queued X", t, k+1, x))
	if s.detect(t) {
		return s.killResult(t, false)
	}
	tr.status = nWaiting
	return nResult{status: nWaiting}
}

func (s *naiveSim) commit(t int) nResult {
	tr, rr := s.check(t, 0, false)
	if rr.rejected != "" {
		return rr
	}
	var xKeys []int
	for ki := 0; ki < s.k; ki++ {
		if lk, ok := s.granted[ki][t]; ok && lk.mode == nX {
			xKeys = append(xKeys, ki)
		}
	}
	sort.Ints(xKeys)
	if len(xKeys) == 0 {
		s.log = append(s.log, fmt.Sprintf("Commit T%d -> read-only commit", t))
		evs := s.releaseCommit(t, nil)
		return nResult{status: nDead, events: append([]nEvent{{committedKey: -1, txn: t}}, evs...)}
	}
	tr.status = nCommitting
	remaining := len(xKeys)
	for _, ki := range xKeys {
		if s.grantOK(ki, nC, t) {
			lk := s.granted[ki][t]
			lk.mode = nC
			s.granted[ki][t] = lk
			remaining--
			continue
		}
		pos := 0
		for pos < len(s.queues[ki]) && s.queues[ki][pos].mode == nC {
			pos++
		}
		s.queues[ki] = append(s.queues[ki], nReq{})
		copy(s.queues[ki][pos+1:], s.queues[ki][pos:])
		s.queues[ki][pos] = nReq{txn: t, mode: nC}
		s.log = append(s.log, fmt.Sprintf("Commit T%d -> queued C k%d at %d", t, ki+1, pos))
		if s.detect(t) {
			return s.killResult(t, false)
		}
	}
	if remaining == 0 {
		s.log = append(s.log, fmt.Sprintf("Commit T%d -> certifies %d keys", t, len(xKeys)))
		evs := s.releaseCommit(t, xKeys)
		return nResult{status: nDead, events: evs}
	}
	return nResult{status: nCommitting}
}

func (s *naiveSim) abort(t int) nResult {
	tr := s.txns[t]
	if tr == nil {
		return s.reject("unknown txn")
	}
	if tr.status != nActive && tr.status != nWaiting && tr.status != nCommitting {
		return s.reject("not abortable")
	}
	s.log = append(s.log, fmt.Sprintf("Abort T%d", t))
	return s.killResult(t, true)
}

func (s *naiveSim) releaseCommit(t int, xKeys []int) []nEvent {
	var evs []nEvent
	pending := map[int]bool{}
	for ki := 0; ki < s.k; ki++ {
		if _, ok := s.granted[ki][t]; ok {
			pending[ki] = true
		}
	}
	for _, ki := range xKeys {
		s.committed[ki] = s.granted[ki][t].val
		evs = append(evs, nEvent{txn: t, key: ki + 1, committedKey: ki + 1, committedValue: s.committed[ki]})
	}
	for ki := range pending {
		delete(s.granted[ki], t)
	}
	s.txns[t] = &nTxn{status: nDead}
	evs = append(evs, s.pump(pending)...)
	return evs
}

func (s *naiveSim) killResult(t int, userAbort bool) nResult {
	pending := map[int]bool{}
	for ki := 0; ki < s.k; ki++ {
		if _, ok := s.granted[ki][t]; ok {
			pending[ki] = true
			delete(s.granted[ki], t)
		}
		kept := s.queues[ki][:0]
		for _, rq := range s.queues[ki] {
			if rq.txn == t {
				pending[ki] = true
			} else {
				kept = append(kept, rq)
			}
		}
		s.queues[ki] = kept
	}
	s.txns[t] = &nTxn{status: nDead}
	tag := "DEADLOCK"
	if userAbort {
		tag = "abort"
	}
	s.log = append(s.log, fmt.Sprintf("  -> %s T%d + cascade", tag, t))
	return nResult{deadlock: !userAbort, status: nDead, events: s.pump(pending)}
}

func (s *naiveSim) pump(pending map[int]bool) []nEvent {
	var evs []nEvent
	for len(pending) > 0 {
		ki := s.k
		for k := range pending {
			if k < ki {
				ki = k
			}
		}
		delete(pending, ki)
		for len(s.queues[ki]) > 0 {
			head := s.queues[ki][0]
			tr := s.txns[head.txn]
			if tr == nil || tr.status == nDead {
				s.queues[ki] = s.queues[ki][1:]
				continue
			}
			if !s.grantOK(ki, head.mode, head.txn) {
				break
			}
			s.queues[ki] = s.queues[ki][1:]
			switch head.mode {
			case nS:
				s.granted[ki][head.txn] = nLock{mode: nS}
				tr.status = nActive
				evs = append(evs, nEvent{grant: true, txn: head.txn, key: ki + 1, mode: nS, val: s.committed[ki]})
			case nX:
				s.granted[ki][head.txn] = nLock{mode: nX, val: head.val}
				tr.status = nActive
				evs = append(evs, nEvent{grant: true, txn: head.txn, key: ki + 1, mode: nX})
			case nC:
				lk := s.granted[ki][head.txn]
				lk.mode = nC
				s.granted[ki][head.txn] = lk
				hasMore := false
				for qk := range s.queues {
					for _, rq := range s.queues[qk] {
						if rq.txn == head.txn && rq.mode == nC {
							hasMore = true
						}
					}
				}
				if !hasMore {
					var cKeys []int
					for k2, gl := range s.granted {
						if lk2, ok := gl[head.txn]; ok && lk2.mode == nC {
							cKeys = append(cKeys, k2)
						}
					}
					sort.Ints(cKeys)
					evs = append(evs, s.releaseCommit(head.txn, cKeys)...)
					goto nextRound
				}
			}
		}
	nextRound:
	}
	return evs
}

// detect builds waits-for edges over every queue entry and tests whether t
// can return to itself.
func (s *naiveSim) detect(t int) bool {
	holder := func(txn int) string { return fmt.Sprintf("H%d", txn) }
	entry := func(k, i int) string { return fmt.Sprintf("Q%d:%d", k, i) }

	adj := map[string][]string{}
	var starts []string
	for ki := range s.queues {
		for qi, rq := range s.queues[ki] {
			from := entry(ki, qi)
			if rq.txn == t {
				starts = append(starts, from)
			}
			for h, lk := range s.granted[ki] {
				if h != rq.txn && !nCompat(rq.mode, lk.mode) {
					adj[from] = append(adj[from], holder(h))
				}
			}
			for p := 0; p < qi; p++ {
				if s.queues[ki][p].txn != rq.txn {
					adj[from] = append(adj[from], entry(ki, p))
				}
			}
		}
	}
	// holder node edges: hop to each queued entry of that holder txn
	holderTxn := func(node string) (int, bool) {
		var id int
		if _, err := fmt.Sscanf(node, "H%d", &id); err != nil {
			return 0, false
		}
		return id, true
	}
	seen := map[string]bool{}
	var dfs func(n, start string) bool
	dfs = func(n, start string) bool {
		var tos []string
		if id, ok := holderTxn(n); ok {
			for ki := range s.queues {
				for qi, rq := range s.queues[ki] {
					if rq.txn == id {
						tos = append(tos, entry(ki, qi))
					}
				}
			}
		} else {
			tos = adj[n]
		}
		for _, to := range tos {
			if to == start {
				return true
			}
			if !seen[to] {
				seen[to] = true
				if dfs(to, start) {
					return true
				}
			}
		}
		return false
	}
	for _, st := range starts {
		seen = map[string]bool{st: true}
		if dfs(st, st) {
			return true
		}
	}
	return false
}
