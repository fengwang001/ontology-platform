package srv

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveSelector 是按需求规则逐步写成的朴素参照实现：
// 用切片线性扫描，不做任何优化，用于与 Selector 对拍。
type naiveSelector struct {
	cap     int
	coolCap int64
	wr      int64
	recs    []*naiveRecord
	nextSeq uint64
}

type naiveRecord struct {
	target        string
	port          int
	priority      int
	weight        int
	expiresAt     int64
	seq           uint64
	cooldownUntil int64
	failures      uint64
	lastFail      int64
	hasLastFail   bool
}

func (n *naiveSelector) find(target string, port int) *naiveRecord {
	for _, rec := range n.recs {
		if rec.target == target && rec.port == port {
			return rec
		}
	}
	return nil
}

func (n *naiveSelector) purge(now int64) int {
	kept := n.recs[:0]
	removed := 0
	for _, rec := range n.recs {
		if rec.expiresAt <= now {
			removed++
		} else {
			kept = append(kept, rec)
		}
	}
	n.recs = kept
	return removed
}

func (n *naiveSelector) add(target string, port, priority, weight int, ttl, now int64) ErrCode {
	if target == "" || port < 1 || port > MaxPort ||
		priority < 0 || priority > MaxPriority ||
		weight < 0 || weight > MaxWeight ||
		ttl < 1 || ttl > MaxTTL {
		return ErrInvalidParam
	}
	if !validNow(now) {
		return ErrInvalidTime
	}
	if rec := n.find(target, port); rec != nil {
		rec.priority = priority
		rec.weight = weight
		rec.expiresAt = now + ttl
		return -1
	}
	if len(n.recs) >= n.cap {
		n.purge(now)
		if len(n.recs) >= n.cap {
			return ErrFull
		}
	}
	n.nextSeq++
	n.recs = append(n.recs, &naiveRecord{
		target:    target,
		port:      port,
		priority:  priority,
		weight:    weight,
		expiresAt: now + ttl,
		seq:       n.nextSeq,
	})
	return -1
}

func (n *naiveSelector) failure(target string, port int, cooldown, now int64) ErrCode {
	if target == "" || cooldown < 1 || cooldown > MaxCooldown {
		return ErrInvalidParam
	}
	if !validNow(now) {
		return ErrInvalidTime
	}
	rec := n.find(target, port)
	if rec == nil {
		return ErrNotFound
	}
	if now >= rec.expiresAt {
		return ErrExpired
	}
	if !rec.hasLastFail || now >= rec.lastFail+n.wr {
		rec.failures = 0
	}
	rec.failures++
	rec.lastFail = now
	rec.hasLastFail = true
	shift := rec.failures - 1
	if shift > 30 {
		shift = 30
	}
	effective := cooldown << shift
	if effective > n.coolCap {
		effective = n.coolCap
	}
	if until := now + effective; until > rec.cooldownUntil {
		rec.cooldownUntil = until
	}
	return -1
}

func (n *naiveSelector) success(target string, port int, now int64) ErrCode {
	if target == "" {
		return ErrInvalidParam
	}
	if !validNow(now) {
		return ErrInvalidTime
	}
	rec := n.find(target, port)
	if rec == nil {
		return ErrNotFound
	}
	if now >= rec.expiresAt {
		return ErrExpired
	}
	rec.failures = 0
	return -1
}

func (n *naiveSelector) pick(r uint64, now int64) (string, int, ErrCode) {
	if !validNow(now) {
		return "", 0, ErrInvalidTime
	}
	minPriority := -1
	for _, rec := range n.recs {
		if now < rec.expiresAt && now >= rec.cooldownUntil {
			if minPriority < 0 || rec.priority < minPriority {
				minPriority = rec.priority
			}
		}
	}
	if minPriority < 0 {
		return "", 0, ErrNoAvailable
	}
	var zero, weighted []*naiveRecord
	for _, rec := range n.recs {
		if now < rec.expiresAt && now >= rec.cooldownUntil && rec.priority == minPriority {
			if rec.weight == 0 {
				zero = append(zero, rec)
			} else {
				weighted = append(weighted, rec)
			}
		}
	}
	bySeq := func(recs []*naiveRecord) {
		sort.Slice(recs, func(i, j int) bool { return recs[i].seq < recs[j].seq })
	}
	bySeq(zero)
	bySeq(weighted)
	group := append(zero, weighted...)
	var sum uint64
	for _, rec := range group {
		sum += uint64(rec.weight)
	}
	r1 := r % (sum + 1)
	var cumulative uint64
	for _, rec := range group {
		cumulative += uint64(rec.weight)
		if cumulative >= r1 {
			return rec.target, rec.port, -1
		}
	}
	return "", 0, ErrNoAvailable
}

// stateDump 导出 Selector 内部状态用于与朴素实现逐字段对照。
func (s *Selector) stateDump() string {
	recs := make([]*record, 0, len(s.records))
	for _, rec := range s.records {
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].seq < recs[j].seq })
	out := fmt.Sprintf("nextSeq=%d", s.nextSeq)
	for _, rec := range recs {
		out += fmt.Sprintf(" | (%s,%d) seq=%d pri=%d w=%d exp=%d until=%d f=%d lf=%d/%v",
			rec.target, rec.port, rec.seq, rec.priority, rec.weight,
			rec.expiresAt, rec.cooldownUntil, rec.failures, rec.lastFail, rec.hasLastFail)
	}
	return out
}

func (n *naiveSelector) stateDump() string {
	recs := make([]*naiveRecord, len(n.recs))
	copy(recs, n.recs)
	sort.Slice(recs, func(i, j int) bool { return recs[i].seq < recs[j].seq })
	out := fmt.Sprintf("nextSeq=%d", n.nextSeq)
	for _, rec := range recs {
		out += fmt.Sprintf(" | (%s,%d) seq=%d pri=%d w=%d exp=%d until=%d f=%d lf=%d/%v",
			rec.target, rec.port, rec.seq, rec.priority, rec.weight,
			rec.expiresAt, rec.cooldownUntil, rec.failures, rec.lastFail, rec.hasLastFail)
	}
	return out
}

// errCodeOf 把 Selector 返回的 error 映射为 ErrCode，-1 表示无错误。
func errCodeOf(err error) ErrCode {
	if err == nil {
		return -1
	}
	return err.(*Error).Code
}

// TestDifferentialAgainstNaive 用 2000 组随机记录集合、随机失败/成功序列
// 与随机数，把 Selector 与朴素参照实现逐步对照，日志打印输入、输出与判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	const trials = 2000
	rng := rand.New(rand.NewSource(20261002))
	for trial := 0; trial < trials; trial++ {
		capacity := 1 + rng.Intn(6)
		coolCap := int64(1 + rng.Intn(200))
		wr := int64(1 + rng.Intn(200))
		sel, err := New(capacity, coolCap, wr)
		if err != nil {
			t.Fatalf("trial %d: New 失败: %v", trial, err)
		}
		naive := &naiveSelector{cap: capacity, coolCap: coolCap, wr: wr}

		ops := 20 + rng.Intn(40)
		for step := 0; step < ops; step++ {
			target := fmt.Sprintf("t%d", rng.Intn(4))
			port := 1 + rng.Intn(3)
			now := int64(rng.Intn(300))
			var input, output, basis string

			switch rng.Intn(5) {
			case 0: // Add
				priority := rng.Intn(4)
				weight := rng.Intn(21)
				if rng.Intn(4) == 0 {
					weight = 0 // 提高 0 权重占比
				}
				ttl := int64(1 + rng.Intn(50))
				got := errCodeOf(sel.Add(target, port, priority, weight, ttl, now))
				want := naive.add(target, port, priority, weight, ttl, now)
				input = fmt.Sprintf("Add(%s,%d,pri=%d,w=%d,ttl=%d,now=%d)", target, port, priority, weight, ttl, now)
				output = fmt.Sprintf("err=%v", got)
				basis = fmt.Sprintf("naive err=%v", want)
				if got != want {
					t.Fatalf("trial %d step %d: %s\n got=%d want=%d\nsel:   %s\nnaive: %s",
						trial, step, input, got, want, sel.stateDump(), naive.stateDump())
				}
			case 1: // Failure
				cooldown := int64(1 + rng.Intn(30))
				got := errCodeOf(sel.Failure(target, port, cooldown, now))
				want := naive.failure(target, port, cooldown, now)
				input = fmt.Sprintf("Failure(%s,%d,cooldown=%d,now=%d)", target, port, cooldown, now)
				output = fmt.Sprintf("err=%v", got)
				basis = fmt.Sprintf("naive err=%v", want)
				if got != want {
					t.Fatalf("trial %d step %d: %s\n got=%d want=%d\nsel:   %s\nnaive: %s",
						trial, step, input, got, want, sel.stateDump(), naive.stateDump())
				}
			case 2: // Success
				got := errCodeOf(sel.Success(target, port, now))
				want := naive.success(target, port, now)
				input = fmt.Sprintf("Success(%s,%d,now=%d)", target, port, now)
				output = fmt.Sprintf("err=%v", got)
				basis = fmt.Sprintf("naive err=%v", want)
				if got != want {
					t.Fatalf("trial %d step %d: %s\n got=%d want=%d\nsel:   %s\nnaive: %s",
						trial, step, input, got, want, sel.stateDump(), naive.stateDump())
				}
			case 3: // Pick
				var r uint64
				switch rng.Intn(3) {
				case 0:
					r = uint64(rng.Intn(64)) // 小随机数，覆盖 r1 边界
				case 1:
					r = rng.Uint64()
				default:
					r = uint64(rng.Intn(1000))
				}
				gt, gp, gerr := sel.Pick(r, now)
				nt, np, nerr := naive.pick(r, now)
				input = fmt.Sprintf("Pick(r=%d, now=%d)", r, now)
				output = fmt.Sprintf("(%s,%d,err=%v)", gt, gp, gerr)
				basis = fmt.Sprintf("naive (%s,%d,err=%v)", nt, np, nerr)
				if errCodeOf(gerr) != nerr || gt != nt || gp != np {
					t.Fatalf("trial %d step %d: %s\n got=(%s,%d,%v) want=(%s,%d,%v)\nsel:   %s\nnaive: %s",
						trial, step, input, gt, gp, gerr, nt, np, nerr, sel.stateDump(), naive.stateDump())
				}
			case 4: // Purge
				gotN, gotErr := sel.Purge(now)
				wantCode := ErrCode(-1)
				wantN := 0
				if !validNow(now) {
					wantCode = ErrInvalidTime
				} else {
					wantN = naive.purge(now)
				}
				input = fmt.Sprintf("Purge(now=%d)", now)
				output = fmt.Sprintf("(n=%d, err=%v)", gotN, gotErr)
				basis = fmt.Sprintf("naive (n=%d, err=%v)", wantN, wantCode)
				if errCodeOf(gotErr) != wantCode || gotN != wantN {
					t.Fatalf("trial %d step %d: %s\n got=(%d,%v) want=(%d,%v)\nsel:   %s\nnaive: %s",
						trial, step, input, gotN, gotErr, wantN, wantCode, sel.stateDump(), naive.stateDump())
				}
			}

			// 每步之后对照完整内部状态（判定依据：两实现状态须逐字段一致）。
			if got, want := sel.stateDump(), naive.stateDump(); got != want {
				t.Fatalf("trial %d step %d: %s -> %s 后状态不一致\nsel:   %s\nnaive: %s",
					trial, step, input, output, got, want)
			}
			if trial < 3 { // 前 3 组打印完整逐步日志，便于人工核对
				t.Logf("trial=%d step=%d input=%s output=%s basis=%s state={%s}",
					trial, step, input, output, basis, sel.stateDump())
			}
		}
		if trial%500 == 0 {
			t.Logf("trial=%d 完成：cap=%d coolCap=%d wr=%d ops=%d 最终状态 {%s}",
				trial, capacity, coolCap, wr, ops, sel.stateDump())
		}
	}
}
