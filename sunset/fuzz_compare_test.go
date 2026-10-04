package sunset

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// TestRandomAgainstNaive 运行 1500 组随机操作序列，逐步对照真实闸门与朴素全量扫描模型；
// 日志打印每步输入、双方输出与判定依据（-v 可见）。
func TestRandomAgainstNaive(t *testing.T) {
	cfg := Config{Nmin: 60, Bw: 40, Pd: 12, X: 3, Q: 25, Xmax: 80}
	rng := rand.New(rand.NewSource(20261004))
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		g, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaive(cfg)
		var log strings.Builder
		fmt.Fprintf(&log, "seq %d cfg=%+v\n", seq, cfg)

		names := []string{"d0", "d1", "d2", "d3"}
		consumers := []string{"c0", "c1", "c2", "c3"}
		existing := []string{}
		var now int64

		for step := 0; step < 40; step++ {
			now += int64(rng.Intn(8))
			kind := rng.Intn(7)
			if kind == 0 && len(existing) >= len(names) {
				kind = 1 + rng.Intn(6)
			}
			if kind != 0 && len(existing) == 0 {
				kind = 0
			}
			o := op{kind: kind, now: now}
			switch kind {
			case 0:
				o.d = names[len(existing)]
				if len(existing) > 0 && rng.Intn(2) == 0 {
					pool := append([]string(nil), existing...)
					rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
					k := 1 + rng.Intn(len(pool))
					if k > 3 {
						k = 3
					}
					o.parents = pool[:k]
				}
			default:
				o.d = existing[rng.Intn(len(existing))]
				o.c = consumers[rng.Intn(len(consumers))]
				if kind == 1 {
					o.arg = int64(rng.Intn(int(cfg.Nmin) + 40))
				}
				if kind == 6 {
					o.arg = int64(1 + rng.Intn(int(cfg.Xmax)))
				}
			}

			var got opResult
			var reason string
			switch kind {
			case 0:
				got.err = sentinelName(g.AddDataset(o.d, o.parents, o.now))
				reason = "Add"
			case 1:
				affected, e := g.Deprecate(o.d, o.arg, o.now)
				got.err = sentinelName(e)
				got.items = affected
				reason = fmt.Sprintf("Deprecate notice=%d", o.arg)
			case 2:
				got.err = sentinelName(g.Undeprecate(o.d, o.now))
				reason = "Undeprecate"
			case 3:
				res, e := g.Access(o.c, o.d, o.now)
				got.allowed = res.Allowed
				got.err = sentinelName(e)
				if e == nil {
					reason = res.Reason
				} else {
					reason = e.Error()
				}
			case 4:
				got.err = sentinelName(g.Ack(o.c, o.d, o.now))
				reason = "Ack"
			case 5:
				e := g.Advance(o.d, o.now)
				got.err = sentinelName(e)
				got.items = ErrorItems(e)
				if e != nil {
					reason = e.Error()
				} else {
					reason = "advanced one step"
				}
			case 6:
				got.err = sentinelName(g.Extend(o.d, o.c, o.arg, o.now))
				reason = fmt.Sprintf("Extend extra=%d", o.arg)
			}
			if got.err == "" && kind == 0 {
				existing = append(existing, o.d)
			}

			want := m.apply(o)
			fmt.Fprintf(&log, "step %02d %-10s now=%d d=%s c=%s parents=%v arg=%d | got{allow=%v err=%s items=%v} want{allow=%v err=%s items=%v} | %s\n",
				step, kindName(kind), o.now, o.d, o.c, o.parents, o.arg,
				got.allowed, nz(got.err), got.items, want.allowed, nz(want.err), want.items, reason)

			if got.allowed != want.allowed || got.err != want.err {
				t.Fatalf("seq=%d step=%d mismatch\ngot  {allow=%v err=%s}\nwant {allow=%v err=%s}\n%s",
					seq, step, got.allowed, got.err, want.allowed, want.err, log.String())
			}
			if (kind == 5 && (got.err == "Downstream" || got.err == "Consumers")) &&
				!eqStrings(got.items, want.items) {
				t.Fatalf("seq=%d step=%d items mismatch got=%v want=%v\n%s",
					seq, step, got.items, want.items, log.String())
			}
			// 每步结束后对照阶段与计划时刻。
			for _, d := range existing {
				ph, _ := g.Phase(d)
				sun, brown, _ := g.Times(d)
				cnt, sum, _ := g.ExtendInfo(d)
				if int(ph) != m.phase[d] || sun != m.sunset[d] || brown != m.brown[d] ||
					cnt != m.cnt[d] || sum != m.sum[d] {
					t.Fatalf("seq=%d step=%d state mismatch on %s: gate(ph=%d sun=%d brown=%d cnt=%d sum=%d) model(ph=%d sun=%d brown=%d cnt=%d sum=%d)\n%s",
						seq, step, d, ph, sun, brown, cnt, sum,
						m.phase[d], m.sunset[d], m.brown[d], m.cnt[d], m.sum[d], log.String())
				}
			}
		}

		// 序列结束后对每个仍存在的数据集各跑一次 Advance，对照活跃消费者明细。
		finalNow := now + 1
		for _, d := range existing {
			e := g.Advance(d, finalNow)
			gotErr := sentinelName(e)
			want := m.apply(op{kind: 5, now: finalNow, d: d})
			if gotErr != want.err || (gotErr == "Consumers" && !eqStrings(ErrorItems(e), want.items)) ||
				(gotErr == "Downstream" && !eqStrings(ErrorItems(e), want.items)) {
				t.Fatalf("seq=%d final advance %s: got=%s %v want=%s %v\n%s",
					seq, d, gotErr, ErrorItems(e), want.err, want.items, log.String())
			}
		}

		// 抽样打印部分序列日志（每 100 组打印 1 组），覆盖“打印输入/输出/判定依据”。
		if seq%100 == 0 {
			t.Logf("\n%s", log.String())
		}
	}
}

func nz(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func eqStrings(a, b []string) bool {
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
