package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"
)

func mustCommit(t *testing.T, g *Graph, id string, parents ...string) {
	t.Helper()
	if err := g.Commit(id, parents); err != nil {
		t.Fatalf("Commit(%q,%v) 意外失败: %v", id, parents, err)
	}
}

func expectReason(t *testing.T, err error, want Reason) {
	t.Helper()
	e, ok := AsError(err)
	if !ok {
		t.Fatalf("期望 *ontology.Error，实际 %T: %v", err, err)
	}
	if e.Reason != want {
		t.Fatalf("期望原因 %q，实际 %q", want, e.Reason)
	}
}

// TestCrissCrossMerge 覆盖交叉合并：
// R -> X,Y；M1 父为 X,Y；M2 父为 Y,X。合并基必须恰为 {X,Y}，不含 R。
func TestCrissCrossMerge(t *testing.T) {
	g := NewGraph()
	mustCommit(t, g, "R")
	mustCommit(t, g, "X", "R")
	mustCommit(t, g, "Y", "R")
	mustCommit(t, g, "M1", "X", "Y")
	mustCommit(t, g, "M2", "Y", "X")

	got, err := g.MergeBases("M1", "M2")
	if err != nil {
		t.Fatalf("MergeBases 失败: %v", err)
	}
	want := []string{"X", "Y"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("交叉合并基 = %v，期望 %v（不得含 R）", got, want)
	}
	rev, err := g.MergeBases("M2", "M1")
	if err != nil || fmt.Sprint(rev) != fmt.Sprint(want) {
		t.Fatalf("反向 MergeBases = %v, %v，期望 %v", rev, err, want)
	}
	if gen, _ := g.Gen("M1"); gen != 3 {
		t.Fatalf("Gen(M1) = %d，期望 3", gen)
	}
	if gen, _ := g.Gen("M2"); gen != 3 {
		t.Fatalf("Gen(M2) = %d，期望 3", gen)
	}
}

// TestAncestorAndSelf 覆盖 a 是 b 的祖先与 a == b。
func TestAncestorAndSelf(t *testing.T) {
	g := NewGraph()
	mustCommit(t, g, "R")
	mustCommit(t, g, "C", "R")
	mustCommit(t, g, "D", "C")

	cases := []struct {
		a, b string
		want bool
	}{
		{"R", "D", true},
		{"C", "D", true},
		{"D", "R", false},
		{"R", "C", true},
		{"D", "D", true},
		{"R", "R", true},
	}
	for _, tc := range cases {
		got, err := g.IsAncestor(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Fatalf("IsAncestor(%q,%q) = %v,%v 期望 %v", tc.a, tc.b, got, err, tc.want)
		}
	}
	mb, err := g.MergeBases("R", "D")
	if err != nil || fmt.Sprint(mb) != "[R]" {
		t.Fatalf("MergeBases(R,D) = %v,%v，期望 [R]", mb, err)
	}
	mb, err = g.MergeBases("C", "C")
	if err != nil || fmt.Sprint(mb) != "[C]" {
		t.Fatalf("MergeBases(C,C) = %v,%v，期望 [C]", mb, err)
	}
}

// TestDisconnectedRoots 覆盖两棵互不相连的树：无公共祖先返回空集而非错误。
func TestDisconnectedRoots(t *testing.T) {
	g := NewGraph()
	mustCommit(t, g, "R1")
	mustCommit(t, g, "R2")
	mustCommit(t, g, "A", "R1")
	mustCommit(t, g, "B", "R2")

	got, err := g.MergeBases("R1", "R2")
	if err != nil || len(got) != 0 {
		t.Fatalf("MergeBases(R1,R2) = %v,%v，期望空集且无错误", got, err)
	}
	got, err = g.MergeBases("A", "B")
	if err != nil || len(got) != 0 {
		t.Fatalf("MergeBases(A,B) = %v,%v，期望空集", got, err)
	}
	if ok, _ := g.IsAncestor("R1", "B"); ok {
		t.Fatal("R1 不应是 B 的祖先")
	}
}

// TestOctopusMerge 覆盖三个父的章鱼合并。
func TestOctopusMerge(t *testing.T) {
	g := NewGraph()
	mustCommit(t, g, "R")
	mustCommit(t, g, "P1", "R")
	mustCommit(t, g, "P2", "R")
	mustCommit(t, g, "P3", "R")
	mustCommit(t, g, "O", "P1", "P2", "P3")
	mustCommit(t, g, "Q", "P3")

	if gen, _ := g.Gen("O"); gen != 3 {
		t.Fatalf("Gen(O) = %d，期望 3", gen)
	}
	mb, err := g.MergeBases("O", "Q")
	if err != nil || fmt.Sprint(mb) != "[P3]" {
		t.Fatalf("章鱼合并基 = %v,%v，期望 [P3]", mb, err)
	}
	mb, err = g.MergeBases("O", "R")
	if err != nil || fmt.Sprint(mb) != "[R]" {
		t.Fatalf("MergeBases(O,R) = %v,%v，期望 [R]", mb, err)
	}
}

// TestSameGenerationNoAncestor 覆盖世代号相等但互不同、互不为祖先。
func TestSameGenerationNoAncestor(t *testing.T) {
	g := NewGraph()
	mustCommit(t, g, "R")
	mustCommit(t, g, "X", "R")
	mustCommit(t, g, "Y", "R")

	if ok, err := g.IsAncestor("X", "Y"); err != nil || ok {
		t.Fatalf("IsAncestor(X,Y) = %v,%v，期望 false", ok, err)
	}
	if ok, err := g.IsAncestor("Y", "X"); err != nil || ok {
		t.Fatalf("IsAncestor(Y,X) = %v,%v，期望 false", ok, err)
	}
	mb, err := g.MergeBases("X", "Y")
	if err != nil || fmt.Sprint(mb) != "[R]" {
		t.Fatalf("MergeBases(X,Y) = %v,%v，期望 [R]", mb, err)
	}
}

// TestCommitRejectionOrder 验证严格按
// 空 id → 重复 id → 父过多 → 重复父 → 父未登记 的顺序只报第一个。
func TestCommitRejectionOrder(t *testing.T) {
	g := NewGraph()
	mustCommit(t, g, "R")

	expectReason(t, g.Commit("", []string{"R"}), ReasonEmptyID)
	expectReason(t, g.Commit("R", nil), ReasonDuplicateID)

	nine := []string{"R", "R", "a", "b", "c", "d", "e", "f", "g"}
	expectReason(t, g.Commit("Z1", nine), ReasonTooManyParents)

	err := g.Commit("Z2", []string{"R", "R", "ghost"})
	e, _ := AsError(err)
	if e.Reason != ReasonDuplicateParent || e.ID != "R" || e.Index != 1 {
		t.Fatalf("重复父错误 = reason=%q id=%q index=%d，期望 duplicate parent/R/1", e.Reason, e.ID, e.Index)
	}

	err = g.Commit("Z3", []string{"ghost1", "ghost2"})
	e, _ = AsError(err)
	if e.Reason != ReasonParentNotRegistered || e.ID != "ghost1" || e.Index != 0 {
		t.Fatalf("未登记父错误 = reason=%q id=%q index=%d", e.Reason, e.ID, e.Index)
	}

	// 被拒绝的登记不得改变图：这些 id 随后仍可成功登记。
	mustCommit(t, g, "Z1")
	mustCommit(t, g, "Z2")
	mustCommit(t, g, "Z3")
	if gen, _ := g.Gen("Z3"); gen != 1 {
		t.Fatalf("拒绝后登记的 Z3 世代 = %d，期望 1（根）", gen)
	}
	expectReason(t, g.Commit("", nil), ReasonEmptyID)
}

// TestUnknownIDReasons 验证查询接口对未登记 id 返回可区分原因，且 a 先于 b。
func TestUnknownIDReasons(t *testing.T) {
	g := NewGraph()
	mustCommit(t, g, "R")

	_, err := g.Gen("nope")
	expectReason(t, err, ReasonGenUnknownID)

	_, err = g.IsAncestor("nope", "nope2")
	expectReason(t, err, ReasonIsAncestorUnknownA)
	_, err = g.IsAncestor("R", "nope")
	expectReason(t, err, ReasonIsAncestorUnknownB)

	_, err = g.MergeBases("nope", "nope2")
	expectReason(t, err, ReasonMergeBasesUnknownA)
	_, err = g.MergeBases("R", "nope")
	expectReason(t, err, ReasonMergeBasesUnknownB)
}

// TestGenerationPruning 用非导出计数器证明：N 个提交的线性主干末端分出
// 两条各 10 个提交的分支时，求两端合并基实际遍历的提交数在
// N=1000 与 N=100000 下完全相同，与历史总长度无关。
func TestGenerationPruning(t *testing.T) {
	build := func(n int) (*Graph, string, string) {
		g := NewGraph()
		mustCommit(t, g, "c0")
		prev := "c0"
		for i := 1; i < n; i++ {
			id := fmt.Sprintf("c%d", i)
			mustCommit(t, g, id, prev)
			prev = id
		}
		aTip, bTip := prev, prev
		for i := 0; i < 10; i++ {
			id := fmt.Sprintf("a%d", i)
			mustCommit(t, g, id, aTip)
			aTip = id
		}
		for i := 0; i < 10; i++ {
			id := fmt.Sprintf("b%d", i)
			mustCommit(t, g, id, bTip)
			bTip = id
		}
		return g, aTip, bTip
	}

	g1, a1, b1 := build(1000)
	mb1, err := g1.MergeBases(a1, b1)
	if err != nil || len(mb1) != 1 || mb1[0] != "c999" {
		t.Fatalf("N=1000 合并基 = %v,%v，期望 [c999]", mb1, err)
	}
	v1 := g1.lastMergeBaseVisits()

	g2, a2, b2 := build(100000)
	mb2, err := g2.MergeBases(a2, b2)
	if err != nil || len(mb2) != 1 || mb2[0] != "c99999" {
		t.Fatalf("N=100000 合并基 = %v,%v，期望 [c99999]", mb2, err)
	}
	v2 := g2.lastMergeBaseVisits()

	t.Logf("剪枝计数器：N=1000 遍历 %d 个提交，N=100000 遍历 %d 个提交", v1, v2)
	if v1 != v2 {
		t.Fatalf("遍历规模依赖历史长度：%d != %d", v1, v2)
	}
	if v1 != 21 {
		t.Fatalf("遍历数 = %d，期望恰为 21（10+10 分支提交 + 1 个分叉点）", v1)
	}
}

// TestConcurrentAndReplay 验证并发登记/查询可串行化，且重放同一序列结果一致。
func TestConcurrentAndReplay(t *testing.T) {
	// 1) 串行构造一份登记序列（仅数据准备，不涉及图并发）。
	rng := rand.New(rand.NewSource(42))
	type rec struct {
		id      string
		parents []string
	}
	var seq []rec
	var order []string
	seq = append(seq, rec{id: "R"})
	order = append(order, "R")
	for i := 1; i < 200; i++ {
		id := fmt.Sprintf("n%d", i)
		k := rng.Intn(3)
		if k > i {
			k = i
		}
		picked := map[int]bool{}
		for len(picked) < k {
			picked[rng.Intn(i)] = true
		}
		var pl []string
		for idx := range picked {
			pl = append(pl, order[idx])
		}
		sort.Strings(pl)
		seq = append(seq, rec{id: id, parents: pl})
		order = append(order, id)
	}

	// 2) 串行参考图：重放同一序列。
	serial := NewGraph()
	for _, r := range seq {
		mustCommit(t, serial, r.id, r.parents...)
	}
	serialGens := make(map[string]int, len(seq))
	for _, r := range seq {
		gen, err := serial.Gen(r.id)
		if err != nil {
			t.Fatalf("串行 Gen(%s): %v", r.id, err)
		}
		serialGens[r.id] = gen
	}

	// 3) 并发图：按串行参考的世代号分层，同世代提交的父全部来自更早世代，
	// 因此同层可以并发登记而不违反“父先于子”；层间等待，保证任一成功读到
	// 的世代号都等于串行结果（可线性化）。登记期间并发跑只读 Gen 查询。
	concurrent := NewGraph()
	var layers [][]rec
	layerOf := map[string]int{}
	for _, r := range seq {
		layer := 0
		for _, p := range r.parents {
			if layerOf[p]+1 > layer {
				layer = layerOf[p] + 1
			}
		}
		layerOf[r.id] = layer
		for len(layers) <= layer {
			layers = append(layers, nil)
		}
		layers[layer] = append(layers[layer], r)
	}

	for layer, batch := range layers {
		queryStop := make(chan struct{})
		var queryWg sync.WaitGroup
		for w := 0; w < 4; w++ {
			localRng := rand.New(rand.NewSource(int64(700 + layer*10 + w)))
			queryWg.Add(1)
			go func() {
				defer queryWg.Done()
				for {
					select {
					case <-queryStop:
						return
					default:
						id := order[localRng.Intn(len(order))]
						if gen, err := concurrent.Gen(id); err == nil && gen != serialGens[id] {
							t.Errorf("并发期 Gen(%s) = %d，期望 %d", id, gen, serialGens[id])
							return
						}
						time.Sleep(time.Microsecond)
					}
				}
			}()
		}

		var regWg sync.WaitGroup
		var errMu sync.Mutex
		var regErr error
		for _, r := range batch {
			regWg.Add(1)
			go func(r rec) {
				defer regWg.Done()
				if err := concurrent.Commit(r.id, r.parents); err != nil {
					errMu.Lock()
					if regErr == nil {
						regErr = err
					}
					errMu.Unlock()
				}
			}(r)
		}
		regWg.Wait()
		close(queryStop)
		queryWg.Wait()
		if regErr != nil {
			t.Fatalf("第 %d 层并发登记失败: %v", layer, regErr)
		}
	}

	// 4) 全部登记完成：世代号与逐对 MergeBases 与串行重放完全一致。
	for _, r := range seq {
		gen, err := concurrent.Gen(r.id)
		if err != nil || gen != serialGens[r.id] {
			t.Fatalf("最终 Gen(%s) = %d,%v，期望 %d", r.id, gen, err, serialGens[r.id])
		}
	}
	checkRng := rand.New(rand.NewSource(99))
	for i := 0; i < 300; i++ {
		x := order[checkRng.Intn(len(order))]
		y := order[checkRng.Intn(len(order))]
		got, err := concurrent.MergeBases(x, y)
		if err != nil {
			t.Fatalf("最终 MergeBases(%s,%s): %v", x, y, err)
		}
		want, err := serial.MergeBases(x, y)
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("重放 MergeBases(%s,%s) = %v，串行参考 %v,%v", x, y, got, want, err)
		}
	}
}
