package orphanreclaim_test

import (
	"fmt"

	"ontology/orphanreclaim"
)

// Example 演示：联合单边不足保留 -> 补齐联合组救回 -> 再次变孤儿从第一代重算 -> 两代期满清理。
func Example() {
	cfg := orphanreclaim.Config{
		Rules: map[string]orphanreclaim.LinkRule{
			"ownedBy": {Type: "ownedBy", Kind: orphanreclaim.IndependentRetention},
			"partOf":  {Type: "partOf", Kind: orphanreclaim.JointRetention, JointGroup: []string{"derivedFrom"}},
			"derivedFrom": {Type: "derivedFrom", Kind: orphanreclaim.JointRetention,
				JointGroup: []string{"partOf"}},
		},
		GraceGen1: 10,
		GraceGen2: 4,
	}

	var now int64
	clock := func() int64 { return now }
	r, err := orphanreclaim.New(cfg, clock, nil, nil)
	if err != nil {
		panic(err)
	}
	r.CreateObject("dataset")
	r.CreateObject("pipeline")
	r.CreateObject("project")

	// 只有 partOf 一条联合单边：孤儿，进第一代。
	_ = r.AddLink("pipeline", "dataset", "partOf")
	g, _ := r.GenerationOf("dataset")
	fmt.Println("after single joint edge:", int(g))

	// 补齐 derivedFrom：联合组满足，脱离队列。
	_ = r.AddLink("project", "dataset", "derivedFrom")
	g, _ = r.GenerationOf("dataset")
	fmt.Println("joint group complete:", int(g))

	// 联合组再次破坏：重新从第一代、以当前时刻起算。
	now = 8
	_ = r.RemoveLink("project", "dataset", "derivedFrom")
	now = 18
	r.Advance() // 18-8=10：第一代满，升第二代（deadline=22）
	g, _ = r.GenerationOf("dataset")
	fmt.Println("promoted at 18:", int(g))

	now = 21
	r.Advance() // 还差一刻：仍在第二代
	g, _ = r.GenerationOf("dataset")
	fmt.Println("still gen2 at 21:", int(g))

	now = 22
	r.Advance() // 第二代满：真正清理
	_, err = r.GenerationOf("dataset")
	fmt.Println("purged at 22:", err == orphanreclaim.ErrObjectNotFound)

	// Output:
	// after single joint edge: 1
	// joint group complete: 0
	// promoted at 18: 2
	// still gen2 at 21: 2
	// purged at 22: true
}
