package history_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"ontology/history"
)

// This file holds an independently written naive model of the kernel:
// linear scans everywhere, no indexes, no heap. Random operation
// sequences are replayed against both implementations and every
// observable result must match exactly.

type mDoc struct {
	flags    [4]bool
	status   history.DocStatus
	cachedAt time.Time
	order    uint64
}

type mReq struct {
	token uint64
	delta int
	at    time.Time
}

type model struct {
	entries []history.Entry
	pos     int
	docs    map[uint64]*mDoc
	now     time.Time
	pending []mReq

	capacity int
	ttl      time.Duration

	nextDoc uint64
	nextSeq uint64
	nextTok uint64
	nextOrd uint64
}

func newModel(capacity int, ttl time.Duration) *model {
	return &model{
		pos: -1, docs: make(map[uint64]*mDoc), now: t0,
		capacity: capacity, ttl: ttl,
		nextDoc: 1, nextSeq: 1, nextTok: 1,
	}
}

func mEligible(d *mDoc) bool {
	for _, f := range d.flags {
		if f {
			return false
		}
	}
	return true
}

func (m *model) referenced(id uint64) bool {
	for _, e := range m.entries {
		if e.DocID == id {
			return true
		}
	}
	return false
}

func (m *model) cachedIDs() []uint64 {
	var ids []uint64
	for id, d := range m.docs {
		if d.status == history.DocCached {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (m *model) expire() {
	for _, d := range m.docs {
		if d.status == history.DocCached && m.now.Sub(d.cachedAt) >= m.ttl {
			d.status = history.DocUnloaded
		}
	}
}

func (m *model) evictOverflow() {
	for len(m.cachedIDs()) > m.capacity {
		var oldest uint64
		first := true
		for id, d := range m.docs {
			if d.status != history.DocCached {
				continue
			}
			if first {
				oldest, first = id, false
				continue
			}
			o := m.docs[oldest]
			if d.cachedAt.Before(o.cachedAt) ||
				(d.cachedAt.Equal(o.cachedAt) && d.order < o.order) {
				oldest = id
			}
		}
		m.docs[oldest].status = history.DocUnloaded
	}
}

func (m *model) leave(id uint64) {
	d := m.docs[id]
	if !mEligible(d) {
		d.status = history.DocUnloaded
		return
	}
	d.status = history.DocCached
	d.cachedAt = m.now
	d.order = m.nextOrd
	m.nextOrd++
	m.expire()
	m.evictOverflow()
}

func (m *model) reload(old uint64) uint64 {
	if d, ok := m.docs[old]; ok {
		d.status = history.DocUnloaded
		delete(m.docs, old)
	}
	id := m.nextDoc
	m.nextDoc++
	for i := range m.entries {
		if m.entries[i].DocID == old {
			m.entries[i].DocID = id
		}
	}
	m.docs[id] = &mDoc{status: history.DocActive}
	return id
}

func (m *model) truncate() {
	removed := m.entries[m.pos+1:]
	m.entries = m.entries[:m.pos+1]
	seen := map[uint64]bool{}
	for _, e := range removed {
		if seen[e.DocID] {
			continue
		}
		seen[e.DocID] = true
		if !m.referenced(e.DocID) {
			if d, ok := m.docs[e.DocID]; ok && d.status == history.DocCached {
				d.status = history.DocUnloaded
			}
		}
	}
}

func (m *model) navigate(url string, state any, sameDoc bool, at time.Time) (history.Entry, error) {
	if url == "" {
		return history.Entry{}, history.ErrInvalidArgument
	}
	m.drain()
	if at.Before(m.now) {
		return history.Entry{}, history.ErrClockRegression
	}
	m.now = at
	if sameDoc && m.pos >= 0 {
		doc := m.entries[m.pos].DocID
		m.truncate()
		e := history.Entry{URL: url, DocID: doc, State: state, Seq: m.nextSeq}
		m.nextSeq++
		m.entries = append(m.entries, e)
		m.pos++
		return e, nil
	}
	var curDoc uint64
	hadCur := m.pos >= 0
	if hadCur {
		curDoc = m.entries[m.pos].DocID
	}
	newID := m.nextDoc
	m.nextDoc++
	m.truncate()
	e := history.Entry{URL: url, DocID: newID, State: state, Seq: m.nextSeq}
	m.nextSeq++
	m.entries = append(m.entries, e)
	m.pos++
	if hadCur {
		m.leave(curDoc)
	}
	m.docs[newID] = &mDoc{status: history.DocActive}
	return e, nil
}

func (m *model) replace(url string, state any, at time.Time) error {
	if url == "" {
		return history.ErrInvalidArgument
	}
	m.drain()
	if at.Before(m.now) {
		return history.ErrClockRegression
	}
	if m.pos < 0 {
		return history.ErrInvalidState
	}
	m.now = at
	m.entries[m.pos].URL = url
	m.entries[m.pos].State = state
	return nil
}

func (m *model) traverse(delta int, at time.Time) uint64 {
	tok := m.nextTok
	m.nextTok++
	m.pending = append(m.pending, mReq{token: tok, delta: delta, at: at})
	return tok
}

func (m *model) drain() []history.Outcome {
	if len(m.pending) == 0 {
		return nil
	}
	pending := m.pending
	m.pending = nil
	var outcomes []history.Outcome
	for _, req := range pending[:len(pending)-1] {
		outcomes = append(outcomes, history.Outcome{Token: req.token, Delta: req.delta, Pos: -1, Err: history.ErrSuperseded})
	}
	req := pending[len(pending)-1]
	outcomes = append(outcomes, m.apply(req))
	return outcomes
}

func (m *model) apply(req mReq) history.Outcome {
	fail := func(err error) history.Outcome {
		return history.Outcome{Token: req.token, Delta: req.delta, Pos: -1, Err: err}
	}
	t := m.pos + req.delta
	if t < 0 || t >= len(m.entries) {
		return fail(history.ErrInvalidArgument)
	}
	if req.at.Before(m.now) {
		return fail(history.ErrClockRegression)
	}
	m.now = req.at
	cur := m.entries[m.pos]
	tgt := m.entries[t]
	switch {
	case req.delta == 0:
		m.reload(cur.DocID)
	case tgt.DocID == cur.DocID:
		m.pos = t
	default:
		m.leave(cur.DocID)
		if d, ok := m.docs[tgt.DocID]; ok && d.status == history.DocCached {
			d.status = history.DocActive
			m.pos = t
		} else {
			m.reload(tgt.DocID)
			m.pos = t
		}
	}
	return history.Outcome{Token: req.token, Delta: req.delta, Pos: m.pos}
}

func (m *model) setFlag(doc uint64, flag history.Flag, val bool, at time.Time) error {
	if flag < 0 || flag > history.FlagUncacheable {
		return history.ErrInvalidArgument
	}
	m.drain()
	if at.Before(m.now) {
		return history.ErrClockRegression
	}
	d, ok := m.docs[doc]
	if !ok {
		return history.ErrDocumentNotFound
	}
	if d.status == history.DocUnloaded {
		return history.ErrInvalidState
	}
	m.now = at
	d.flags[flag] = val
	if d.status == history.DocCached && !mEligible(d) {
		d.status = history.DocUnloaded
	}
	return nil
}

func (m *model) advance(d time.Duration) error {
	if d < 0 {
		return history.ErrClockRegression
	}
	m.drain()
	m.now = m.now.Add(d)
	m.expire()
	return nil
}

// --- comparison harness ---

var errCats = []error{
	history.ErrInvalidArgument,
	history.ErrClockRegression,
	history.ErrSuperseded,
	history.ErrDocumentNotFound,
	history.ErrInvalidState,
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	for _, c := range errCats {
		if errors.Is(a, c) != errors.Is(b, c) {
			return false
		}
	}
	return true
}

type view struct {
	now     time.Time
	pos     int
	entries []history.Entry
	cached  []uint64
	docs    map[uint64]history.DocInfo
}

func kernelView(k *history.Kernel) view {
	s := k.Snapshot()
	return view{now: s.Now, pos: s.Pos, entries: s.Entries, cached: s.Cached, docs: s.Docs}
}

func modelView(m *model) view {
	v := view{
		now: m.now, pos: m.pos,
		entries: append([]history.Entry(nil), m.entries...),
		cached:  m.cachedIDs(),
		docs:    make(map[uint64]history.DocInfo, len(m.docs)),
	}
	for id, d := range m.docs {
		v.docs[id] = history.DocInfo{Status: d.status, Flags: d.flags, CachedAt: d.cachedAt}
	}
	return v
}

func TestRandomAgainstNaiveModel(t *testing.T) {
	ttls := []time.Duration{time.Second, 5 * time.Second, 10 * time.Second, time.Hour}
	for seed := int64(0); seed < 30; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			capacity := r.Intn(4)
			ttl := ttls[r.Intn(len(ttls))]
			var logBuf strings.Builder
			k, err := history.NewKernel(
				history.Config{CacheCapacity: capacity, CacheTTL: ttl, Now: t0},
				history.WithLogger(func(format string, args ...any) {
					fmt.Fprintf(&logBuf, format+"\n", args...)
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			m := newModel(capacity, ttl)

			clock := 0
			fail := func(step int, format string, args ...any) {
				t.Logf("kernel decision log:\n%s", logBuf.String())
				t.Fatalf("step %d: %s", step, fmt.Sprintf(format, args...))
			}
			nextAt := func() time.Time {
				if r.Intn(2) == 0 {
					clock += r.Intn(3)
				}
				at := t0.Add(time.Duration(clock) * time.Second)
				if r.Intn(20) == 0 { // deliberate clock regression attempt
					at = at.Add(-time.Duration(r.Intn(60)+1) * time.Second)
				}
				return at
			}

			for step := 0; step < 400; step++ {
				at := nextAt()
				switch op := r.Intn(100); {
				case op < 30: // navigate
					url := fmt.Sprintf("u%d", r.Intn(6))
					if r.Intn(15) == 0 {
						url = ""
					}
					state, sameDoc := r.Intn(100), r.Intn(2) == 0
					ke, kerr := k.Navigate(url, state, sameDoc, at)
					me, merr := m.navigate(url, state, sameDoc, at)
					if !sameErr(kerr, merr) {
						fail(step, "navigate err kernel=%v model=%v", kerr, merr)
					}
					if kerr == nil && !reflect.DeepEqual(ke, me) {
						fail(step, "navigate entry kernel=%+v model=%+v", ke, me)
					}
				case op < 45: // replace
					url := fmt.Sprintf("r%d", r.Intn(6))
					if r.Intn(20) == 0 {
						url = ""
					}
					state := r.Intn(100)
					kerr := k.Replace(url, state, at)
					merr := m.replace(url, state, at)
					if !sameErr(kerr, merr) {
						fail(step, "replace err kernel=%v model=%v", kerr, merr)
					}
				case op < 70: // 1-3 traversals then drain
					n := 1 + r.Intn(3)
					for j := 0; j < n; j++ {
						delta := r.Intn(9) - 4
						kt := k.Traverse(delta, at)
						mt := m.traverse(delta, at)
						if kt != mt {
							fail(step, "traverse token kernel=%d model=%d", kt, mt)
						}
					}
					ko, mo := k.Drain(), m.drain()
					if len(ko) != len(mo) {
						fail(step, "drain outcomes kernel=%d model=%d", len(ko), len(mo))
					}
					for i := range ko {
						if ko[i].Token != mo[i].Token || ko[i].Pos != mo[i].Pos || !sameErr(ko[i].Err, mo[i].Err) {
							fail(step, "outcome[%d] kernel=%+v model=%+v", i, ko[i], mo[i])
						}
					}
				case op < 85: // eligibility flag
					doc := uint64(r.Intn(10) + 1)
					flag := history.Flag(r.Intn(5)) // 4 is invalid on purpose
					val := r.Intn(2) == 0
					kerr := k.SetEligibility(doc, flag, val, at)
					merr := m.setFlag(doc, flag, val, at)
					if !sameErr(kerr, merr) {
						fail(step, "setFlag err kernel=%v model=%v", kerr, merr)
					}
				default: // advance clock
					d := time.Duration(r.Intn(20)) * time.Second
					if r.Intn(15) == 0 {
						d = -d
					}
					kerr := k.AdvanceClock(d)
					merr := m.advance(d)
					if !sameErr(kerr, merr) {
						fail(step, "advance err kernel=%v model=%v", kerr, merr)
					}
				}

				kv, mv := kernelView(k), modelView(m)
				if !reflect.DeepEqual(kv, mv) {
					fail(step, "state diverged:\nkernel=%+v\nmodel=%+v", kv, mv)
				}
				if err := k.CheckInvariants(); err != nil {
					fail(step, "invariants: %v", err)
				}
			}
		})
	}
}
