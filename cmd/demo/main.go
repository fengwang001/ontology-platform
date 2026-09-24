// Command demo runs the budgeted graph traversal acceptance checks.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ontology/audit"
	"ontology/graph"
	"ontology/resume"
	"ontology/walk"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	g := graph.New()
	g.AddEdge("a", "d")
	g.AddEdge("a", "b")
	g.AddEdge("a", "c")
	check("graph 出边字典序", slices.Equal(g.Out("a"), []string{"b", "c", "d"}),
		fmt.Sprintf("Out(a)=%v", g.Out("a")))

	dg := demoGraph()

	base, err := walk.Walk(dg, "a", 4)
	same := err == nil
	for i := 0; i < 20; i++ {
		r, err2 := walk.Walk(dg, "a", 4)
		same = same && err2 == nil && slices.Equal(r.Seq, base.Seq) && bytes.Equal(r.Token, base.Token)
	}
	check("二十次重复遍历逐元素相同", same, fmt.Sprintf("seq=%v", base.Seq))

	tok0, _ := walk.InitialToken(dg, "a")
	z, err := walk.Walk(dg, "a", 0)
	check("预算零幂等", err == nil && len(z.Seq) == 0 && bytes.Equal(z.Token, tok0), "seq 为空且续点等于初始续点")

	full, _ := walk.Walk(dg, "a", 1000)
	again, err := walk.Resume(dg, full.Token, 5)
	check("已完成续点再续传返回空", err == nil && len(again.Seq) == 0, fmt.Sprintf("完整遍历 %d 节点", len(full.Seq)))

	countOK := true
	for b := 0; b <= 8; b++ {
		r, err2 := walk.Walk(dg, "a", b)
		countOK = countOK && err2 == nil && len(r.Seq) == min(b, 6)
	}
	check("访问数==min(预算,可达数)", countOK, "可达=6, 预算 0..8 逐一验证")

	splitErr := audit.CheckSplits(dg, "a", 6)
	check("全部切分点两段续传==一次遍历", splitErr == nil, "切分点 1..5 逐一验证")

	star := graph.New()
	for i := 0; i < 10000; i++ {
		star.AddEdge("hub", fmt.Sprintf("leaf%05d", i))
	}
	sr, _ := walk.Walk(star, "hub", 100)
	check("星形图峰值队列<=4*预算", sr.Stats.PeakQueue <= 400,
		fmt.Sprintf("叶子=10000 预算=100 峰值=%d 上界=400", sr.Stats.PeakQueue))

	tok := resume.Encode(resume.State{FrontCursor: 1, Queue: []string{"aa", "bb"}, Seen: []string{"aa", "bb"}})
	bg := graph.New()
	bg.AddEdge("aa", "bb")
	bg.AddEdge("aa", "cc")
	rejected, bits := 0, len(tok)*8
	for i := 0; i < len(tok); i++ {
		for bit := 0; bit < 8; bit++ {
			bad := slices.Clone(tok)
			bad[i] ^= 1 << bit
			_, err2 := resume.Decode(bg, bad)
			if errors.Is(err2, resume.ErrChecksum) || errors.Is(err2, resume.ErrIncomplete) ||
				errors.Is(err2, resume.ErrUnknownNode) {
				rejected++
			}
		}
	}
	check("续点逐比特翻转全部被拒且分类正确", rejected == bits, fmt.Sprintf("%d/%d 变体被拒", rejected, bits))

	seg, _ := walk.Walk(dg, "a", 2)
	dg.Remove("b")
	_, err = walk.Resume(dg, seg.Token, 3)
	check("续传间删除队列中节点被报错指名",
		errors.Is(err, resume.ErrUnknownNode) && strings.Contains(err.Error(), `"b"`),
		fmt.Sprintf("err=%v", err))

	cyc := graph.New()
	cyc.AddEdge("x", "x")
	cyc.AddEdge("x", "y")
	cyc.AddEdge("y", "x")
	cr, _ := walk.Walk(cyc, "x", 10)
	check("环与自环不重复访问", slices.Equal(cr.Seq, []string{"x", "y"}), fmt.Sprintf("seq=%v", cr.Seq))

	if failed > 0 {
		fmt.Printf("TOTAL FAIL (%d 项未通过)\n", failed)
	} else {
		fmt.Println("TOTAL OK")
	}
}

// demoGraph: a->b,c; b->d; c->d; d->e; e->f. 6 nodes reachable from a.
func demoGraph() *graph.Graph {
	g := graph.New()
	for _, e := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"d", "e"}, {"e", "f"}} {
		g.AddEdge(e[0], e[1])
	}
	return g
}
