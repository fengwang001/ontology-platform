package endpointshard

import (
	"testing"
)

// 已有端点必须留在原分片，即使状态位或区域变化也只更新内容。
func TestSyncKeepsExistingEndpointsInShard(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)

	rep1 := mustSync(t, m, "svc", []Endpoint{readyEp("a", "cn"), readyEp("b", "cn"), readyEp("c", "us")})
	t.Logf("输入: sync[a b c](均就绪); 实际输出: %+v; 判定依据: a,b 落分片1, c 落新建分片2", rep1)
	v := mustDescribe(t, m, "svc")
	if endpointShard(v, "a") != 1 || endpointShard(v, "b") != 1 || endpointShard(v, "c") != 2 {
		t.Fatalf("unexpected layout: %+v", v)
	}

	// a 换区域且失健康，b 变为终止中，c 完全不变。
	desired := []Endpoint{
		ep("a", "eu", false, false),
		ep("b", "cn", true, true),
		readyEp("c", "us"),
	}
	rep2 := mustSync(t, m, "svc", desired)
	v2 := mustDescribe(t, m, "svc")
	t.Logf("输入: sync[a 换区域+失健康, b 终止中, c 不变]; 实际输出: %+v; 判定依据: 均留原分片, 仅分片1 内容更新", rep2)
	if endpointShard(v2, "a") != 1 || endpointShard(v2, "b") != 1 || endpointShard(v2, "c") != 2 {
		t.Fatalf("existing endpoints must stay in their shards: %+v", v2)
	}
	if len(rep2.Shards) != 1 {
		t.Fatalf("only shard 1 changed, report must have 1 entry: %+v", rep2)
	}
	c1, _ := findChange(rep2, 1)
	if c1.Kind != ChangeUpdated || c1.Generation != 2 {
		t.Fatalf("shard 1 must be updated at generation 2: %+v", c1)
	}
	if len(c1.Endpoints) != 2 || c1.Endpoints[0] != desired[0] || c1.Endpoints[1] != desired[1] {
		t.Fatalf("shard 1 content must reflect new endpoint data: %+v", c1)
	}
}

// 新增端点落位：端点数最多但未满的分片；并列取编号小者；无未满则新建。
func TestSyncPlacementMostFullAndTieBreak(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 3)

	// 构造分片1{e1,e2,e3} 分片2{e4,e5,e6}，再移除 e3,e6 得到 [2,2]。
	mustSync(t, m, "svc", readyEps("e1", "e2", "e3", "e4", "e5", "e6"))
	rep := mustSync(t, m, "svc", readyEps("e1", "e2", "e4", "e5"))
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: 移除 e3,e6; 实际输出: %+v; 判定依据: 两个分片各剩 2 个, 2+2>3 不合并", rep)
	if !equalStrings(shardMembers(t, v, 1), []string{"e1", "e2"}) ||
		!equalStrings(shardMembers(t, v, 2), []string{"e4", "e5"}) {
		t.Fatalf("unexpected layout: %+v", v)
	}

	// 并列 [2,2]：新增 x 必须进编号较小的分片1。
	rep = mustSync(t, m, "svc", readyEps("e1", "e2", "e4", "e5", "x"))
	v = mustDescribe(t, m, "svc")
	t.Logf("输入: 新增 x; 实际输出: %+v; 判定依据: 并列取编号小者 -> 分片1", rep)
	if endpointShard(v, "x") != 1 {
		t.Fatalf("tie must break to lowest shard id: %+v", v)
	}
}
