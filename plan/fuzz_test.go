package plan_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"ontology/diff"
	"ontology/norm"
	"ontology/plan"
)

type fuzzRow struct {
	id   int64
	hasD bool
	d    int64
	hasC bool
	c    []byte
}

func randomRow(rng *rand.Rand, id int64, sd int) fuzzRow {
	fr := fuzzRow{id: id}
	switch rng.Intn(3) {
	case 0:
		fr.hasD = false // NULL
	case 1:
		fr.hasD = true
		// 侧重产生恰半值：让尾数末 (6-sd) 位落在 D/2 附近
		D := int64(1)
		for i := 0; i < 6-sd; i++ {
			D *= 10
		}
		q := rng.Int63n(200) - 100
		rem := []int64{0, D / 2, D/2 - 1, D/2 + 1, rng.Int63n(D)}[rng.Intn(5)]
		fr.d = q*D + rem
		if fr.d > norm.MaxMant {
			fr.d = norm.MaxMant
		}
		if fr.d < -norm.MaxMant {
			fr.d = -norm.MaxMant
		}
	case 2:
		fr.hasD = true
		fr.d = rng.Int63n(2_000_001) - 1_000_000
	}
	switch rng.Intn(4) {
	case 0:
		fr.hasC = false // NULL
	case 1:
		fr.hasC = true
		fr.c = []byte{} // 空串
	default:
		fr.hasC = true
		n := rng.Intn(5)
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = []byte{'a', 'b', 0x20, 0x09, 'x'}[rng.Intn(5)]
		}
		// 目标端可能有右侧补位空格
		fr.c = append(buf, spaces(rng.Intn(3))...)
	}
	return fr
}

func spaces(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 0x20
	}
	return b
}

func toNormRow(fr fuzzRow) norm.Row {
	r := norm.Row{ID: fr.id}
	if fr.hasD {
		v := fr.d
		r.D = &v
	}
	if fr.hasC {
		r.C = make([]byte, len(fr.c))
		copy(r.C, fr.c)
	}
	return r
}

func putOnSim(s *naiveSim, fr fuzzRow, side bool) {
	if side {
		s.srcPut(fr.id, fr.hasD, fr.d, fr.hasC, fr.c)
	} else {
		s.tgtPut(fr.id, fr.hasD, fr.d, fr.hasC, fr.c)
	}
}

func compareResultsEq(a []diff.ResultRow, b []simResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		mask := uint8(0)
		if b[i].dDiff {
			mask |= diff.ColD
		}
		if b[i].cDiff {
			mask |= diff.ColC
		}
		if a[i].ID != b[i].id || a[i].Class != b[i].cls || a[i].Changed != mask {
			return false
		}
	}
	return true
}

func TestFuzzAgainstNaive(t *testing.T) {
	const N = 2000
	rng := rand.New(rand.NewSource(20261003))
	trace := &strings.Builder{}
	t.Cleanup(func() {
		if err := os.WriteFile("fuzz_trace.log", []byte(trace.String()), 0o644); err != nil {
			t.Logf("写 fuzz_trace.log 失败: %v", err)
			return
		}
		t.Logf("完整输入/输出/判定日志已写入 plan/fuzz_trace.log（%d 组）", N)
	})
	for it := 0; it < N; it++ {
		sd := rng.Intn(7)
		rm := rng.Intn(3)
		ne := rng.Intn(2) == 1
		cfg, _ := norm.NewCfg(sd, rm, ne)
		d := diff.New(cfg)
		p := plan.New(d, cfg)
		sim := newNaive(sd, rm, ne)

		log := []string{fmt.Sprintf("=== iter=%d sd=%d rm=%d ne=%v ===", it, sd, rm, ne)}
		summary := fmt.Sprintf("iter=%d sd=%d rm=%d ne=%v", it, sd, rm, ne)

		idPool := []int64{1, 2, 3, 5, 8, 13, 100, 999, norm.MaxID}
		// 初始填充
		for _, id := range idPool {
			if rng.Intn(2) == 0 {
				fr := randomRow(rng, id, sd)
				_ = d.SrcPut(toNormRow(fr))
				putOnSim(sim, fr, true)
				log = append(log, fmt.Sprintf("SrcPut id=%d dNil=%v d=%d cNil=%v c=%q",
					id, !fr.hasD, fr.d, !fr.hasC, fr.c))
			}
			if rng.Intn(2) == 0 {
				fr := randomRow(rng, id, sd)
				_ = d.TgtPut(toNormRow(fr))
				putOnSim(sim, fr, false)
				log = append(log, fmt.Sprintf("TgtPut id=%d dNil=%v d=%d cNil=%v c=%q",
					id, !fr.hasD, fr.d, !fr.hasC, fr.c))
			}
		}

		lo := int64(1)
		hi := int64(norm.MaxID + 1)
		steps := 4 + rng.Intn(6)
		for step := 0; step < steps; step++ {
			switch rng.Intn(5) {
			case 0, 1: // Compare 对拍
				got, err := p.Compare(lo, hi)
				if err != nil {
					t.Fatalf("iter=%d Compare err=%v\n%s", it, err, joinLog(log))
				}
				want := sim.compare(lo, hi)
				if !compareResultsEq(got, want) {
					t.Fatalf("iter=%d Compare mismatch\ngot=%v\nwant=%v\n%s",
						it, dumpRows(got), dumpSim(want), joinLog(log))
				}
				line := fmt.Sprintf("Compare[1,1e9+1) => %s 判定：两侧归一后按id归并一致", dumpRows(got))
				log = append(log, line)
				summary += " | " + line
			case 2, 3: // Make + Apply 对拍
				del := rng.Intn(2) == 1
				role := []int{1, 2, 0, 3}[rng.Intn(4)]
				pl, err := p.Make(lo, hi, del)
				if err != nil {
					t.Fatalf("iter=%d Make err=%v\n%s", it, err, joinLog(log))
				}
				simOps := sim.makePlan(lo, hi, del)
				if pl.Len() != len(simOps) || pl.HasDelete() != containsDel(simOps) {
					t.Fatalf("iter=%d plan shape mismatch: %d vs %d\n%s",
						it, pl.Len(), len(simOps), joinLog(log))
				}
				if !opsMatch(pl.Ops(), simOps) {
					t.Fatalf("iter=%d plan content mismatch\nreal=%s\nsim=%s\n%s",
						it, dumpOps(pl.Ops()), dumpSimOps(simOps), joinLog(log))
				}
				// 计划生成后随机制造并发干扰
				disturb := rng.Intn(3)
				bumped := int64(-1)
				if len(simOps) > 0 && disturb != 0 {
					victim := simOps[rng.Intn(len(simOps))].id
					if disturb == 1 {
						fr := randomRow(rng, victim, sd)
						_ = d.TgtPut(toNormRow(fr))
						putOnSim(sim, fr, false)
					} else {
						_ = d.TgtDel(victim)
						sim.tgtDel(victim)
					}
					bumped = victim
					log = append(log, fmt.Sprintf("!! Apply 前干扰 id=%d 类型=%d(1写2删)", victim, disturb))
				}
				ni, nu, nd, aerr := p.Apply(role, pl)
				sni, snu, snd, staleID, ok, permDen := sim.apply(role, simOps)
				line := fmt.Sprintf(
					"Plan(del=%v,len=%d) Apply(role=%d, 干扰id=%d) => (%d,%d,%d) err=%v；模拟 (%d,%d,%d) ok=%v perm=%v stale=%d 判定：%s",
					del, pl.Len(), role, bumped, ni, nu, nd, aerr,
					sni, snu, snd, ok, permDen, staleID,
					applyReason(aerr, ok, permDen, staleID, ni, nu, nd, sni, snu, snd))
				log = append(log, line)
				summary += " | " + line
				if permDen {
					if aerr == nil || !errorIs(aerr, plan.ErrPermission) {
						t.Fatalf("iter=%d want permission denied, got %v\n%s", it, aerr, joinLog(log))
					}
				} else if !ok {
					var se *plan.ErrStale
					if !errors.As(aerr, &se) || se.ID != staleID {
						t.Fatalf("iter=%d want ErrStale(%d), got %v\n%s", it, staleID, aerr, joinLog(log))
					}
				} else {
					if aerr != nil || ni != sni || nu != snu || nd != snd {
						t.Fatalf("iter=%d apply result mismatch (%d,%d,%d,%v) vs sim (%d,%d,%d)\n%s",
							it, ni, nu, nd, aerr, sni, snu, snd, joinLog(log))
					}
				}
				// 每次 Apply 后两侧应满足：无 Missing/Changed；del 且成功且管理员时无 Extra
				got, _ := d.Compare(lo, hi)
				want := sim.compare(lo, hi)
				if !compareResultsEq(got, want) {
					t.Fatalf("iter=%d post-apply Compare mismatch\nreal=%s\nsim=%s\n%s",
						it, dumpRows(got), dumpSim(want), joinLog(log))
				}
				if aerr == nil {
					for _, r := range got {
						if r.Class == diff.ClsMissing || r.Class == diff.ClsChanged {
							t.Fatalf("iter=%d not converged after successful apply: %v\n%s",
								it, r, joinLog(log))
						}
						if del && role == plan.RoleAdmin && r.Class == diff.ClsExtra {
							t.Fatalf("iter=%d extra remains after delete-apply: %v\n%s",
								it, r, joinLog(log))
						}
					}
				}
			case 4: // 直接写/删，对拍版本
				id := idPool[rng.Intn(len(idPool))]
				if rng.Intn(3) == 0 {
					_ = d.TgtDel(id)
					sim.tgtDel(id)
					log = append(log, fmt.Sprintf("TgtDel id=%d", id))
				} else {
					fr := randomRow(rng, id, sd)
					err := d.TgtPut(toNormRow(fr))
					if err != nil {
						t.Fatalf("iter=%d TgtPut err: %v", it, err)
					}
					putOnSim(sim, fr, false)
					log = append(log, fmt.Sprintf("TgtPut id=%d dNil=%v d=%d cNil=%v c=%q",
						id, !fr.hasD, fr.d, !fr.hasC, fr.c))
				}
				rv, rex := d.Version(id)
				sv, sex := sim.ver[id], false
				if _, present := sim.tgt[id]; present {
					sex = true
				}
				if rv != sv || rex != sex {
					t.Fatalf("iter=%d version id=%d: real(%d,%v) sim(%d,%v)\n%s",
						it, id, rv, rex, sv, sex, joinLog(log))
				}
			}
		}

		fmt.Fprintln(trace, joinLog(log))
		t.Log(summary)
	}
}
