package scrape_test

import (
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/head"
	"ontology/query"
	"ontology/scrape"
)

type op struct {
	kind    int // 0=Append 1=AppendStale 2=Scrape
	series  string
	ts      int64
	v       int64
	target  string
	outcome int // 0=ok 1=error 2=panic
	values  map[string]int64
}

func genScript(seed int64, count int) []op {
	rng := rand.New(rand.NewSource(seed))
	names := []string{"a", "b", "c", "d", "e"}
	targets := []string{"m0", "m1", "m2"}
	free := []string{"free1", "free2", "m0/a", "m1/b"} // 含 target 自有序列以制造冲突
	nows := map[string]int64{}
	clock := int64(1000)
	ops := make([]op, 0, count)
	var lastDirect *op
	for i := 0; i < count; i++ {
		if lastDirect != nil && rng.Intn(100) < 12 { // 重放上次直写：同值为重复，异值为冲突
			d := *lastDirect
			if rng.Intn(2) == 0 {
				d.v++
			}
			ops = append(ops, d)
			lastDirect = nil
			continue
		}
		if rng.Intn(100) < 30 {
			clock += int64(rng.Intn(50))
			if ts := clock - int64(rng.Intn(120)); ts >= 0 {
				d := op{kind: rng.Intn(2), series: free[rng.Intn(4)], ts: ts, v: int64(rng.Intn(10))}
				ops = append(ops, d)
				lastDirect = &d
			}
			continue
		}
		tg := targets[rng.Intn(3)]
		nows[tg] += 1 + int64(rng.Intn(400))
		o := op{kind: 2, target: tg, ts: nows[tg]}
		switch p := rng.Intn(100); {
		case p < 8:
			o.outcome = 1
		case p < 13:
			o.outcome = 2
		case p < 21: // 个数超过 Lim=4
			o.values = map[string]int64{}
			for j := 0; j < 5+rng.Intn(3); j++ {
				o.values[string(rune('n'+j))] = int64(j)
			}
		case p < 28: // 非法名字
			bad := []string{"", "up", strings.Repeat("x", 64)}
			o.values = map[string]int64{bad[rng.Intn(3)]: 1, "ok": 2}
		default:
			o.values = map[string]int64{}
			for _, n := range names {
				if rng.Intn(100) < 60 {
					o.values[n] = int64(rng.Intn(1000))
				}
			}
		}
		ops = append(ops, o)
	}
	return ops
}

func fetchFor(o op) func() (map[string]int64, error) {
	switch o.outcome {
	case 1:
		return func() (map[string]int64, error) { return nil, errors.New("boom") }
	case 2:
		return func() (map[string]int64, error) { panic("kaboom") }
	default:
		return func() (map[string]int64, error) { return o.values, nil }
	}
}

// runScript 在真实实现与朴素模拟上重放同一脚本，逐步对照错误、结果与状态。
func runScript(t *testing.T, ops []op) *head.Head {
	t.Helper()
	h, err := head.NewHead(500, 50, 40, 4)
	if err != nil {
		t.Fatal(err)
	}
	q, s, n := query.New(h), scrape.New(h), newNaive(500, 50, 40)
	targets := map[string]*naiveTarget{}
	for i, o := range ops {
		if o.kind == 2 {
			tg := targets[o.target]
			if tg == nil {
				tg = &naiveTarget{name: o.target, prev: map[string]bool{}}
				targets[o.target] = tg
			}
			gotR, gotErr := s.Scrape(o.ts, o.target, fetchFor(o))
			wantR, wantErr := naiveScrape(n, 4, tg, o.ts, o.outcome, o.values)
			if gotErr != wantErr || gotR != wantR {
				t.Fatalf("op%d Scrape(%d,%s): 真实=(%+v,%v) 模型=(%+v,%v)", i, o.ts, o.target, gotR, gotErr, wantR, wantErr)
			}
		} else {
			it := head.Item{Series: o.series, Ts: o.ts, V: o.v, Stale: o.kind == 1}
			var gotErr error
			if o.kind == 1 {
				gotErr, it.V = h.AppendStale(o.series, o.ts), 0
			} else {
				gotErr = h.Append(o.series, o.ts, o.v)
			}
			if wantErr := n.batch([]head.Item{it}); gotErr != wantErr {
				t.Fatalf("op%d %+v: 真实 err=%v 模型 err=%v", i, it, gotErr, wantErr)
			}
		}
		if i%80 == 0 {
			t.Logf("op%d 输入=%+v", i, o)
		}
	}
	if got, want := h.Snapshot(), n.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("样本集合不一致:\n真实=%v\n模型=%v", got, want)
	}
	for _, name := range []string{"m0/a", "m1/b", "free1", "m0/up", "m2/e"} {
		for _, tt := range []int64{500, 1500, 3000, 6000, 20000} {
			if got, want := q.Instant(name, tt), n.instant(name, tt); got != want {
				t.Fatalf("Instant(%s,%d): 真实=%+v 模型=%+v", name, tt, got, want)
			}
		}
	}
	if h.Dups() != n.dups {
		t.Fatalf("Dups: 真实=%d 模型=%d", h.Dups(), n.dups)
	}
	t.Logf("输出: %d 个操作后样本集合、瞬时查询、Dups(%d) 与朴素模拟一致；依据: 逐步对照同一输入", len(ops), n.dups)
	return h
}

// TestAgainstNaiveModel 与朴素模拟对照；并重放两次验证可精确复现。
func TestAgainstNaiveModel(t *testing.T) {
	h1 := runScript(t, genScript(42, 400))
	h2 := runScript(t, genScript(42, 400))
	if !reflect.DeepEqual(h1.Snapshot(), h2.Snapshot()) {
		t.Fatal("相同输入序列重放应得到相同样本集合")
	}
	t.Logf("判定依据: 相同输入重放两次，样本集合完全一致（序列数=%d）", len(h1.Snapshot()))
}
