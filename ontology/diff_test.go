package ontology

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// naiveEntry 是朴素实现的条目。
type naiveEntry struct {
	size, cost, freq int64
	h                *big.Rat
	last             int64
}

// naiveCache 每次驱逐都对全部条目做一次线性扫描求 (H, last) 最小者。
// 它直接复刻规格，用作与堆实现对拍的“参考模型”。
type naiveCache struct {
	cap, used, tick int64
	L               *big.Rat
	items           map[string]*naiveEntry
	log             strings.Builder
}

func newNaive(cap int64) *naiveCache {
	return &naiveCache{cap: cap, L: big.NewRat(0, 1), items: map[string]*naiveEntry{}}
}

func naiveScore(L *big.Rat, freq, cost, size int64) *big.Rat {
	return new(big.Rat).Add(L, new(big.Rat).SetFrac64(freq*cost, size))
}

func (n *naiveCache) put(key string, size, cost int64) ([]string, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	if size <= 0 {
		return nil, ErrInvalidSize
	}
	if cost < 1 {
		return nil, ErrInvalidCost
	}
	if size > n.cap {
		return nil, ErrObjectTooLarge
	}
	fmt.Fprintf(&n.log, "PUT key=%q size=%d cost=%d\n", key, size, cost)

	if old, ok := n.items[key]; ok {
		delete(n.items, key)
		n.used -= old.size
	}
	var evicted []string
	for n.used+size > n.cap {
		// 线性扫描求最小 (H, last)。
		var victimKey string
		var victim *naiveEntry
		for k, e := range n.items {
			if victim == nil || e.h.Cmp(victim.h) < 0 ||
				(e.h.Cmp(victim.h) == 0 && e.last < victim.last) {
				victim, victimKey = e, k
			}
		}
		n.L.Set(victim.h)
		n.used -= victim.size
		delete(n.items, victimKey)
		evicted = append(evicted, victimKey)
		fmt.Fprintf(&n.log, "  evict %q -> L=%s\n", victimKey, n.L.RatString())
	}
	n.items[key] = &naiveEntry{
		size: size, cost: cost, freq: 1,
		h: naiveScore(n.L, 1, cost, size), last: n.tick,
	}
	n.used += size
	n.tick++
	fmt.Fprintf(&n.log, "  inserted H=%s last=%d evicted=%v used=%d L=%s\n",
		n.items[key].h.RatString(), n.items[key].last, evicted, n.used, n.L.RatString())
	return evicted, nil
}

func (n *naiveCache) get(key string) (bool, error) {
	if key == "" {
		return false, ErrEmptyKey
	}
	e, ok := n.items[key]
	if !ok {
		fmt.Fprintf(&n.log, "GET key=%q -> miss\n", key)
		return false, nil
	}
	e.freq++
	e.h = naiveScore(n.L, e.freq, e.cost, e.size)
	e.last = n.tick
	n.tick++
	fmt.Fprintf(&n.log, "GET key=%q -> hit freq=%d H=%s last=%d L=%s\n",
		key, e.freq, e.h.RatString(), e.last, n.L.RatString())
	return true, nil
}

// snapshot 返回 (used, L, 每个键的 freq/H/last)，用于整体状态比对。
func (n *naiveCache) snapshot() string {
	var b strings.Builder
	fmt.Fprintf(&b, "used=%d tick=%d L=%s\n", n.used, n.tick, n.L.RatString())
	for k, e := range n.items {
		fmt.Fprintf(&b, "  %q freq=%d H=%s last=%d size=%d cost=%d\n",
			k, e.freq, e.h.RatString(), e.last, e.size, e.cost)
	}
	return b.String()
}

func (c *Cache) snapshot() string {
	var b strings.Builder
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(&b, "used=%d tick=%d L=%s\n", c.used, c.tick, c.L.RatString())
	for k, e := range c.items {
		fmt.Fprintf(&b, "  %q freq=%d H=%s last=%d size=%d cost=%d\n",
			k, e.freq, e.h.RatString(), e.last, e.size, e.cost)
	}
	return b.String()
}

type op struct {
	kind       int // 0=Put, 1=Get
	key        string
	size, cost int64
}

// TestDifferentialAgainstNaive 用 2000 组随机操作序列对拍堆实现与朴素实现。
// 每一组打印输入、输出与判定依据（-v 可见全部；失败时额外转储完整日志）。
func TestDifferentialAgainstNaive(t *testing.T) {
	const groups = 2000
	rng := rand.New(rand.NewSource(20261001))
	for g := 0; g < groups; g++ {
		cap := int64(1 + rng.Intn(12))
		real, _ := NewCache(cap)
		ref := newNaive(cap)

		nOps := 5 + rng.Intn(40)
		keys := []string{"a", "b", "c", "d", "e", "f", "g", ""}
		var seq strings.Builder
		fmt.Fprintf(&seq, "==== group %d cap=%d ops=%d ====\n", g, cap, nOps)

		for i := 0; i < nOps; i++ {
			k := keys[rng.Intn(len(keys))]
			switch rng.Intn(2) {
			case 0: // Put
				// 让 size/cost 偶尔非法，验证拒绝路径两实现一致。
				var size, cost int64
				switch rng.Intn(10) {
				case 0:
					size = int64(rng.Intn(2)) // 0 或 1（可能合法）
				default:
					size = int64(1 + rng.Intn(int(cap)+3))
				}
				if rng.Intn(10) == 0 {
					cost = int64(rng.Intn(3)) // 0,1,2（0 非法）
				} else {
					cost = int64(1 + rng.Intn(8))
				}
				rEv, rErr := real.Put(k, size, cost)
				nEv, nErr := ref.put(k, size, cost)
				fmt.Fprintf(&seq, "PUT %q size=%d cost=%d -> real(ev=%v,err=%v) ref(ev=%v,err=%v)\n",
					k, size, cost, rEv, errName(rErr), nEv, errName(nErr))
				if errName(rErr) != errName(nErr) || !sameSlice(rEv, nEv) {
					t.Fatalf("group %d PUT mismatch\n判定依据: 错误/驱逐序列不一致\n%s\n--- ref log ---\n%s\n--- real state ---\n%s\n--- ref state ---\n%s",
						g, seq.String(), ref.log.String(), real.snapshot(), ref.snapshot())
				}
			case 1: // Get
				rHit, rErr := real.Get(k)
				nHit, nErr := ref.get(k)
				fmt.Fprintf(&seq, "GET %q -> real(hit=%v,err=%v) ref(hit=%v,err=%v)\n",
					k, rHit, errName(rErr), nHit, errName(nErr))
				if rHit != nHit || errName(rErr) != errName(nErr) {
					t.Fatalf("group %d GET mismatch\n判定依据: 命中/错误不一致\n%s", g, seq.String())
				}
			}
		}

		// 逐键比较 freq/H/last，再比较 used/L/tick；snapshot 文本化后整体对比。
		rs, ns := real.snapshot(), ref.snapshot()
		if normalizeSnap(rs) != normalizeSnap(ns) {
			t.Fatalf("group %d final state mismatch\n判定依据: used/tick/L 或某条目的 freq/H/last 不一致\n%s\n--- ref log ---\n%s--- real state ---\n%s--- ref state ---\n%s",
				g, seq.String(), ref.log.String(), rs, ns)
		}
		fmt.Fprintf(&seq, "判定: PASS（驱逐序列、错误原因、used/L/tick、freq/H/last 全部一致）\n")
		t.Logf("\n%s%s", seq.String(), rs)
	}
}

func errName(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func sameSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// normalizeSnap 对 snapshot 行排序，消除 map 遍历顺序差异（首行 used/tick/L 保留在最前）。
func normalizeSnap(s string) string {
	lines := splitLines(s)
	if len(lines) <= 2 {
		return s
	}
	head := lines[0]
	rest := append([]string{}, lines[1:]...)
	sortStrings(rest)
	return head + "\n" + strings.Join(rest, "\n")
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func sortStrings(a []string) {
	// 简单插入排序，序列很短且避免引入 sort 包造成无关差异。
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
