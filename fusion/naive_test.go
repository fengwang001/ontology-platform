package fusion

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveSrc 是朴素参考实现中的来源状态，完全独立于生产代码内部结构。
type naiveSrc struct {
	name         string
	weight       int
	higherBetter bool
	ttl          int64

	submitted bool
	submitAt  int64
	scores    map[string]int64 // doc -> 去重后最好分数
}

type naiveMerger struct {
	order   []string
	sources map[string]*naiveSrc
	water   int64
	ranks   []string // 上一次成功 Fuse 的完整名次表
}

func newNaive() *naiveMerger {
	return &naiveMerger{sources: map[string]*naiveSrc{}}
}

func (n *naiveMerger) addSource(name string, weight int, hb bool, ttl int64) error {
	if name == "" || weight < 1 || weight > 1000 || ttl < 1 || ttl > 1_000_000 {
		return ErrInvalidArgument
	}
	if _, ok := n.sources[name]; ok {
		return ErrDuplicateName
	}
	n.sources[name] = &naiveSrc{
		name: name, weight: weight, higherBetter: hb, ttl: ttl,
		scores: map[string]int64{},
	}
	n.order = append(n.order, name)
	return nil
}

func (n *naiveMerger) submit(name string, hits []Hit, now int64) error {
	src, ok := n.sources[name]
	if !ok {
		return ErrSourceNotFound
	}
	if len(hits) == 0 || now < 0 {
		return ErrInvalidArgument
	}
	for _, h := range hits {
		if h.Doc == "" || h.Score < -1_000_000_000_000_000 || h.Score > 1_000_000_000_000_000 {
			return ErrInvalidArgument
		}
	}
	if now < n.water {
		return ErrClockRollback
	}
	dedup := map[string]int64{}
	for _, h := range hits {
		if old, ok := dedup[h.Doc]; ok {
			if src.higherBetter && h.Score > old || !src.higherBetter && h.Score < old {
				dedup[h.Doc] = h.Score
			}
		} else {
			dedup[h.Doc] = h.Score
		}
	}
	src.scores = dedup
	src.submitted = true
	src.submitAt = now
	if now > n.water {
		n.water = now
	}
	return nil
}

// fuse 严格按题面规则用 big.Rat 逐步计算。
func (n *naiveMerger) fuse(now int64, k int) ([]ResultItem, error) {
	if k < 1 || now < 0 {
		return nil, ErrInvalidArgument
	}
	if now < n.water {
		return nil, ErrClockRollback
	}
	var active []*naiveSrc
	for _, name := range n.order {
		s := n.sources[name]
		if s.submitted && now-s.submitAt < s.ttl {
			active = append(active, s)
		}
	}
	if len(active) == 0 {
		return nil, ErrNothingToFuse
	}

	rankOf := map[string]int{}
	for i, d := range n.ranks {
		rankOf[d] = i
	}
	totals := map[string]*big.Rat{}
	for _, s := range active {
		var lo, hi int64
		first := true
		for _, v := range s.scores {
			if first || v < lo {
				lo = v
			}
			if first || v > hi {
				hi = v
			}
			first = false
		}
		for doc, score := range s.scores {
			var nv *big.Rat
			if hi == lo {
				nv = big.NewRat(1, 1)
			} else {
				if s.higherBetter {
					nv = big.NewRat(score-lo, (hi - lo))
				} else {
					nv = big.NewRat(hi-score, (hi - lo))
				}
			}
			nv.Mul(nv, big.NewRat(int64(s.weight), 1))
			t, ok := totals[doc]
			if !ok {
				t = new(big.Rat)
				totals[doc] = t
			}
			t.Add(t, nv)
		}
	}

	docs := make([]string, 0, len(totals))
	for d := range totals {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool {
		a, b := docs[i], docs[j]
		if c := totals[b].Cmp(totals[a]); c != 0 {
			return c < 0
		}
		ra, oka := rankOf[a]
		rb, okb := rankOf[b]
		if oka != okb {
			return oka
		}
		if oka && ra != rb {
			return ra < rb
		}
		return a < b
	})

	n.ranks = append([]string(nil), docs...)
	if now > n.water {
		n.water = now
	}

	limit := k
	if limit > len(docs) {
		limit = len(docs)
	}
	out := make([]ResultItem, limit)
	for i := 0; i < limit; i++ {
		r := totals[docs[i]]
		out[i] = ResultItem{Doc: docs[i], Score: r.Num().String() + "/" + r.Denom().String()}
	}
	return out, nil
}

var docPool = []string{"a", "b", "c", "x", "y", "z", "p", "q", "r", "doc-1", "doc-2", "中文", ""}

// TestRandomDifferential：与朴素实现对拍 2000 组随机操作序列，
// 日志打印每组的输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential test in -short mode")
	}
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261001))
	for seq := 0; seq < sequences; seq++ {
		m := New()
		n := newNaive()
		var logb strings.Builder
		fmt.Fprintf(&logb, "--- sequence %d ---", seq)
		steps := 10 + rng.Intn(31)
		for step := 0; step < steps; step++ {
			op := rng.Intn(3)
			switch op {
			case 0: // AddSource
				pick := []string{"", "A", "B", "C", "A", "s0", "s1"}
				name := pick[rng.Intn(len(pick))]
				weight := []int{1, 1000, rng.Intn(10) + 1, 0, 1001, rng.Intn(1000) + 1}[rng.Intn(6)]
				hb := rng.Intn(2) == 0
				ttl := []int64{1, 1_000_000, rng.Int63n(10) + 1, 0, 1_000_001}[rng.Intn(5)]
				got := m.AddSource(name, weight, hb, ttl)
				want := n.addSource(name, weight, hb, ttl)
				fmt.Fprintf(&logb, "\nadd name=%q weight=%d hb=%v ttl=%d => got=%v want=%v",
					name, weight, hb, ttl, got, want)
				if !sameReason(got, want) {
					t.Fatalf("seq %d step %d AddSource mismatch: %v vs %v\n%s", seq, step, got, want, logb.String())
				}
			case 1: // Submit
				names := append([]string{"ghost"}, n.order...)
				name := names[rng.Intn(len(names))]
				nHits := rng.Intn(5)
				var hits []Hit
				for i := 0; i < nHits; i++ {
					doc := docPool[rng.Intn(len(docPool))]
					var score int64
					switch rng.Intn(6) {
					case 0:
						score = 1_000_000_000_000_000
					case 1:
						score = -1_000_000_000_000_000
					case 2:
						score = 1_000_000_000_000_001 * sign(rng)
					default:
						score = rng.Int63n(20) - 10
					}
					hits = append(hits, Hit{Doc: doc, Score: score})
				}
				now := []int64{0, rng.Int63n(8), n.water, n.water - 1, -1}[rng.Intn(5)]
				got := m.Submit(name, append([]Hit(nil), hits...), now)
				want := n.submit(name, hits, now)
				fmt.Fprintf(&logb, "\nsubmit src=%q now=%d hits=%v => got=%v want=%v",
					name, now, hits, got, want)
				if !sameReason(got, want) {
					t.Fatalf("seq %d step %d Submit mismatch: %v vs %v\n%s", seq, step, got, want, logb.String())
				}
			case 2: // Fuse
				now := []int64{0, rng.Int63n(8), n.water, n.water - 1, -1}[rng.Intn(5)]
				k := []int{1, 2, 3, 100, 0, -2}[rng.Intn(6)]
				gotItems, gotErr := m.Fuse(now, k)
				wantItems, wantErr := n.fuse(now, k)
				basis := "no active source / rank-inertia order applied"
				if gotErr == nil {
					basis = fmt.Sprintf("scores=%v", wantItems)
				}
				fmt.Fprintf(&logb, "\nfuse now=%d k=%d => got=(%v,%v) want=(%v,%v) basis=%s",
					now, k, gotItems, gotErr, wantItems, wantErr, basis)
				if !sameReason(gotErr, wantErr) {
					t.Fatalf("seq %d step %d Fuse error mismatch: %v vs %v\n%s", seq, step, gotErr, wantErr, logb.String())
				}
				if gotErr == nil && !equalItems(gotItems, wantItems) {
					t.Fatalf("seq %d step %d Fuse items mismatch:\n got=%v\nwant=%v\n%s",
						seq, step, gotItems, wantItems, logb.String())
				}
			}
		}
		t.Log(logb.String())
	}
}

func sign(rng *rand.Rand) int64 {
	if rng.Intn(2) == 0 {
		return 1
	}
	return -1
}

func sameReason(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	for _, target := range []error{ErrInvalidArgument, ErrDuplicateName, ErrSourceNotFound, ErrClockRollback, ErrNothingToFuse} {
		if errorIs(a, target) != errorIs(b, target) {
			return false
		}
	}
	return true
}

func errorIs(err, target error) bool { return err == target }

func equalItems(a, b []ResultItem) bool {
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
