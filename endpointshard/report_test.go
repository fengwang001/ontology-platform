package endpointshard

import "testing"

// 变更报告的精确集合与代次：只有内容变化、新建或被删除的分片出现在报告中，
// 每个分片在报告里出现时代次恰好递增一次。
func TestChangeReportExactSetAndGenerations(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)

	type want struct {
		id   int
		kind ChangeKind
		gen  uint64
	}
	check := func(step string, rep ChangeReport, wants ...want) {
		t.Helper()
		if len(rep.Shards) != len(wants) {
			t.Fatalf("%s: report = %+v, want %d entries", step, rep, len(wants))
		}
		for i, w := range wants {
			got := rep.Shards[i]
			if got.ShardID != w.id || got.Kind != w.kind || got.Generation != w.gen {
				t.Fatalf("%s: entry %d = %+v, want {id:%d kind:%v gen:%d}", step, i, got, w.id, w.kind, w.gen)
			}
		}
		for i := 1; i < len(rep.Shards); i++ {
			if rep.Shards[i-1].ShardID >= rep.Shards[i].ShardID {
				t.Fatalf("%s: report not sorted by shard id: %+v", step, rep)
			}
		}
		t.Logf("输入: %s; 实际输出: %+v; 判定依据: 报告集合与代次精确匹配 %+v", step, rep, wants)
	}

	rep := mustSync(t, m, "svc", readyEps("a", "b", "c"))
	check("sync[a b c]", rep, want{1, ChangeCreated, 1}, want{2, ChangeCreated, 1})

	rep = mustSync(t, m, "svc", readyEps("a", "b", "c"))
	check("sync 相同集合", rep)
	v := mustDescribe(t, m, "svc")
	if shardGen(t, v, 1) != 1 || shardGen(t, v, 2) != 1 {
		t.Fatalf("no-op sync must not change generations: %+v", v)
	}

	rep = mustSync(t, m, "svc", readyEps("a", "b", "c", "d")) // d -> 唯一未满的分片2
	check("sync 新增 d", rep, want{2, ChangeUpdated, 2})

	rep = mustSync(t, m, "svc", readyEps("a", "b", "d")) // 移除 c, 分片2 剩 {d}
	check("sync 移除 c", rep, want{2, ChangeUpdated, 3})

	rep = mustSync(t, m, "svc", readyEps("a", "b", "d", "e", "f")) // e->s2, f->新建 s3
	check("sync 新增 e f", rep, want{2, ChangeUpdated, 4}, want{3, ChangeCreated, 1})

	// 移除 a,b,d,e：分片1、2 变空被删除；分片3 内容不变不得出现在报告里。
	rep = mustSync(t, m, "svc", readyEps("f"))
	check("sync 仅保留 f", rep, want{1, ChangeDeleted, 2}, want{2, ChangeDeleted, 5})

	v = mustDescribe(t, m, "svc")
	if len(v.Shards) != 1 || v.Shards[0].ID != 3 || v.Shards[0].Generation != 1 {
		t.Fatalf("only shard 3 (gen 1) must remain: %+v", v)
	}
	if v.LastShardID != 3 {
		t.Fatalf("last shard id must be 3: %+v", v)
	}
}

// 状态位变化只更新内容：不换分片、不影响未涉及分片。
func TestStatusBitChangeOnlyUpdatesContent(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	mustSync(t, m, "svc", readyEps("a", "b", "c")) // s1{a,b} s2{c}

	desired := []Endpoint{ep("a", "cn", true, true), readyEp("b", "cn"), readyEp("c", "cn")}
	rep := mustSync(t, m, "svc", desired)
	v := mustDescribe(t, m, "svc")
	t.Logf("输入: a 置终止中; 实际输出: %+v; 判定依据: a 留分片1, 报告仅含分片1, 内容已更新", rep)
	if endpointShard(v, "a") != 1 {
		t.Fatalf("status change must not move endpoint: %+v", v)
	}
	if len(rep.Shards) != 1 || rep.Shards[0].ShardID != 1 || rep.Shards[0].Kind != ChangeUpdated {
		t.Fatalf("only shard 1 may be reported: %+v", rep)
	}
	got := rep.Shards[0].Endpoints[0]
	if got.ID != "a" || !got.Terminating || !got.Healthy {
		t.Fatalf("content must be updated in place: %+v", got)
	}
}
