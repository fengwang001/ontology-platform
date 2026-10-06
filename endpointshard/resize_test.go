package endpointshard

import "testing"

func mustResize(t *testing.T, m *Manager, name string, newM int) ChangeReport {
	t.Helper()
	rep, err := m.Resize(name, newM)
	if err != nil {
		t.Fatalf("resize %s: %v", name, err)
	}
	return rep
}

// 变大：不触发任何搬移，只影响此后的落位与合并判断。
func TestResizeGrow(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	mustSync(t, m, "svc", readyEps("a", "b", "c", "d")) // s1{a,b} s2{c,d}
	before := mustDescribe(t, m, "svc")
	rep := mustResize(t, m, "svc", 4)
	after := mustDescribe(t, m, "svc")
	t.Logf("输入: resize 2->4; 实际输出: %+v; 判定依据: 报告为空, 布局与代次不变", rep)
	if !rep.Empty() {
		t.Fatalf("grow must not move anything: %+v", rep)
	}
	if len(after.Shards) != 2 || after.Shards[0].Generation != before.Shards[0].Generation ||
		after.Shards[1].Generation != before.Shards[1].Generation || after.M != 4 {
		t.Fatalf("grow must keep layout and generations: %+v", after)
	}
	// 影响此后的落位：新端点优先填满已未满的分片1（并列取小编号）。
	rep = mustSync(t, m, "svc", readyEps("a", "b", "c", "d", "e", "f"))
	after = mustDescribe(t, m, "svc")
	t.Logf("输入: 新增 e,f; 实际输出: %+v; 判定依据: e,f 依次落分片1 至满", rep)
	if !equalStrings(shardMembers(t, after, 1), []string{"a", "b", "e", "f"}) {
		t.Fatalf("new endpoints must fill shard 1 first: %+v", after)
	}
	// 影响此后的合并判断：容量 4 下移除后 [1,1] 可合并。
	rep = mustSync(t, m, "svc", readyEps("a", "c"))
	after = mustDescribe(t, m, "svc")
	t.Logf("输入: 仅留 a,c; 实际输出: %+v; 判定依据: 移除后 [1,1] 合并为单分片", rep)
	if len(after.Shards) != 1 || !equalStrings(shardMembers(t, after, 1), []string{"a", "c"}) {
		t.Fatalf("merge judgement must use new M: %+v", after)
	}
}

// 变小：超容分片移出字典序最大端点并按新增规则重新落位，原子调整后整理一次。
func TestResizeShrink(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 4)
	mustSync(t, m, "svc", readyEps("a", "b", "c", "d", "e", "f")) // s1{a,b,c,d} s2{e,f}
	rep := mustResize(t, m, "svc", 2)
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: resize 4->2; 实际输出: %+v; 判定依据: s1 移出 c,d 并新建 s3 落位", rep)
	// s1 超容(4>2)：移出字典序最大的 d,c（均入被移出集）。
	// 被移出集按字典序落位：c 先 -> s2{e,f} 已满，无未满分片 -> 新建 s3{c}；
	// d -> 唯一未满的 s3 -> s3{c,d}。整理：三分片 [2,2,2]，两最少之和 4>2 不合并。
	if !equalStrings(shardMembers(t, v, 1), []string{"a", "b"}) {
		t.Fatalf("shard 1 must keep smallest ids: %+v", v)
	}
	if !equalStrings(shardMembers(t, v, 2), []string{"e", "f"}) {
		t.Fatalf("shard 2 untouched: %+v", v)
	}
	if !equalStrings(shardMembers(t, v, 3), []string{"c", "d"}) {
		t.Fatalf("evicted endpoints must be re-placed into a new shard: %+v", v)
	}
	c1, _ := findChange(rep, 1)
	c3, _ := findChange(rep, 3)
	if c1.Kind != ChangeUpdated || c3.Kind != ChangeCreated {
		t.Fatalf("report must contain updated shard 1 and created shard 3: %+v", rep)
	}
}

// 变小且存在未满分片时，被移出端点优先落进现有未满分片。
func TestResizeShrinkIntoExistingShard(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 3)
	mustSync(t, m, "svc", readyEps("a", "b", "c", "d")) // s1{a,b,c} s2{d}
	rep := mustResize(t, m, "svc", 2)
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: resize 3->2; 实际输出: %+v; 判定依据: s1 移出 c, 落入未满的 s2", rep)
	if !equalStrings(shardMembers(t, v, 1), []string{"a", "b"}) ||
		!equalStrings(shardMembers(t, v, 2), []string{"c", "d"}) {
		t.Fatalf("evicted c must go to non-full shard 2: %+v", v)
	}
	if len(v.Shards) != 2 {
		t.Fatalf("no new shard may be created: %+v", v)
	}
}

// 调整为相同容量是空操作；非正容量报参数非法且不改变状态。
func TestResizeNoopAndInvalid(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	mustSync(t, m, "svc", readyEps("a", "b", "c"))
	before := mustDescribe(t, m, "svc")
	rep := mustResize(t, m, "svc", 2)
	t.Logf("输入: resize 2->2; 实际输出: %+v; 判定依据: 空报告且状态不变", rep)
	if !rep.Empty() {
		t.Fatalf("same capacity must be a no-op: %+v", rep)
	}
	for _, bad := range []int{0, -1, -100} {
		if _, err := m.Resize("svc", bad); err == nil {
			t.Fatalf("resize %d must fail", bad)
		} else if k, _ := KindOf(err); k != KindInvalidArgument {
			t.Fatalf("resize %d: kind = %v, want invalid argument", bad, k)
		}
	}
	after := mustDescribe(t, m, "svc")
	if after.M != before.M || after.LastShardID != before.LastShardID ||
		len(after.Shards) != len(before.Shards) || after.Shards[0].Generation != before.Shards[0].Generation {
		t.Fatalf("rejected resize must not change state: %+v", after)
	}
	t.Logf("输入: resize 0/-1/-100; 实际输出: invalid argument; 判定依据: 状态与代次不变")
}
