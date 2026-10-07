package history

// 本文件是一个独立编写的朴素模型（线性扫描、无堆、无索引），
// 用于与内核在随机操作序列上逐步对照，验证内核行为与朴素语义完全一致。

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type mDoc struct {
	flags     docFlags
	status    docStatus
	enteredAt int64
	order     uint64
}

type model struct {
	entries   []Entry
	pos       int
	docs      map[string]*mDoc
	now       int64
	capacity  int
	ttl       int64
	docSeq    uint64
	entrySeq  uint64
	insertSeq uint64
}

func newModel(cfg Config) *model {
	return &model{pos: -1, docs: map[string]*mDoc{}, capacity: cfg.Capacity, ttl: cfg.TTL}
}

func (m *model) allocDoc() string {
	m.docSeq++
	id := fmt.Sprintf("D%d", m.docSeq)
	m.docs[id] = &mDoc{status: statusActive}
	return id
}

// leaveCurrent 朴素实现离开当前文档的资格判定。
func (m *model) leaveCurrent() {
	d := m.docs[m.entries[m.pos].DocID]
	if d == nil || d.status != statusActive {
		return
	}
	if m.capacity > 0 && d.flags.eligible() {
		d.status = statusCached
		d.enteredAt = m.now
		m.insertSeq++
		d.order = m.insertSeq
	} else {
		d.status = statusUnloaded
	}
}

// cachedIDs 线性扫描出全部缓存文档标识。
func (m *model) cachedIDs() []string {
	var out []string
	for id, d := range m.docs {
		if d.status == statusCached {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// earliestCached 线性扫描找出进入缓存最早的文档。
func (m *model) earliestCached() string {
	best := ""
	for id, d := range m.docs {
		if d.status != statusCached {
			continue
		}
		if best == "" {
			best = id
			continue
		}
		b := m.docs[best]
		if d.enteredAt < b.enteredAt || (d.enteredAt == b.enteredAt && d.order < b.order) {
			best = id
		}
	}
	return best
}

func (m *model) enforceCapacity() {
	for len(m.cachedIDs()) > m.capacity {
		m.docs[m.earliestCached()].status = statusUnloaded
	}
}

func (m *model) evictExpired() {
	if m.ttl <= 0 {
		return
	}
	for {
		id := m.earliestCached()
		if id == "" || m.now-m.docs[id].enteredAt < m.ttl {
			return
		}
		m.docs[id].status = statusUnloaded
	}
}

// referenced 线性扫描条目列表，判断文档是否仍被引用。
func (m *model) referenced(docID string) bool {
	for _, e := range m.entries {
		if e.DocID == docID {
			return true
		}
	}
	return false
}

func (m *model) navigate(url string, state any, sameDoc bool) (Entry, error) {
	if url == "" {
		return Entry{}, errf(KindInvalidArgument, "empty url")
	}
	// 截断：失去全部引用的缓存文档立即驱逐。
	removed := m.entries[m.pos+1:]
	m.entries = m.entries[:m.pos+1]
	for _, e := range removed {
		if !m.referenced(e.DocID) {
			if d := m.docs[e.DocID]; d != nil && d.status == statusCached {
				d.status = statusUnloaded
			}
		}
	}
	m.entrySeq++
	e := Entry{URL: url, State: state, Seq: m.entrySeq}
	if sameDoc && m.pos >= 0 {
		e.DocID = m.entries[m.pos].DocID
	} else {
		if m.pos >= 0 {
			m.leaveCurrent()
		}
		e.DocID = m.allocDoc()
	}
	m.entries = append(m.entries, e)
	m.pos++
	m.enforceCapacity()
	return e, nil
}

func (m *model) replace(url string, state any) (Entry, error) {
	if url == "" {
		return Entry{}, errf(KindInvalidArgument, "empty url")
	}
	if m.pos < 0 {
		return Entry{}, errf(KindInvalidState, "no current entry to replace")
	}
	m.entries[m.pos].URL = url
	m.entries[m.pos].State = state
	return m.entries[m.pos], nil
}

// reload 线性扫描并更新同文档的所有条目。
func (m *model) reload(oldID string) {
	newID := m.allocDoc()
	for i := range m.entries {
		if m.entries[i].DocID == oldID {
			m.entries[i].DocID = newID
		}
	}
	if old := m.docs[oldID]; old != nil && old.status == statusActive {
		old.status = statusUnloaded
	}
}

func (m *model) traverse(delta int) TraverseResult {
	target := m.pos + delta
	if target < 0 || target >= len(m.entries) {
		return TraverseResult{Err: errf(KindInvalidArgument, "delta %d out of range", delta)}
	}
	if delta == 0 {
		m.reload(m.entries[m.pos].DocID)
		return TraverseResult{Action: ActionReloaded, Entry: m.entries[m.pos]}
	}
	curID := m.entries[m.pos].DocID
	tgtID := m.entries[target].DocID
	if tgtID == curID {
		m.pos = target
		return TraverseResult{Action: ActionSameDocument, Entry: m.entries[m.pos]}
	}
	tgt := m.docs[tgtID]
	restored := tgt != nil && tgt.status == statusCached
	if restored {
		tgt.status = statusActive
	}
	m.leaveCurrent()
	if !restored {
		m.reload(tgtID)
	}
	m.pos = target
	m.enforceCapacity()
	if restored {
		return TraverseResult{Action: ActionRestored, Entry: m.entries[m.pos]}
	}
	return TraverseResult{Action: ActionReloaded, Entry: m.entries[m.pos]}
}

func (m *model) advance(delta int64) error {
	if delta < 0 {
		return errf(KindClockRollback, "negative clock delta %d", delta)
	}
	m.now += delta
	m.evictExpired()
	return nil
}

// setFlag 复刻内核的拒绝次序：参数非法 -> 时钟回退 -> 文档不存在 -> 状态不允许。
func (m *model) setFlag(at int64, docID string, which int, v bool) error {
	if docID == "" {
		return errf(KindInvalidArgument, "empty document id")
	}
	if at < m.now {
		return errf(KindClockRollback, "op time %d before model time %d", at, m.now)
	}
	if at > m.now {
		m.now = at
		m.evictExpired()
	}
	d := m.docs[docID]
	if d == nil {
		return errf(KindDocumentNotFound, "document %q", docID)
	}
	if d.status == statusUnloaded {
		return errf(KindInvalidState, "document %q is unloaded", docID)
	}
	switch which {
	case 0:
		d.flags.networkPending = v
	case 1:
		d.flags.unloadBlocker = v
	case 2:
		d.flags.exclusiveResource = v
	case 3:
		d.flags.markedUncacheable = v
	}
	if d.status == statusCached && !d.flags.eligible() {
		d.status = statusUnloaded
	}
	return nil
}

// ---- 随机操作序列对照 ----

func kernelSetFlag(k *Kernel, at int64, docID string, which int, v bool) error {
	switch which {
	case 0:
		return k.SetNetworkPending(at, docID, v)
	case 1:
		return k.SetUnloadBlocker(at, docID, v)
	case 2:
		return k.SetExclusiveResource(at, docID, v)
	default:
		return k.SetUncacheable(at, docID, v)
	}
}

func sameErrKind(a, b error) bool {
	ka, oka := KindOf(a)
	kb, okb := KindOf(b)
	if a == nil && b == nil {
		return true
	}
	if (a == nil) != (b == nil) {
		return false
	}
	return oka && okb && ka == kb
}

func compareState(t *testing.T, k *Kernel, m *model, step int, knownDocs map[string]bool) {
	t.Helper()
	ke, me := k.Entries(), m.entries
	if len(ke) != len(me) {
		t.Fatalf("step %d: entry count diverge kernel=%d model=%d", step, len(ke), len(me))
	}
	for i := range ke {
		if ke[i] != me[i] {
			t.Fatalf("step %d: entry %d diverge kernel=%+v model=%+v", step, i, ke[i], me[i])
		}
	}
	if k.Position() != m.pos {
		t.Fatalf("step %d: position diverge kernel=%d model=%d", step, k.Position(), m.pos)
	}
	if k.Now() != m.now {
		t.Fatalf("step %d: clock diverge kernel=%d model=%d", step, k.Now(), m.now)
	}
	kc, mc := k.CachedDocIDs(), m.cachedIDs()
	if len(kc) != len(mc) {
		t.Fatalf("step %d: cached count diverge kernel=%v model=%v", step, kc, mc)
	}
	for i := range kc {
		if kc[i] != mc[i] {
			t.Fatalf("step %d: cached docs diverge kernel=%v model=%v", step, kc, mc)
		}
	}
	for _, e := range ke {
		knownDocs[e.DocID] = true
	}
	for id := range knownDocs {
		ks, kok := k.DocStatus(id)
		md, mok := m.docs[id]
		if kok != mok {
			t.Fatalf("step %d: doc %s existence diverge kernel=%v model=%v", step, id, kok, mok)
		}
		if kok && ks != md.status {
			t.Fatalf("step %d: doc %s status diverge kernel=%s model=%s", step, id, ks, md.status)
		}
	}
	if err := k.CheckInvariants(); err != nil {
		t.Fatalf("step %d: invariant violated: %v", step, err)
	}
}

// 与朴素模型对照随机操作序列；每步打印输入、输出与判定依据。
func TestRandomAgainstModel(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			k := mustKernel(t, Config{Capacity: 3, TTL: 20})
			m := newModel(Config{Capacity: 3, TTL: 20})
			knownDocs := map[string]bool{}

			for step := 0; step < 300; step++ {
				op := rng.Intn(100)
				switch {
				case op < 30: // 推入导航
					url := fmt.Sprintf("u%d", rng.Intn(6))
					state := rng.Intn(1000)
					sameDoc := rng.Intn(3) == 0
					ke, kerr := k.Navigate(url, state, sameDoc)
					me, merr := m.navigate(url, state, sameDoc)
					t.Logf("step=%d op=navigate url=%q state=%d sameDoc=%v -> kernel(doc=%s,err=%v) model(doc=%s,err=%v)",
						step, url, state, sameDoc, ke.DocID, kerr, me.DocID, merr)
					if !sameErrKind(kerr, merr) || (kerr == nil && ke != me) {
						t.Fatalf("step %d: navigate diverge kernel=(%+v,%v) model=(%+v,%v)", step, ke, kerr, me, merr)
					}
				case op < 40: // 替换导航
					url := fmt.Sprintf("r%d", rng.Intn(6))
					state := rng.Intn(1000)
					ke, kerr := k.Replace(url, state)
					me, merr := m.replace(url, state)
					t.Logf("step=%d op=replace url=%q state=%d -> kernel(err=%v) model(err=%v)", step, url, state, kerr, merr)
					if !sameErrKind(kerr, merr) || (kerr == nil && ke != me) {
						t.Fatalf("step %d: replace diverge kernel=(%+v,%v) model=(%+v,%v)", step, ke, kerr, me, merr)
					}
				case op < 65: // 单次遍历
					delta := rng.Intn(9) - 4
					kr := k.TraverseSync(delta)
					mr := m.traverse(delta)
					t.Logf("step=%d op=traverse delta=%d -> kernel(action=%s,doc=%s,err=%v) model(action=%s,doc=%s,err=%v)",
						step, delta, kr.Action, kr.Entry.DocID, kr.Err, mr.Action, mr.Entry.DocID, mr.Err)
					if kr.Action != mr.Action || !sameErrKind(kr.Err, mr.Err) ||
						(kr.Err == nil && kr.Entry != mr.Entry) {
						t.Fatalf("step %d: traverse diverge kernel=%+v model=%+v", step, kr, mr)
					}
				case op < 75: // 连续遍历合并：只有最后一次被执行
					n := 1 + rng.Intn(3)
					deltas := make([]int, n)
					futs := make([]*Future, n)
					for i := range deltas {
						deltas[i] = rng.Intn(9) - 4
						futs[i] = k.Traverse(deltas[i])
					}
					k.RunPending()
					for i, f := range futs {
						kr := f.Result()
						var mr TraverseResult
						if i < n-1 {
							mr = TraverseResult{Err: errf(KindSuperseded, "superseded")}
						} else {
							mr = m.traverse(deltas[i])
						}
						t.Logf("step=%d op=traverse-batch[%d/%d] delta=%d -> kernel(action=%s,err=%v) expect(action=%s,err=%v)",
							step, i, n, deltas[i], kr.Action, kr.Err, mr.Action, mr.Err)
						if kr.Action != mr.Action || !sameErrKind(kr.Err, mr.Err) ||
							(kr.Err == nil && kr.Entry != mr.Entry) {
							t.Fatalf("step %d: batch traverse diverge kernel=%+v model=%+v", step, kr, mr)
						}
					}
				case op < 85: // 时钟推进
					delta := int64(rng.Intn(25))
					kerr := k.AdvanceClock(delta)
					merr := m.advance(delta)
					t.Logf("step=%d op=advance-clock delta=%d -> kernel(err=%v) model(err=%v)", step, delta, kerr, merr)
					if !sameErrKind(kerr, merr) {
						t.Fatalf("step %d: advance diverge kernel=%v model=%v", step, kerr, merr)
					}
				default: // 文档状态变化（含未知文档与过期时刻）
					which := rng.Intn(4)
					v := rng.Intn(2) == 0
					docID := fmt.Sprintf("D%d", 1+rng.Intn(int(2+k.docSeq*2)))
					at := k.Now() + int64(rng.Intn(8)) - 2
					kerr := kernelSetFlag(k, at, docID, which, v)
					merr := m.setFlag(at, docID, which, v)
					t.Logf("step=%d op=set-flag which=%d doc=%s at=%d v=%v -> kernel(err=%v) model(err=%v)",
						step, which, docID, at, v, kerr, merr)
					if !sameErrKind(kerr, merr) {
						t.Fatalf("step %d: set-flag diverge kernel=%v model=%v", step, kerr, merr)
					}
				}
				compareState(t, k, m, step, knownDocs)
			}
		})
	}
}
