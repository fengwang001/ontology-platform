package lifecycle_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/lifecycle"
	"ontology/lifecycle/naive"
)

func (w *diffWorld) verifyEquiv(t *testing.T, tag string) {
	t.Helper()
	for _, id := range w.ids {
		want, ok1 := w.nav.State(naive.InstanceID(id))
		got, ok2 := w.eng.GetStateForTest(lifecycle.InstanceID(id))
		if ok1 != ok2 || lifecycle.State(want) != got {
			t.Fatalf("[%s] state mismatch on %s: engine=%s(%v) naive=%s(%v)",
				tag, id, got, ok2, want, ok1)
		}
		wv, _ := w.nav.Attr(naive.InstanceID(id), "v")
		gv := w.eng.GetAttrForTest(lifecycle.InstanceID(id), "v")
		if fmt.Sprint(wv) != fmt.Sprint(gv) {
			t.Fatalf("[%s] attr mismatch on %s: engine=%v naive=%v", tag, id, gv, wv)
		}
		for _, b := range w.ids {
			wantL := w.nav.Linked(naive.InstanceID(id), "edge", naive.InstanceID(b))
			gotL := w.eng.LinkedForTest(
				lifecycle.InstanceID(id), "edge", lifecycle.InstanceID(b))
			if wantL != gotL {
				t.Fatalf("[%s] link mismatch %s->%s: engine=%v naive=%v",
					tag, id, b, gotL, wantL)
			}
		}
	}
}

// TestDifferentialRandomized 随机生成配置、链式结构与请求序列，逐批对拍
// 正式引擎与独立朴素模型；日志打印每次迁移的输入、判定依据与最终结果。
func TestDifferentialRandomized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping randomized differential test in -short mode")
	}
	rng := rand.New(rand.NewSource(20261007))
	var lastLog string
	for iter := 0; iter < 120; iter++ {
		cfg := randomConfig(rng)
		w := buildDiffWorld(t, rng, cfg)
		w.nav.Log = func(line string) { w.logB.WriteString(line + "\n") }

		for step := 0; step < 30; step++ {
			batchN := 1 + rng.Intn(3)
			ereqs := []lifecycle.TransitionRequest{}
			nreqs := []naive.Request{}
			chosen := map[string]bool{}
			for k := 0; k < batchN; k++ {
				id := w.roots[rng.Intn(len(w.roots))]
				if chosen[id] {
					continue
				}
				chosen[id] = true
				rule, ok := w.ruleFor(id)
				if !ok {
					continue
				}
				var aops []lifecycle.AttrOp
				var naops []naive.AttrOp
				if rng.Intn(3) == 0 {
					aops = append(aops, lifecycle.AttrOp{
						Key: "v", Op: lifecycle.AttrAdd, Value: int64(1)})
					naops = append(naops, naive.AttrOp{
						Key: "v", Op: "add", Value: int64(1)})
				}
				pri := rng.Intn(3)
				ereqs = append(ereqs, lifecycle.TransitionRequest{
					Instance: lifecycle.InstanceID(id), Rule: rule,
					Priority: pri, Attrs: aops,
				})
				nreqs = append(nreqs, naive.Request{
					Instance: naive.InstanceID(id), Rule: rule,
					Priority: pri, Attrs: naops,
				})
			}
			if len(ereqs) == 0 {
				continue
			}

			eouts := w.eng.Execute(ereqs...)
			nouts := w.nav.Execute(nreqs...)
			for k := range ereqs {
				ecode := 0
				if eouts[k].Err != nil {
					ecode = int(eouts[k].Err.Code)
				}
				if ecode != nouts[k].Code {
					t.Fatalf("iter=%d step=%d req#%d %s/%s: engine code=%d naive code=%d\nLOG:\n%s",
						iter, step, k, ereqs[k].Instance, ereqs[k].Rule,
						ecode, nouts[k].Code, tailLog(w.logB.String()))
				}
			}
			w.verifyEquiv(t, fmt.Sprintf("iter=%d step=%d", iter, step))
		}
		lastLog = w.logB.String()
	}
	if n := len(tailLog(lastLog)); n == 0 {
		t.Fatal("expected decision log output")
	}
	t.Logf("differential runs ok; sample log:\n%s", tailLog(lastLog))
}

// TestDifferentialSampleLog 在小规模固定场景下断言日志包含输入、判定依据
// 与 ACCEPT/REJECT 结果。
func TestDifferentialSampleLog(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	cfg := randomConfig(rng)
	w := buildDiffWorld(t, rng, cfg)
	for step := 0; step < 10; step++ {
		id := w.ids[rng.Intn(len(w.ids))]
		rule, ok := w.ruleFor(id)
		if !ok {
			continue
		}
		w.eng.Execute(lifecycle.TransitionRequest{
			Instance: lifecycle.InstanceID(id), Rule: rule,
			Attrs: []lifecycle.AttrOp{{Key: "v", Op: lifecycle.AttrAdd, Value: int64(1)}},
		})
	}
	log := w.logB.String()
	for _, want := range []string{"target=", "rule=", "=> ACCEPT"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, tailLog(log))
		}
	}
}
