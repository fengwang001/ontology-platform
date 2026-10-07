// Command server 演示受权限约束的图遍历环检测服务：
// 构建一张带权限标签的对象图，用两个权限不同的调用方分别遍历，
// 并打印各自的可见结果与判定日志。
package main

import (
	"fmt"
	"log"

	"ontology/ontology"
)

func main() {
	store := ontology.NewStore()
	links := []ontology.Link{
		{ID: "owns", From: "alice", To: "project-x", Label: "public"},
		{ID: "uses", From: "project-x", To: "dataset-y", Label: "public"},
		{ID: "derived-from", From: "dataset-y", To: "alice", Label: "restricted"}, // 仅借此闭合的环路
		{ID: "audits", From: "project-x", To: "auditor", Label: "restricted"},     // 不可见延伸
		{ID: "bookmark", From: "alice", To: "dashboard", Label: "public"},
	}
	for _, l := range links {
		if err := store.AddLink(l); err != nil {
			log.Fatal(err)
		}
	}

	logger := ontology.LoggerFunc(func(e ontology.LogEntry) {
		fmt.Printf("  [log] kind=%-8s at=%-10s path=%v reason=%s\n", e.Kind, e.At, e.Path, e.Reason)
	})

	for _, caller := range [][]ontology.Label{{"public"}, {"public", "restricted"}} {
		fmt.Printf("=== caller labels: %v ===\n", caller)
		res, err := ontology.Traverse(store, ontology.Request{
			CallerLabels: caller,
			Start:        "alice",
			MaxDepth:     8,
		}, logger)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(render(res.Root, ""))
		fmt.Printf("  stats: %+v (snapshot version %d)\n\n", res.Stats, res.Version)
	}
}

func render(n *ontology.Node, indent string) string {
	out := ""
	for _, e := range n.Edges {
		switch {
		case e.Kind == ontology.TermExtended:
			out += fmt.Sprintf("%s-%s[%s]-> %s\n", indent, e.LinkID, e.LinkLabel, e.To)
			out += render(e.Child, indent+"  ")
		case e.Kind == ontology.TermCycle && e.Visible:
			out += fmt.Sprintf("%sCYCLE -%s[%s]-> %s\n", indent, e.LinkID, e.LinkLabel, e.To)
		case e.Kind == ontology.TermCycle:
			out += fmt.Sprintf("%sCYCLE(hidden) 此方向被判定为环路并终止\n", indent)
		case e.Kind == ontology.TermHidden:
			out += fmt.Sprintf("%sHIDDEN-EXT 此处存在不可见延伸，是否成环未知\n", indent)
		}
	}
	return out
}
