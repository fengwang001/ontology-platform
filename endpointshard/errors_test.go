package endpointshard

import "testing"

func mustKind(t *testing.T, err error, want Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error kind %v, got nil", want)
	}
	got, ok := KindOf(err)
	if !ok || got != want {
		t.Fatalf("error kind = %v (ok=%v), want %v; err=%v", got, ok, want, err)
	}
	t.Logf("输入: 触发错误; 实际输出: %v; 判定依据: 类别为 %v", err, want)
}

// 错误类别可区分且优先级明确：参数非法 > 服务不存在 > 服务已存在。
func TestErrorPriority(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	// 参数非法优先于服务不存在。
	_, err := m.Sync("ghost", []Endpoint{readyEp("a", "cn"), readyEp("a", "cn")})
	mustKind(t, err, KindInvalidArgument)
	_, err = m.Sync("ghost", []Endpoint{ep("", "cn", true, false)})
	mustKind(t, err, KindInvalidArgument)
	_, err = m.Sync("ghost", []Endpoint{ep("a", "", true, false)})
	mustKind(t, err, KindInvalidArgument)
	_, err = m.Resize("ghost", 0)
	mustKind(t, err, KindInvalidArgument)
	// 参数非法优先于服务已存在。
	mustKind(t, m.CreateService("svc", 0), KindInvalidArgument)
	mustKind(t, m.CreateService("svc", -3), KindInvalidArgument)
	// 合法参数下报服务不存在。
	_, err = m.Sync("ghost", readyEps("a"))
	mustKind(t, err, KindServiceNotFound)
	_, err = m.Query("ghost", "cn")
	mustKind(t, err, KindServiceNotFound)
	_, err = m.Resize("ghost", 3)
	mustKind(t, err, KindServiceNotFound)
	mustKind(t, m.DeleteService("ghost"), KindServiceNotFound)
	// 重复创建报已存在。
	mustKind(t, m.CreateService("svc", 2), KindServiceAlreadyExists)
	// 空服务名一律参数非法。
	mustKind(t, m.CreateService("", 1), KindInvalidArgument)
	mustKind(t, m.DeleteService(""), KindInvalidArgument)
	_, err = m.Sync("", nil)
	mustKind(t, err, KindInvalidArgument)
	_, err = m.Query("", "cn")
	mustKind(t, err, KindInvalidArgument)
	_, err = m.Resize("", 1)
	mustKind(t, err, KindInvalidArgument)
}

// 服务可创建与删除；删除后同步与查询报不存在；重建后编号重新计数。
func TestServiceLifecycle(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	mustSync(t, m, "svc", readyEps("a", "b", "c"))
	if err := m.DeleteService("svc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustKind(t, m.DeleteService("svc"), KindServiceNotFound)
	_, err := m.Sync("svc", nil)
	mustKind(t, err, KindServiceNotFound)
	_, err = m.Query("svc", "cn")
	mustKind(t, err, KindServiceNotFound)
	mustCreate(t, m, "svc", 1)
	rep := mustSync(t, m, "svc", readyEps("x"))
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: 删除后重建并同步; 实际输出: %+v; 判定依据: 新服务编号从 1 重新计数", rep)
	if v.LastShardID != 1 || len(v.Shards) != 1 {
		t.Fatalf("recreated service must start fresh: %+v", v)
	}
}

// 被拒绝的操作不得改变任何状态，包括分片编号与代次。
func TestRejectedOpsDontChangeState(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	mustSync(t, m, "svc", readyEps("a", "b", "c"))
	before := mustDescribe(t, m, "svc")
	// 各类被拒绝的操作。
	_, _ = m.Sync("svc", []Endpoint{readyEp("x", "cn"), readyEp("x", "cn")}) // 重复标识
	_, _ = m.Sync("svc", []Endpoint{ep("", "cn", true, false)})              // 空标识
	_, _ = m.Sync("svc", []Endpoint{ep("y", "", true, false)})               // 空区域
	_, _ = m.Resize("svc", 0)                                                // 非正容量
	_ = m.CreateService("svc", 5)                                            // 已存在
	_, _ = m.Sync("ghost", nil)                                              // 不存在
	_, _ = m.Resize("ghost", 3)                                              // 不存在
	after := mustDescribe(t, m, "svc")
	t.Logf("输入: 一系列被拒绝的操作; 实际输出: %+v; 判定依据: 与拒绝前完全一致", after)
	if after.M != before.M || after.LastShardID != before.LastShardID || len(after.Shards) != len(before.Shards) {
		t.Fatalf("rejected ops changed state: %+v -> %+v", before, after)
	}
	for i := range before.Shards {
		if !shardViewEqual(after.Shards[i], before.Shards[i]) {
			t.Fatalf("shard %d changed: %+v -> %+v", i, before.Shards[i], after.Shards[i])
		}
	}
}

func shardViewEqual(a, b ShardView) bool {
	if a.ID != b.ID || a.Generation != b.Generation || len(a.Endpoints) != len(b.Endpoints) {
		return false
	}
	for i := range a.Endpoints {
		if a.Endpoints[i] != b.Endpoints[i] {
			return false
		}
	}
	return true
}
