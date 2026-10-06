package endpointshard

import "testing"

// 空分片在整理时被删除；分片编号不复用。
func TestEmptyShardDeletion(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)

	mustSync(t, m, "svc", readyEps("a", "b", "c"))
	rep := mustSync(t, m, "svc", readyEps("a"))
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: 移除 b,c; 实际输出: %+v; 判定依据: 分片2 变空被删除, 分片1 保留 a", rep)
	if len(v.Shards) != 1 || v.Shards[0].ID != 1 {
		t.Fatalf("empty shard must be deleted: %+v", v)
	}
	c2, ok := findChange(rep, 2)
	if !ok || c2.Kind != ChangeDeleted || c2.Endpoints != nil {
		t.Fatalf("shard 2 must be reported as deleted: %+v", rep)
	}

	// 编号不复用：新建分片编号为 3。
	rep = mustSync(t, m, "svc", readyEps("a", "b", "c"))
	v = mustDescribe(t, m, "svc")
	t.Logf("输入: 新增 b,c; 实际输出: %+v; 判定依据: 新分片编号为 3, 不复用 2", rep)
	if v.LastShardID != 3 || endpointShard(v, "c") != 3 {
		t.Fatalf("shard ids must be monotonic and never reused: %+v", v)
	}
}

// 合并边界：两个最少分片端点数之和恰等于 M 时必须合并。
func TestMergeBoundarySumEqualsM(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)

	mustSync(t, m, "svc", readyEps("a", "b", "c", "d")) // s1{a,b} s2{c,d}
	rep := mustSync(t, m, "svc", readyEps("a", "c"))    // s1{a} s2{c}, 1+1=2=M -> 合并
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: 移除 b,d; 实际输出: %+v; 判定依据: 1+1 恰等于 M=2, 分片2 并入分片1", rep)
	if len(v.Shards) != 1 {
		t.Fatalf("sum == M must merge: %+v", v)
	}
	if !equalStrings(shardMembers(t, v, 1), []string{"a", "c"}) {
		t.Fatalf("shard 2 must merge into shard 1: %+v", v)
	}
	c1, _ := findChange(rep, 1)
	c2, _ := findChange(rep, 2)
	if c1.Kind != ChangeUpdated || c2.Kind != ChangeDeleted {
		t.Fatalf("merge report must contain updated target and deleted source: %+v", rep)
	}
}

// 之和超过 M 时不合并。
func TestNoMergeWhenSumExceedsM(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)

	mustSync(t, m, "svc", readyEps("a", "b", "c")) // s1{a,b} s2{c}
	rep := mustSync(t, m, "svc", readyEps("a", "b", "c", "d"))
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: 新增 d; 实际输出: %+v; 判定依据: d 落唯一未满的分片2, 2+2>2 不合并", rep)
	if len(v.Shards) != 2 || endpointShard(v, "d") != 2 {
		t.Fatalf("d must go to the only non-full shard 2: %+v", v)
	}
}

// 每次同步最多合并一次；与当前完全相同的同步报告为空且不改变代次。
func TestAtMostOneMergePerSyncAndNoopSync(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 4)

	ids := []string{"e01", "e02", "e03", "e04", "e05", "e06", "e07", "e08", "e09", "e10", "e11", "e12"}
	mustSync(t, m, "svc", readyEps(ids...)) // s1..s3 各 4 个
	rep := mustSync(t, m, "svc", readyEps("e01", "e05", "e09"))
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: 仅保留 e01,e05,e09; 实际输出: %+v; 判定依据: 三个分片各剩 1 个, 只合并一次 -> [2,1]", rep)
	if len(v.Shards) != 2 {
		t.Fatalf("at most one merge per sync: want 2 shards, got %+v", v)
	}
	if !equalStrings(shardMembers(t, v, 1), []string{"e01", "e05"}) ||
		!equalStrings(shardMembers(t, v, 3), []string{"e09"}) {
		t.Fatalf("one merge must produce [2,1]: %+v", v)
	}
	gen1, gen3 := shardGen(t, v, 1), shardGen(t, v, 3)

	// 完全相同的同步：报告为空，代次不变，且不触发进一步合并（[2,1] 仍可合并）。
	rep = mustSync(t, m, "svc", readyEps("e01", "e05", "e09"))
	v2 := mustDescribe(t, m, "svc")
	t.Logf("输入: 相同期望集; 实际输出: %+v; 判定依据: 报告为空, 代次与布局不变", rep)
	if !rep.Empty() {
		t.Fatalf("identical sync must return empty report: %+v", rep)
	}
	if len(v2.Shards) != 2 || shardGen(t, v2, 1) != gen1 || shardGen(t, v2, 3) != gen3 {
		t.Fatalf("identical sync must not change generations or layout: %+v", v2)
	}

	// 一次内容变化即可触发整理，[2,1] 合并为一个分片。
	desired := []Endpoint{readyEp("e01", "eu"), readyEp("e05", "cn"), readyEp("e09", "cn")}
	rep = mustSync(t, m, "svc", desired)
	v3 := mustDescribe(t, m, "svc")
	t.Logf("输入: e01 换区域; 实际输出: %+v; 判定依据: 整理合并 [2,1] -> 单分片", rep)
	if len(v3.Shards) != 1 || !equalStrings(shardMembers(t, v3, 1), []string{"e01", "e05", "e09"}) {
		t.Fatalf("cleanup must merge [2,1] into one shard: %+v", v3)
	}
}
