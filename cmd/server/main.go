// 演示：构造一个小型本体图，执行一次双限制遍历并输出各分支的截断标记。
package main

import (
	"fmt"
	"log/slog"
	"os"

	"ontology/graph"
	"ontology/traverse"
)

func main() {
	store := graph.NewStore()
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		if err := store.AddObject(graph.Object{ID: id, Type: "Thing"}); err != nil {
			panic(err)
		}
	}
	links := []graph.Link{
		{ID: "l1", Type: "t", SourceID: "a", TargetID: "b"},
		{ID: "l2", Type: "t", SourceID: "b", TargetID: "c"},
		{ID: "l3", Type: "t", SourceID: "a", TargetID: "d"},
		{ID: "l4", Type: "t", SourceID: "d", TargetID: "e"},
		{ID: "l5", Type: "t", SourceID: "a", TargetID: "f"},
		{ID: "l6", Type: "t", SourceID: "a", TargetID: "g"},
		{ID: "l7", Type: "t", SourceID: "g", TargetID: "h"},
		{ID: "l8", Type: "t", SourceID: "h", TargetID: "i"},
	}
	for _, l := range links {
		if err := store.AddLink(l); err != nil {
			panic(err)
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := traverse.NewService(store, logger)
	res, err := svc.Traverse(traverse.Request{
		StartID:     "a",
		Directions:  []graph.Direction{graph.Outgoing},
		DepthLimit:  2,
		ResultLimit: 2,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("== returned ==")
	for _, b := range res.Returned {
		fmt.Printf("  %-40s marker=%s\n", b.Path.String(), b.Marker)
	}
	fmt.Println("== truncated ==")
	for _, b := range res.Truncated {
		fmt.Printf("  %-40s marker=%s\n", b.Path.String(), b.Marker)
	}
	fmt.Printf("stats: %+v\n", res.Stats)
}
