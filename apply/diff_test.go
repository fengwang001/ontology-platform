package apply_test

// 独立朴素模拟：按规格逐条用线性扫描与全量排序实现，与 apply.Merger 对拍。

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/apply"
)

type simRow struct {
	key      apply.Key
	a, b     int64
	aseq     int64
	arrival  int64
	wait     apply.Key
	detached bool
}

type sim struct {
	timeout int64
	limit   int
	allow   map[int]bool
	next    [3]int64
	tid     map[apply.Key]int64
	alive   map[apply.Key]bool
	ta      map[apply.Key]int64
	tb      map[apply.Key]int64
	pend    map[apply.Key]*simRow
	dead    []apply.Key
	maxNow  int64
	aseq    int64
}

func newSim(timeout int64, limit int, allow map[int]bool) *sim {
	return &sim{
		timeout: timeout,
		limit:   limit,
		allow:   allow,
		next:    [3]int64{0, 1, 1},
		tid:     map[apply.Key]int64{},
		alive:   map[apply.Key]bool{},
		ta:      map[apply.Key]int64{},
		tb:      map[apply.Key]int64{},
		pend:    map[apply.Key]*simRow{},
	}
}

func simValidShard(s int) bool   { return s >= 1 && s <= 16 }
func simValidKind(k int) bool    { return k == 1 || k == 2 }
func simValidID(id int64) bool   { return id >= 1 && id <= 1_000_000_000 }
func simValidRef(r int64) bool   { return r >= 0 && r <= 1_000_000_000 }
func simValidNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

// simRefs 按 a、b 顺序返回非零引用的目标键（a 指部门表，b 指员工表）。
func simRefs(shard, kind int, a, b int64) []apply.Key {
	if kind == 1 {
		if a == 0 {
			return nil
		}
		return []apply.Key{{Shard: shard, Kind: 1, ID: a}}
	}
	refs := []apply.Key{{Shard: shard, Kind: 1, ID: a}}
	if b != 0 {
		refs = append(refs, apply.Key{Shard: shard, Kind: 2, ID: b})
	}
	return refs
}

// judge 返回第一个不就绪的引用键；自引用视为就绪。
func (s *sim) judge(key apply.Key, a, b int64) (apply.Key, bool) {
	for _, ref := range simRefs(key.Shard, key.Kind, a, b) {
		if ref == key {
			continue
		}
		if _, ok := s.tid[ref]; !ok {
			return ref, false
		}
		if !s.alive[ref] {
			return ref, false
		}
	}
	return apply.Key{}, true
}

func (s *sim) land(key apply.Key, a, b int64) int64 {
	tid, ok := s.tid[key]
	if !ok {
		tid = s.next[key.Kind]
		s.next[key.Kind]++
		s.tid[key] = tid
	}
	s.alive[key] = true
	s.ta[key], s.tb[key] = 0, 0
	for _, ref := range simRefs(key.Shard, key.Kind, a, b) {
		rtid := tid
		if ref != key {
			rtid = s.tid[ref]
		}
		if ref.Kind == 1 {
			s.ta[key] = rtid
		} else {
			s.tb[key] = rtid
		}
	}
	return tid
}

func (s *sim) release(trigger apply.Key) {
	collect := func(w apply.Key) []apply.Key {
		var rows []*simRow
		for _, r := range s.pend {
			if !r.detached && r.wait == w {
				rows = append(rows, r)
				r.detached = true
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].aseq < rows[j].aseq })
		keys := make([]apply.Key, 0, len(rows))
		for _, r := range rows {
			keys = append(keys, r.key)
		}
		return keys
	}
	queue := collect(trigger)
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		r := s.pend[k]
		if r == nil {
			continue
		}
		if wait, ready := s.judge(k, r.a, r.b); ready {
			s.land(k, r.a, r.b)
			delete(s.pend, k)
			queue = append(queue, collect(k)...)
		} else {
			r.wait = wait
			r.detached = false
		}
	}
}

func (s *sim) expire(now int64) int {
	var rows []*simRow
	for _, r := range s.pend {
		if now-r.arrival >= s.timeout {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].aseq < rows[j].aseq })
	for _, r := range rows {
		s.dead = append(s.dead, r.key)
		delete(s.pend, r.key)
	}
	return len(rows)
}

func (s *sim) upsert(shard, kind int, id, a, b, now int64) (apply.Result, error) {
	if !simValidShard(shard) || !simValidKind(kind) || !simValidID(id) ||
		!simValidRef(a) || !simValidRef(b) || !simValidNow(now) {
		return apply.Result{}, apply.ErrInvalid
	}
	if kind == 1 && b != 0 {
		return apply.Result{}, apply.ErrInvalid
	}
	if kind == 2 && a == 0 {
		return apply.Result{}, apply.ErrInvalid
	}
	if !s.allow[shard] {
		return apply.Result{}, apply.ErrDenied
	}
	if now < s.maxNow {
		return apply.Result{}, apply.ErrClock
	}
	s.expire(now)
	key := apply.Key{Shard: shard, Kind: kind, ID: id}
	wait, ready := s.judge(key, a, b)
	if ready {
		s.maxNow = now
		delete(s.pend, key)
		tid := s.land(key, a, b)
		s.release(key)
		return apply.Result{Outcome: apply.Applied, Tid: tid}, nil
	}
	if r, ok := s.pend[key]; ok {
		s.maxNow = now
		r.a, r.b, r.wait, r.detached = a, b, wait, false
		return apply.Result{Outcome: apply.Pending}, nil
	}
	if len(s.pend) >= s.limit {
		return apply.Result{}, apply.ErrFull
	}
	s.maxNow = now
	s.aseq++
	s.pend[key] = &simRow{key: key, a: a, b: b, aseq: s.aseq, arrival: now, wait: wait}
	return apply.Result{Outcome: apply.Pending}, nil
}

func (s *sim) delete(shard, kind int, id, now int64) (apply.Result, error) {
	if !simValidShard(shard) || !simValidKind(kind) || !simValidID(id) || !simValidNow(now) {
		return apply.Result{}, apply.ErrInvalid
	}
	if !s.allow[shard] {
		return apply.Result{}, apply.ErrDenied
	}
	if now < s.maxNow {
		return apply.Result{}, apply.ErrClock
	}
	key := apply.Key{Shard: shard, Kind: kind, ID: id}
	if _, ok := s.pend[key]; ok {
		s.maxNow = now
		s.expire(now)
		delete(s.pend, key)
		return apply.Result{Outcome: apply.DroppedPending}, nil
	}
	if s.alive[key] {
		s.maxNow = now
		s.expire(now)
		s.alive[key] = false
		return apply.Result{Outcome: apply.Deleted}, nil
	}
	return apply.Result{}, apply.ErrUnknown
}

func (s *sim) tick(now int64) (int, error) {
	if !simValidNow(now) {
		return 0, apply.ErrInvalid
	}
	if now < s.maxNow {
		return 0, apply.ErrClock
	}
	s.maxNow = now
	return s.expire(now), nil
}

// ---- 随机事件序列对拍 ----

type event struct {
	op           int // 0=Upsert 1=Delete 2=Tick
	s, kind      int
	id, a, b, nw int64
}

func (e event) String() string {
	switch e.op {
	case 0:
		return fmt.Sprintf("Upsert(s=%d,kind=%d,id=%d,a=%d,b=%d,now=%d)", e.s, e.kind, e.id, e.a, e.b, e.nw)
	case 1:
		return fmt.Sprintf("Delete(s=%d,kind=%d,id=%d,now=%d)", e.s, e.kind, e.id, e.nw)
	default:
		return fmt.Sprintf("Tick(now=%d)", e.nw)
	}
}

func genEvents(rng *rand.Rand, n int) []event {
	var evs []event
	var now int64
	for i := 0; i < n; i++ {
		now += int64(rng.Intn(4))
		if rng.Intn(20) == 0 && now > 0 {
			now-- // 制造时钟回退
		}
		switch x := rng.Intn(100); {
		case x < 55: // 合法 Upsert
			shard := 1 + rng.Intn(3) // 3 可能不在 allow
			kind := 1 + rng.Intn(2)
			id := int64(1 + rng.Intn(6))
			var a, b int64
			if kind == 1 {
				a = int64(rng.Intn(7))
			} else {
				a = int64(1 + rng.Intn(6))
				b = int64(rng.Intn(7))
			}
			evs = append(evs, event{op: 0, s: shard, kind: kind, id: id, a: a, b: b, nw: now})
		case x < 70: // Delete
			evs = append(evs, event{op: 1, s: 1 + rng.Intn(3), kind: 1 + rng.Intn(2), id: int64(1 + rng.Intn(6)), nw: now})
		case x < 85: // Tick
			evs = append(evs, event{op: 2, nw: now})
		default: // 非法参数 Upsert
			shard, kind := 1+rng.Intn(3), 1+rng.Intn(2)
			id, a, b := int64(1+rng.Intn(6)), int64(rng.Intn(7)), int64(rng.Intn(7))
			switch rng.Intn(6) {
			case 0:
				id = 0
			case 1:
				kind = 3
			case 2:
				kind, b = 1, 1 // dept 的 b 非 0
			case 3:
				kind, a = 2, 0 // emp 的 a 为 0
			case 4:
				shard = 0
			case 5:
				id = 1_000_000_001
			}
			evs = append(evs, event{op: 0, s: shard, kind: kind, id: id, a: a, b: b, nw: now})
		}
	}
	return evs
}

func runEvent(m *apply.Merger, e event) (apply.Result, int, error) {
	switch e.op {
	case 0:
		r, err := m.Upsert(e.s, e.kind, e.id, e.a, e.b, e.nw)
		return r, 0, err
	case 1:
		r, err := m.Delete(e.s, e.kind, e.id, e.nw)
		return r, 0, err
	default:
		n, err := m.Tick(e.nw)
		return apply.Result{}, n, err
	}
}

func runEventSim(sm *sim, e event) (apply.Result, int, error) {
	switch e.op {
	case 0:
		r, err := sm.upsert(e.s, e.kind, e.id, e.a, e.b, e.nw)
		return r, 0, err
	case 1:
		r, err := sm.delete(e.s, e.kind, e.id, e.nw)
		return r, 0, err
	default:
		n, err := sm.tick(e.nw)
		return apply.Result{}, n, err
	}
}

func resultString(r apply.Result, n int, err error, op int) string {
	if err != nil {
		return "ERR " + err.Error()
	}
	if op == 2 {
		return fmt.Sprintf("Tick dead+=%d", n)
	}
	if r.Outcome == apply.Applied {
		return fmt.Sprintf("Applied tid=%d", r.Tid)
	}
	return r.Outcome.String()
}

// compareState 全量比较真实实现与模拟的可见状态，并校验不变量。
func compareState(m *apply.Merger, sm *sim) string {
	// 死信次序
	d1 := m.Dead()
	if len(d1) != len(sm.dead) {
		return fmt.Sprintf("dead len %d vs sim %d", len(d1), len(sm.dead))
	}
	for i := range d1 {
		if d1[i] != sm.dead[i] {
			return fmt.Sprintf("dead[%d]=%v vs sim %v", i, d1[i], sm.dead[i])
		}
	}
	// 目标行与挂起行
	tids := [3]map[int64]bool{1: {}, 2: {}}
	for shard := 1; shard <= 3; shard++ {
		for kind := 1; kind <= 2; kind++ {
			for id := int64(1); id <= 6; id++ {
				key := apply.Key{Shard: shard, Kind: kind, ID: id}
				tid, a, b, alive, ok := m.Target(shard, kind, id)
				stid, sok := sm.tid[key]
				if ok != sok {
					return fmt.Sprintf("target %v mapped=%v vs sim %v", key, ok, sok)
				}
				if ok {
					if tid != stid || alive != sm.alive[key] || a != sm.ta[key] || b != sm.tb[key] {
						return fmt.Sprintf("target %v = (tid=%d a=%d b=%d alive=%v) vs sim (tid=%d a=%d b=%d alive=%v)",
							key, tid, a, b, alive, stid, sm.ta[key], sm.tb[key], sm.alive[key])
					}
					if tids[kind][tid] {
						return fmt.Sprintf("kind=%d tid=%d 映射不单射", kind, tid)
					}
					tids[kind][tid] = true
				}
				pr, pok := m.PendingRow(shard, kind, id)
				sr, sok2 := sm.pend[key]
				if pok != sok2 {
					return fmt.Sprintf("pending %v exists=%v vs sim %v", key, pok, sok2)
				}
				if pok {
					if pr.A != sr.a || pr.B != sr.b || pr.Aseq != sr.aseq || pr.Arrival != sr.arrival || pr.Wait != sr.wait {
						return fmt.Sprintf("pending %v = %+v vs sim %+v", key, pr, sr)
					}
				}
			}
		}
	}
	// 不变量：每表 tid 连续无空洞（恰为 1..N）。
	for kind := 1; kind <= 2; kind++ {
		for tid := int64(1); tid <= int64(len(tids[kind])); tid++ {
			if !tids[kind][tid] {
				return fmt.Sprintf("kind=%d tid=%d 空洞", kind, tid)
			}
		}
	}
	// 不变量：存活行的每个非零引用都指向已映射的 tid
	// （「落库时指向存活行」由就绪判定保证；Delete 之后引用可指向不存活行）。
	for shard := 1; shard <= 3; shard++ {
		for kind := 1; kind <= 2; kind++ {
			for id := int64(1); id <= 6; id++ {
				_, a, b, alive, ok := m.Target(shard, kind, id)
				if !ok || !alive {
					continue
				}
				if a != 0 && !tids[1][a] {
					return fmt.Sprintf("(%d,%d,%d) 的 a=%d 未指向已映射 dept", shard, kind, id, a)
				}
				if b != 0 && !tids[2][b] {
					return fmt.Sprintf("(%d,%d,%d) 的 b=%d 未指向已映射 emp", shard, kind, id, b)
				}
			}
		}
	}
	return ""
}

// 与朴素模拟对拍 2000 组随机事件序列：逐事件比较输出，逐事件全量比较状态。
func TestRandomDiffAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		timeout := int64(1 + rng.Intn(8))
		limit := 1 + rng.Intn(4)
		var allowList []int
		allowSet := map[int]bool{}
		switch rng.Intn(4) {
		case 0:
			allowList = []int{1}
		case 1:
			allowList = []int{2}
		case 2:
			allowList = []int{1, 2}
		default:
			allowList = nil // 空集合：全部 ErrDenied
		}
		for _, s := range allowList {
			allowSet[s] = true
		}
		events := genEvents(rng, 25+rng.Intn(20))

		m, err := apply.New(timeout, limit, allowList)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		sm := newSim(timeout, limit, allowSet)

		var log strings.Builder
		failed := false
		for i, e := range events {
			r1, n1, e1 := runEvent(m, e)
			r2, n2, e2 := runEventSim(sm, e)
			out1 := resultString(r1, n1, e1, e.op)
			out2 := resultString(r2, n2, e2, e.op)
			match := (e1 == nil) == (e2 == nil) && errors.Is(e1, e2) && r1 == r2 && n1 == n2
			fmt.Fprintf(&log, "  ev#%02d %-46s -> %-18s | sim %-18s match=%v\n", i, e, out1, out2, match)
			if !match {
				t.Logf("seq=%d 事件输出不一致（判定依据：真实实现与朴素模拟对同一事件的返回必须完全相同）:\n%s", seq, log.String())
				failed = true
				break
			}
			if msg := compareState(m, sm); msg != "" {
				t.Logf("seq=%d ev#%d 后状态不一致（判定依据：%s）:\n%s", seq, i, msg, log.String())
				failed = true
				break
			}
		}
		t.Logf("seq=%d T=%d Q=%d allow=%v events=%d dead=%d pending=%d -> %s",
			seq, timeout, limit, allowList, len(events), len(m.Dead()), m.PendingLen(),
			map[bool]string{true: "FAIL", false: "OK"}[failed])
		if failed {
			t.Fatalf("seq=%d 对拍失败", seq)
		}
	}
}
