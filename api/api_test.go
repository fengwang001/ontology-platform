package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// TestEightStep 钉住第三节八步序列的返回值（NOTES.md 分步表）。
func TestEightStep(t *testing.T) {
	e := api.New()
	mustW := func(g, k string, v int64) {
		if err := e.Write(g, k, v); err != nil {
			t.Fatal(err)
		}
	}
	mustW("g0", "a", 5)
	g2, _ := e.ReadG("g0")
	mustW("g0", "b", 3)
	g4, _ := e.ReadG("g0")
	mustW("g1", "c", 7)
	t6 := e.ReadTotal()
	mustW("g0", "b", 10)
	t8 := e.ReadTotal()
	if g2 != 5 || g4 != 8 || t6 != 15 || t8 != 22 {
		t.Fatalf("got (%d,%d,%d,%d), want (5,8,15,22)", g2, g4, t6, t8)
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestViewMatchesBatch 钉住不变量 1：任意写序列后 View 等于批量重算。
func TestViewMatchesBatch(t *testing.T) {
	for _, tc := range []struct{ seed, ops, groups, keys int64 }{
		{1, 200, 3, 20}, {2, 1000, 5, 60}, {3, 5000, 8, 200},
	} {
		e := api.New()
		seed := tc.seed
		rnd := func(n int64) int64 { seed = (seed*6364136223846793005 + 1442695040888963407) >> 33; return seed % n }
		last, model := map[string]int64{}, map[string]int64{}
		var tot int64
		for i := int64(0); i < tc.ops; i++ {
			g, k, v := fmt.Sprintf("g%d", rnd(tc.groups)), fmt.Sprintf("k%d", rnd(tc.keys)), rnd(1000)
			if e.Write(g, k, v) != nil {
				continue // 组冲突：模型同样拒绝
			}
			model[g], tot, last[k] = model[g]+v-last[k], tot+v-last[k], v
		}
		gv, gt := e.View()
		ok := gt == tot && len(gv) == len(model)
		for g, s := range model {
			ok = ok && gv[g] == s
		}
		if !ok {
			t.Fatalf("seed %d: View=%v/%d, want %v/%d", tc.seed, gv, gt, model, tot)
		}
	}
}

// TestRejectLeavesState 钉住不变量 4：三类拒绝可区分且不留痕。
func TestRejectLeavesState(t *testing.T) {
	e := api.New()
	if err := e.Write("g0", "a", 5); err != nil {
		t.Fatal(err)
	}
	bg, bt := e.View()
	for _, tc := range []struct {
		g, k string
		want error
	}{{"", "x", api.ErrEmptyGroup}, {"g0", "", api.ErrEmptyKey}, {"g1", "a", api.ErrGroupConflict}} {
		err := e.Write(tc.g, tc.k, 9)
		matched := 0
		for _, o := range []error{api.ErrEmptyGroup, api.ErrEmptyKey, api.ErrGroupConflict} {
			if errors.Is(err, o) {
				matched++
			}
		}
		if !errors.Is(err, tc.want) || matched != 1 {
			t.Fatalf("write(%q,%q): err=%v matched=%d, want only %v", tc.g, tc.k, err, matched, tc.want)
		}
		ag, at := e.View()
		if at != bt || len(ag) != len(bg) || ag["g0"] != bg["g0"] {
			t.Fatalf("write(%q,%q): state changed after reject", tc.g, tc.k)
		}
	}
	if _, err := e.ReadG(""); !errors.Is(err, api.ErrEmptyGroup) {
		t.Fatalf("ReadG empty group: %v", err)
	}
}

// viewSum 返回 (各组之和的合计, View 报告的总和)，供一致性比对。
func viewSum(e *api.Engine) (int64, int64) {
	gv, gt := e.View()
	var sum int64
	for _, s := range gv {
		sum += s
	}
	return sum, gt
}

// TestConcurrentViewConsistent 钉住第六节：写读交错后并发只读的 View 逐字段相同且与批量重算一致；不用 sleep。
func TestConcurrentViewConsistent(t *testing.T) {
	e := api.New()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 250; i++ {
				_ = e.Write(fmt.Sprintf("g%d", w), fmt.Sprintf("k%d", i), int64(w*1000+i))
			}
		}(w)
	}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 250; i++ {
				if sum, gt := viewSum(e); sum != gt {
					t.Errorf("groups sum %d != total %d", sum, gt)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	wantG, wantT := e.View() // 静默后 32 个并发读必须逐字段相同
	views, tots := make([]map[string]int64, 32), make([]int64, 32)
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); views[i], tots[i] = e.View() }(i)
	}
	wg.Wait()
	for i := range views {
		eq := tots[i] == wantT && len(views[i]) == len(wantG)
		for g, s := range wantG {
			eq = eq && views[i][g] == s
		}
		if !eq {
			t.Fatalf("reader %d: view differs", i)
		}
	}
}
