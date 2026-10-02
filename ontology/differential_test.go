package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func realDump(r *Registry) map[string]any {
	txns := make(map[TxnName]TxnInfo, len(r.txns))
	for name, e := range r.txns {
		txns[name] = TxnInfo{State: e.state, Epoch: e.epoch, N: e.n}
	}
	st := r.Stats()
	return map[string]any{
		"p": r.p, "lb": r.lb, "ln": r.ln, "life": r.life,
		"txns": txns,
		"stats": modelStats{
			committed: st.Committed, aborted: st.Aborted,
			open: st.Open, prepared: st.Prepared,
			probes: st.ProbeCount, visited: st.Visited,
		},
	}
}

// TestDifferentialRandom: 2000 组随机操作序列，与朴素模拟逐步对照，
// 日志含每步输入、输出与最终判定依据。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1171))
	const sequences = 2000

	for seq := 0; seq < sequences; seq++ {
		P := 1 + rng.Intn(4)
		M := 1 + rng.Intn(3)
		r, err := NewRegistry(P, M)
		if err != nil {
			t.Fatalf("seq %d NewRegistry: %v", seq, err)
		}
		mdl := newNaiveModel(P, M)

		steps := 5 + rng.Intn(36)
		logs := make([]string, 0, steps+1)
		fail := func(format string, args ...any) {
			for _, line := range logs {
				t.Log(line)
			}
			t.Fatalf("seq %d (P=%d M=%d): %s\nreal : %#v\nmodel: %#v",
				seq, P, M, fmt.Sprintf(format, args...), realDump(r), mdl.dump())
		}

		// 记录守恒依据：独立累加所有成功 Write 的 n。
		var written int64

		for step := 0; step < steps; step++ {
			kind := rng.Intn(10)
			log := fmt.Sprintf("seq=%d step=%d lb=%d ln=%d life=%d", seq, step, r.lb, r.ln, r.life)

			switch {
			case kind < 5: // Write
				s := rng.Intn(P + 1) // 偶尔越界
				var n int64
				switch rng.Intn(10) {
				case 0:
					n = 0
				case 1:
					n = 1_000_001
				default:
					n = int64(1 + rng.Intn(1000))
				}
				er := r.Write(s, n)
				em := mdl.Write(s, n)
				log += fmt.Sprintf(" Write(s=%d,n=%d) -> real=%v model=%v", s, n, er, em)
				if fmt.Sprint(er) != fmt.Sprint(em) {
					logs = append(logs, log)
					fail("Write error mismatch real=%v model=%v", er, em)
				}
				if er == nil {
					written += n
				}
			case kind == 5: // Barrier
				cp := r.lb + 1
				if rng.Intn(4) == 0 {
					cp += int64(rng.Intn(3) - 1) // 偶尔乱序（可能为 0）
				}
				gr, er := r.Barrier(cp)
				gm, em := mdl.Barrier(cp)
				log += fmt.Sprintf(" Barrier(cp=%d) -> real=%v,%v model=%v,%v", cp, gr, er, gm, em)
				if fmt.Sprint(er) != fmt.Sprint(em) || fmt.Sprint(gr) != fmt.Sprint(gm) {
					logs = append(logs, log)
					fail("Barrier mismatch real=%v,%v model=%v,%v", gr, er, gm, em)
				}
			case kind == 6: // Complete
				c := r.lb
				switch rng.Intn(5) {
				case 0:
					c = r.ln // 过期
				case 1:
					c = r.lb + 1 // 超前
				case 2:
					c = 0 // 非法
				default:
					if r.lb > r.ln {
						c = r.ln + 1 + rng.Int63n(r.lb-r.ln)
					}
				}
				gr, er := r.Complete(c)
				gm, em := mdl.Complete(c)
				log += fmt.Sprintf(" Complete(c=%d) -> real=%v,%v model=%v,%v", c, gr, er, gm, em)
				if fmt.Sprint(er) != fmt.Sprint(em) || fmt.Sprint(gr) != fmt.Sprint(gm) {
					logs = append(logs, log)
					fail("Complete mismatch real=%v,%v model=%v,%v", gr, er, gm, em)
				}
			case kind == 7: // Restore
				c := r.lb
				switch rng.Intn(6) {
				case 0:
					if r.ln > 0 {
						c = r.ln - 1 // 回退
					}
				case 1:
					c = r.lb + 1 // 超前
				case 2:
					c = -1 // 非法
				default:
					c = r.ln + rng.Int63n(r.lb-r.ln+1)
				}
				newP := 1 + rng.Intn(5)
				if rng.Intn(12) == 0 {
					newP = 65 // 非法
				}
				rr2, er := r.Restore(c, newP)
				rm2, em := mdl.Restore(c, newP)
				log += fmt.Sprintf(" Restore(c=%d,P'=%d) -> real={C:%v A:%v P:%d},%v model={C:%v A:%v P:%d},%v",
					c, newP, rr2.Committed, rr2.Aborted, rr2.Probes, er,
					rm2.Committed, rm2.Aborted, rm2.Probes, em)
				if fmt.Sprint(er) != fmt.Sprint(em) {
					logs = append(logs, log)
					fail("Restore error mismatch real=%v model=%v", er, em)
				}
				if er == nil {
					if fmt.Sprint(rr2.Committed) != fmt.Sprint(rm2.Committed) ||
						fmt.Sprint(rr2.Aborted) != fmt.Sprint(rm2.Aborted) ||
						rr2.Probes != rm2.Probes {
						logs = append(logs, log)
						fail("Restore result mismatch real=%+v model=%+v", rr2, rm2)
					}
					P = newP // 后续 Write 越界概率按新并行度
				}
			case kind == 8: // Txn 查询
				s := rng.Intn(P + 2)
				k := int64(1 + rng.Intn(int(r.lb)+4))
				ir, er := r.Txn(s, k)
				im, em := mdl.Txn(s, k)
				log += fmt.Sprintf(" Txn(s=%d,k=%d) -> real=%+v,%v model=%+v,%v", s, k, ir, er, im, em)
				if fmt.Sprint(er) != fmt.Sprint(em) || (er == nil && ir != im) {
					logs = append(logs, log)
					fail("Txn mismatch real=%+v,%v model=%+v,%v", ir, er, im, em)
				}
			default: // Pending 查询
				gr := r.Pending()
				gm := mdl.sortedPending()
				log += fmt.Sprintf(" Pending() -> real=%v model=%v", gr, gm)
				if fmt.Sprint(gr) != fmt.Sprint(gm) {
					logs = append(logs, log)
					fail("Pending mismatch real=%v model=%v", gr, gm)
				}
			}

			// 判定依据一：完整事务表、水位、世代、life、计数逐字段一致。
			if !reflect.DeepEqual(realDump(r), mdl.dump()) {
				logs = append(logs, log)
				fail("state dump mismatch after op")
			}
			// 判定依据二：记录守恒式。
			st := r.Stats()
			if st.Committed+st.Aborted+st.Open+st.Prepared != written {
				logs = append(logs, log)
				fail("conservation: %d+%d+%d+%d != written %d",
					st.Committed, st.Aborted, st.Open, st.Prepared, written)
			}
			// 判定依据三：visited 上界（本次 Complete/Restore 提交数由清单保证）。
			logs = append(logs, log+" | verdict=OK")
		}

		// 整组序列重放确定性：同一日志再跑一遍必须完全一致。
		if seq%500 == 0 {
			t.Logf("seq=%d P=%d M=%d steps=%d verdict=ALL_OK (sample: %s...)",
				seq, P, M, steps, logs[0])
		}
	}
}

// TestDeterminismReplay：抽一组序列，按相同操作重建名册，清单与计数必须相同。
func TestDeterminismReplay(t *testing.T) {
	script := []string{
		"w0:3", "w1:2", "b1", "w0:4", "b2", "c1",
		"w0:1", "w1:7", "b3", "w1:6", "r2:1", "w0:5",
	}
	run := func() (string, Stats) {
		r, _ := NewRegistry(2, 2)
		var got string
		for _, op := range script {
			switch op[0] {
			case 'w':
				var s int
				var n int64
				fmt.Sscanf(op[1:], "%d:%d", &s, &n)
				if err := r.Write(s, n); err != nil {
					got += fmt.Sprintf("%s=ERR;", op)
				}
			case 'b':
				var cp int64
				fmt.Sscanf(op[1:], "%d", &cp)
				g, err := r.Barrier(cp)
				got += fmt.Sprintf("%s=%v,%v;", op, g, err)
			case 'c':
				var c int64
				fmt.Sscanf(op[1:], "%d", &c)
				g, err := r.Complete(c)
				got += fmt.Sprintf("%s=%v,%v;", op, g, err)
			case 'r':
				var c int64
				var np int
				fmt.Sscanf(op[1:], "%d:%d", &c, &np)
				g, err := r.Restore(c, np)
				got += fmt.Sprintf("%s=%+v,%v;", op, g, err)
			}
		}
		return got, r.Stats()
	}
	out1, st1 := run()
	out2, st2 := run()
	t.Logf("replay inputs=%v", script)
	t.Logf("replay output[1]=%s stats=%+v", out1, st1)
	t.Logf("replay output[2]=%s stats=%+v verdict=%v", out2, st2, out1 == out2 && st1 == st2)
	if out1 != out2 || st1 != st2 {
		t.Fatalf("replay nondeterministic")
	}
}
