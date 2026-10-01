package fusion_test

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/fusion"
)

// naiveFuser 是按题面规则用 big.Rat 逐步写成的朴素融合器，用于对拍。
type naiveFuser struct {
	srcs  map[string]*naiveSource
	order []string
	h     int64
	rank  map[string]int
}

type naiveSource struct {
	weight int64
	hb     bool
	ttl    int64
	scores map[string]int64
	ok     bool
	t      int64
}

func newNaive() *naiveFuser {
	return &naiveFuser{srcs: map[string]*naiveSource{}, rank: map[string]int{}}
}

func (n *naiveFuser) addSource(name string, weight int, hb bool, ttl int64) error {
	if name == "" || weight < 1 || weight > 1000 || ttl < 1 || ttl > 1000000 {
		return fusion.ErrInvalidArgument
	}
	if _, dup := n.srcs[name]; dup {
		return fusion.ErrDuplicateSource
	}
	n.srcs[name] = &naiveSource{weight: int64(weight), hb: hb, ttl: ttl}
	n.order = append(n.order, name)
	return nil
}

func (n *naiveFuser) submit(name string, hits []fusion.Hit, now int64) error {
	src, ok := n.srcs[name]
	if !ok {
		return fusion.ErrSourceNotFound
	}
	if len(hits) == 0 || now < 0 {
		return fusion.ErrInvalidArgument
	}
	for _, hit := range hits {
		if hit.Doc == "" || hit.Score > 1000000000000000 || hit.Score < -1000000000000000 {
			return fusion.ErrInvalidArgument
		}
	}
	if now < n.h {
		return fusion.ErrClockRegression
	}
	scores := map[string]int64{}
	for _, hit := range hits {
		cur, seen := scores[hit.Doc]
		if !seen || (src.hb && hit.Score > cur) || (!src.hb && hit.Score < cur) {
			scores[hit.Doc] = hit.Score
		}
	}
	src.scores = scores
	src.ok = true
	src.t = now
	if now > n.h {
		n.h = now
	}
	return nil
}

func (n *naiveFuser) fuse(now int64, k int) ([]fusion.Item, error) {
	if k < 1 || now < 0 {
		return nil, fusion.ErrInvalidArgument
	}
	if now < n.h {
		return nil, fusion.ErrClockRegression
	}
	var valid []*naiveSource
	for _, name := range n.order {
		src := n.srcs[name]
		if src.ok && now-src.t < src.ttl {
			valid = append(valid, src)
		}
	}
	if len(valid) == 0 {
		return nil, fusion.ErrNoFusableSources
	}
	// 每个有效来源的 lo/hi。
	type bounds struct{ lo, hi int64 }
	bs := make([]bounds, len(valid))
	for i, src := range valid {
		first := true
		for _, s := range src.scores {
			if first {
				bs[i].lo, bs[i].hi = s, s
				first = false
				continue
			}
			if s < bs[i].lo {
				bs[i].lo = s
			}
			if s > bs[i].hi {
				bs[i].hi = s
			}
		}
	}
	// 逐文档计算总分：Σ weight_j · n_j(d)，缺席为 0。
	docSet := map[string]bool{}
	for _, src := range valid {
		for doc := range src.scores {
			docSet[doc] = true
		}
	}
	type entry struct {
		doc   string
		total *big.Rat
	}
	var entries []entry
	for doc := range docSet {
		total := new(big.Rat)
		for i, src := range valid {
			s, present := src.scores[doc]
			if !present {
				continue
			}
			span := bs[i].hi - bs[i].lo
			var num int64
			if span == 0 {
				num = 1
				span = 1
			} else if src.hb {
				num = s - bs[i].lo
			} else {
				num = bs[i].hi - s
			}
			term := new(big.Rat).SetInt64(num)
			term.Quo(term, new(big.Rat).SetInt64(span))
			term.Mul(term, new(big.Rat).SetInt64(src.weight))
			total.Add(total, term)
		}
		entries = append(entries, entry{doc: doc, total: total})
	}
	// 排序：总分降序；并列时名次表中出现过的优先、名次升序；否则字节序。
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if c := a.total.Cmp(b.total); c != 0 {
			return c > 0
		}
		ra, oka := n.rank[a.doc]
		rb, okb := n.rank[b.doc]
		switch {
		case oka && !okb:
			return true
		case !oka && okb:
			return false
		case oka && okb && ra != rb:
			return ra < rb
		default:
			return a.doc < b.doc
		}
	})
	n.rank = map[string]int{}
	for i, e := range entries {
		n.rank[e.doc] = i
	}
	if now > n.h {
		n.h = now
	}
	if k > len(entries) {
		k = len(entries)
	}
	items := make([]fusion.Item, k)
	for i := 0; i < k; i++ {
		items[i] = fusion.Item{
			Doc:   entries[i].doc,
			Score: entries[i].total.Num().String() + "/" + entries[i].total.Denom().String(),
		}
	}
	return items, nil
}

func errReason(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, fusion.ErrInvalidArgument):
		return "invalid-argument"
	case errors.Is(err, fusion.ErrDuplicateSource):
		return "duplicate-source"
	case errors.Is(err, fusion.ErrSourceNotFound):
		return "source-not-found"
	case errors.Is(err, fusion.ErrClockRegression):
		return "clock-regression"
	case errors.Is(err, fusion.ErrNoFusableSources):
		return "no-fusable-sources"
	default:
		return "unknown"
	}
}

// TestDifferential 用随机操作序列将 Fuser 与朴素实现对拍。
func TestDifferential(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261002))
	for seq := 0; seq < sequences; seq++ {
		real := fusion.NewFuser()
		naive := newNaive()
		var log strings.Builder
		ops := 3 + rng.Intn(20)
		failed := false
		for op := 0; op < ops && !failed; op++ {
			name := fmt.Sprintf("s%d", rng.Intn(5))
			// now 围绕水位波动，偶尔回退。
			h := rng.Int63n(8)
			now := h
			switch rng.Intn(4) {
			case 0:
				now = rng.Int63n(10)
			case 1:
				now = 100*int64(seq)/int64(sequences) + h
			}
			var desc string
			var gotErr, wantErr error
			switch rng.Intn(3) {
			case 0: // AddSource
				weight := 1 + rng.Intn(1000)
				ttl := int64(1 + rng.Intn(1000000))
				switch rng.Intn(8) {
				case 0:
					weight = rng.Intn(2) // 可能为 0
				case 1:
					weight = 1000 + rng.Intn(3) // 可能越界
				case 2:
					ttl = int64(rng.Intn(2)) // 可能为 0
				case 3:
					ttl = 1000000 + int64(rng.Intn(3)) // 可能越界
				}
				hb := rng.Intn(2) == 0
				desc = fmt.Sprintf("AddSource(%q, w=%d, hb=%v, ttl=%d)", name, weight, hb, ttl)
				gotErr = real.AddSource(name, weight, hb, ttl)
				wantErr = naive.addSource(name, weight, hb, ttl)
			case 1: // Submit
				var hits []fusion.Hit
				if rng.Intn(8) != 0 { // 偶尔为空 hits
					n := 1 + rng.Intn(6)
					hits = make([]fusion.Hit, n)
					for i := range hits {
						doc := fmt.Sprintf("d%d", rng.Intn(8))
						var score int64
						switch rng.Intn(6) {
						case 0:
							score = 1000000000000000 // 恰为 10^15
						case 1:
							score = -1000000000000000
						case 2:
							score = rng.Int63n(2001) - 1000
						default:
							score = rng.Int63n(21) - 10 // 小分数制造并列
						}
						hits[i] = fusion.Hit{Doc: doc, Score: score}
					}
				}
				desc = fmt.Sprintf("Submit(%q, %v, now=%d)", name, hits, now)
				gotErr = real.Submit(name, hits, now)
				wantErr = naive.submit(name, hits, now)
			default: // Fuse
				k := rng.Intn(8) // 可能为 0
				desc = fmt.Sprintf("Fuse(now=%d, k=%d)", now, k)
				var got, want []fusion.Item
				got, gotErr = real.Fuse(now, k)
				want, wantErr = naive.fuse(now, k)
				if errReason(gotErr) == "ok" && errReason(wantErr) == "ok" {
					if !equalItems(got, want) {
						failed = true
						fmt.Fprintf(&log, "op%d %s\n  got  %v\n  want %v\n", op, desc, got, want)
						break
					}
					// 同一 now 下连续融合结果逐字段相同。
					again, err2 := real.Fuse(now, k)
					if err2 != nil || !equalItems(got, again) {
						failed = true
						fmt.Fprintf(&log, "op%d %s 非幂等: first=%v second=%v err=%v\n", op, desc, got, again, err2)
						break
					}
					naive.fuse(now, k) // 保持名次表一致
				}
				fmt.Fprintf(&log, "op%d %s -> got=%v(%s) want=%v(%s)\n", op, desc, got, errReason(gotErr), want, errReason(wantErr))
				if errReason(gotErr) != errReason(wantErr) {
					failed = true
				}
				continue
			}
			fmt.Fprintf(&log, "op%d %s -> got=%s want=%s\n", op, desc, errReason(gotErr), errReason(wantErr))
			if errReason(gotErr) != errReason(wantErr) {
				failed = true
			}
		}
		if failed {
			t.Fatalf("seq %d 对拍失败，判定依据（输入/输出/拒绝原因）:\n%s", seq, log.String())
		}
		t.Logf("seq %d 输入/输出/判定依据:\n%s", seq, log.String())
	}
}

func equalItems(a, b []fusion.Item) bool {
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
