package windowbuf

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// naive 是按规则逐步写成的朴素模拟：每次操作全表扫描并排序。
type naive struct {
	s, g, e int64
	policy  Policy
	st      int64
	buf     map[bufKey]bufValue
	late    int64
}

func newNaive(s, g, e int64, p Policy) *naive {
	return &naive{s: s, g: g, e: e, policy: p, st: -1, buf: make(map[bufKey]bufValue)}
}

type naiveItem struct {
	k   bufKey
	end int64
}

func sortItems(items []naiveItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].end != items[j].end {
			return items[i].end < items[j].end
		}
		return items[i].k.key < items[j].k.key
	})
}

func (n *naive) update(key []byte, ts, value int64) (Outcome, []Emitted) {
	if len(key) == 0 || ts < 0 || ts > maxTs || value < -maxValue || value > maxValue {
		return OutcomeInvalid, nil
	}
	ws := ts / n.s * n.s
	end := ws + n.s
	if end+n.g <= n.st {
		n.late++
		return OutcomeLate, nil
	}
	st := max(n.st, ts)

	var closing []naiveItem
	for k := range n.buf {
		e := k.ws + n.s
		if e+n.g <= st {
			closing = append(closing, naiveItem{k: k, end: e})
		}
	}
	sortItems(closing)

	rest := len(n.buf) - len(closing)
	needNew := 1
	k := bufKey{key: string(key), ws: ws}
	if _, ok := n.buf[k]; ok {
		needNew = 0
	}

	var early []naiveItem
	if rest+needNew > int(n.e) {
		if n.policy == Shutdown {
			return OutcomeRejected, nil
		}
		x := rest + needNew - int(n.e)
		inClosing := make(map[bufKey]bool, len(closing))
		for _, it := range closing {
			inClosing[it.k] = true
		}
		var cand []naiveItem
		for ck := range n.buf {
			if !inClosing[ck] {
				cand = append(cand, naiveItem{k: ck, end: ck.ws + n.s})
			}
		}
		sortItems(cand)
		early = cand[:x]
	}

	var out []Emitted
	for _, it := range closing {
		v := n.buf[it.k]
		delete(n.buf, it.k)
		out = append(out, Emitted{Key: []byte(it.k.key), Ws: it.k.ws, Value: v.value, LastTs: v.lastTs, Mark: Final})
	}
	for _, it := range early {
		v := n.buf[it.k]
		delete(n.buf, it.k)
		out = append(out, Emitted{Key: []byte(it.k.key), Ws: it.k.ws, Value: v.value, LastTs: v.lastTs, Mark: Early})
	}
	n.buf[k] = bufValue{value: value, lastTs: ts}
	n.st = st
	return OutcomeOK, out
}

func (n *naive) tick(t int64) (Outcome, []Emitted) {
	if t < 0 || t > maxTs {
		return OutcomeInvalid, nil
	}
	if t < n.st {
		return OutcomeRejected, nil
	}
	if t == n.st {
		return OutcomeOK, nil
	}
	var closing []naiveItem
	for k := range n.buf {
		e := k.ws + n.s
		if e+n.g <= t {
			closing = append(closing, naiveItem{k: k, end: e})
		}
	}
	sortItems(closing)
	var out []Emitted
	for _, it := range closing {
		v := n.buf[it.k]
		delete(n.buf, it.k)
		out = append(out, Emitted{Key: []byte(it.k.key), Ws: it.k.ws, Value: v.value, LastTs: v.lastTs, Mark: Final})
	}
	n.st = t
	return OutcomeOK, out
}

func (n *naive) buffered() []Entry {
	items := make([]naiveItem, 0, len(n.buf))
	for k := range n.buf {
		items = append(items, naiveItem{k: k, end: k.ws + n.s})
	}
	sortItems(items)
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		v := n.buf[it.k]
		out = append(out, Entry{Key: []byte(it.k.key), Ws: it.k.ws, Value: v.value, LastTs: v.lastTs})
	}
	return out
}

func entriesStr(es []Entry) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = entryStr(e)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// TestRandomVsNaive 用 2000 组随机操作序列对照堆实现与朴素模拟，
// 同时校验 peeks 上界、批内 Final 先于 Early、Final 关闭时刻不超过 ST、
// 以及相同序列重放结果完全一致。
func TestRandomVsNaive(t *testing.T) {
	const sequences = 2000
	keys := []string{"a", "b", "c", "d", "e"}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		s := 1 + rng.Int63n(20)
		g := rng.Int63n(11)
		e := int64(1) + rng.Int63n(6)
		policy := EmitEarly
		if rng.Intn(2) == 1 {
			policy = Shutdown
		}
		real := mustNew(t, s, g, e, policy)
		replay := mustNew(t, s, g, e, policy)
		sim := newNaive(s, g, e, policy)

		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d S=%d G=%d E=%d policy=%s\n", seq, s, g, e, policy)
		ops := 30 + rng.Intn(30)
		failed := false
		for op := 0; op < ops && !failed; op++ {
			st := real.StreamTime()
			isTick := rng.Intn(4) == 0
			var outR, outR2, outN Outcome
			var emR, emR2, emN []Emitted
			var desc string
			peeksBefore := real.peeks
			if isTick {
				tk := st + rng.Int63n(int64(3*s+g+6)) - 5
				if tk < 0 {
					tk = 0
				}
				if rng.Intn(50) == 0 {
					tk = maxTs + 1 // 非法参数
				}
				outR, emR = real.Tick(tk)
				outR2, emR2 = replay.Tick(tk)
				outN, emN = sim.tick(tk)
				desc = fmt.Sprintf("Tick(%d)", tk)
			} else {
				key := keys[rng.Intn(len(keys))]
				if rng.Intn(40) == 0 {
					key = "" // 非法参数
				}
				ts := st + rng.Int63n(int64(3*s+6)) - rng.Int63n(int64(2*s+1))
				if ts < 0 {
					ts = 0
				}
				if rng.Intn(50) == 0 {
					ts = -1 // 非法参数
				}
				value := rng.Int63n(2001) - 1000
				if rng.Intn(50) == 0 {
					value = maxValue + 1 // 非法参数
				}
				outR, emR = real.Update([]byte(key), ts, value)
				outR2, emR2 = replay.Update([]byte(key), ts, value)
				outN, emN = sim.update([]byte(key), ts, value)
				desc = fmt.Sprintf("Update(%q, %d, %d)", key, ts, value)
			}
			peeksDelta := real.peeks - peeksBefore
			fmt.Fprintf(&log, "  op=%d ST前=%d %s -> 实现:%s %s 模拟:%s %s peeks+%d\n",
				op, st, desc, outR, emitsStr(emR), outN, emitsStr(emN), peeksDelta)

			check := func(format string, args ...any) {
				t.Errorf("seq=%d op=%d %s: %s\n%s", seq, op, desc, fmt.Sprintf(format, args...), log.String())
				failed = true
			}
			if outR != outN {
				check("outcome 实现=%s 模拟=%s", outR, outN)
			}
			if emitsStr(emR) != emitsStr(emN) {
				check("emitted 实现=%s 模拟=%s", emitsStr(emR), emitsStr(emN))
			}
			if outR != outR2 || emitsStr(emR) != emitsStr(emR2) {
				check("重放不一致: %s %s vs %s %s", outR, emitsStr(emR), outR2, emitsStr(emR2))
			}
			if failed {
				break
			}
			// peeks 上界：不超过本次发出数+2。
			if peeksDelta > int64(len(emR))+2 {
				check("peeks 增量 %d 超过 发出数+2=%d", peeksDelta, len(emR)+2)
			}
			// 批内 Final 先于 Early，且各自按 (end,key) 升序。
			seenEarly := false
			prevEnd, prevKey := int64(-1), ""
			for _, em := range emR {
				if em.Mark == Early {
					seenEarly = true
				} else if seenEarly {
					check("批内 Final 出现在 Early 之后: %s", emitsStr(emR))
				}
				end := em.Ws + s
				if end < prevEnd || (end == prevEnd && string(em.Key) < prevKey) {
					check("批内次序非 (end,key) 升序: %s", emitsStr(emR))
				}
				prevEnd, prevKey = end, string(em.Key)
				// Final 的关闭时刻不大于发出后的 ST。
				if em.Mark == Final && end+g > real.StreamTime() {
					check("Final 关闭时刻 %d 大于发出后 ST=%d", end+g, real.StreamTime())
				}
			}
			// 缓冲条目数不超过 E。
			if n := len(real.Buffered()); int64(n) > e {
				check("缓冲条目数 %d 超过 E=%d", n, e)
			}
		}
		if failed {
			continue
		}
		// 终态对照：ST、迟到计数、缓冲内容。
		if real.StreamTime() != sim.st {
			t.Errorf("seq=%d: ST 实现=%d 模拟=%d\n%s", seq, real.StreamTime(), sim.st, log.String())
		}
		if real.Late() != sim.late {
			t.Errorf("seq=%d: Late 实现=%d 模拟=%d\n%s", seq, real.Late(), sim.late, log.String())
		}
		if got, want := entriesStr(real.Buffered()), entriesStr(sim.buffered()); got != want {
			t.Errorf("seq=%d: Buffered 实现=%s 模拟=%s\n%s", seq, got, want, log.String())
		}
		if seq < 3 {
			t.Logf("序列样例（输入/输出/判定对照）:\n%s终态: ST=%d Late=%d Buffered=%s",
				log.String(), real.StreamTime(), real.Late(), entriesStr(real.Buffered()))
		} else {
			t.Logf("seq=%d S=%d G=%d E=%d policy=%s ops=%d -> ST=%d Late=%d 一致",
				seq, s, g, e, policy, ops, real.StreamTime(), real.Late())
		}
	}
}

// TestConcurrent 在竞态检测下并发调用所有操作与查询，
// 校验 ST 单调不减与缓冲容量上界始终成立。
func TestConcurrent(t *testing.T) {
	const capacity = 64
	b := mustNew(t, 10, 5, capacity, EmitEarly)
	stop := make(chan struct{})
	var readerWG, workerWG sync.WaitGroup
	violations := make(chan string, 16)

	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		last := int64(-1)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if st := b.StreamTime(); st < last {
				violations <- fmt.Sprintf("ST 回退: %d -> %d", last, st)
			} else {
				last = st
			}
			if n := len(b.Buffered()); n > capacity {
				violations <- fmt.Sprintf("缓冲条目数 %d 超过 E=%d", n, capacity)
			}
			_ = b.Late()
		}
	}()

	for w := 0; w < 8; w++ {
		workerWG.Add(1)
		go func(id int) {
			defer workerWG.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 2000; i++ {
				key := []byte{byte('a' + rng.Intn(8))}
				b.Update(key, rng.Int63n(400), rng.Int63n(2000)-1000)
				if rng.Intn(4) == 0 {
					b.Tick(rng.Int63n(400))
				}
			}
		}(w)
	}
	workerWG.Wait()
	close(stop)
	readerWG.Wait()
	close(violations)
	for v := range violations {
		t.Error(v)
	}
	if n := len(b.Buffered()); n > capacity {
		t.Errorf("终态缓冲条目数 %d 超过 E=%d", n, capacity)
	}
	t.Logf("终态: ST=%d Late=%d Buffered=%d 条", b.StreamTime(), b.Late(), len(b.Buffered()))
}
