package endpointshard

import "testing"

func mustQuery(t *testing.T, m *Manager, name, region string) QueryResult {
	t.Helper()
	res, err := m.Query(name, region)
	if err != nil {
		t.Fatalf("query %s: %v", name, err)
	}
	return res
}

// 查询取所有分片中就绪的端点；同区域在前，各组内按标识字典序。
func TestQueryReadyAndRegionPriority(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	desired := []Endpoint{
		readyEp("p1", "cn"), readyEp("p2", "us"), readyEp("p3", "eu"),
		ep("t1", "cn", true, true),   // 可服务且终止中：不就绪
		ep("u1", "cn", false, false), // 不健康：不就绪也不可服务
	}
	mustSync(t, m, "svc", desired)

	res := mustQuery(t, m, "svc", "cn")
	t.Logf("输入: query(cn); 实际输出: %+v; 判定依据: 仅就绪端点, cn 在前, 组内字典序", res)
	if res.Fallback {
		t.Fatalf("must not fall back while ready endpoints exist: %+v", res)
	}
	if !equalStrings(idsOf(res.Endpoints), []string{"p1", "p2", "p3"}) {
		t.Fatalf("want [p1 p2 p3] with cn first, got %v", idsOf(res.Endpoints))
	}

	res = mustQuery(t, m, "svc", "eu")
	t.Logf("输入: query(eu); 实际输出: %+v; 判定依据: eu 的 p3 在最前", res)
	if !equalStrings(idsOf(res.Endpoints), []string{"p3", "p1", "p2"}) {
		t.Fatalf("want [p3 p1 p2], got %v", idsOf(res.Endpoints))
	}

	// 查询区域没有匹配端点时，全部按字典序。
	res = mustQuery(t, m, "svc", "jp")
	if !equalStrings(idsOf(res.Endpoints), []string{"p1", "p2", "p3"}) {
		t.Fatalf("want [p1 p2 p3], got %v", idsOf(res.Endpoints))
	}
}

// 就绪端点一个也没有时回退为可服务且终止中的端点；仍为空则结果为空。
func TestQueryFallback(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 3)
	desired := []Endpoint{
		ep("t2", "us", true, true),
		ep("t1", "cn", true, true),
		ep("u1", "cn", false, false),
		ep("u2", "us", false, true), // 不健康且终止中：不可服务，不进回退集
	}
	mustSync(t, m, "svc", desired)

	res := mustQuery(t, m, "svc", "cn")
	t.Logf("输入: query(cn) 无就绪端点; 实际输出: %+v; 判定依据: 回退为可服务且终止中, cn 在前", res)
	if !res.Fallback {
		t.Fatalf("fallback must be reported: %+v", res)
	}
	if !equalStrings(idsOf(res.Endpoints), []string{"t1", "t2"}) {
		t.Fatalf("want fallback set [t1 t2], got %v", idsOf(res.Endpoints))
	}

	// 回退集也为空：结果为空，但仍标记发生了回退。
	mustSync(t, m, "svc", []Endpoint{ep("u1", "cn", false, false)})
	res = mustQuery(t, m, "svc", "cn")
	t.Logf("输入: 全部不健康后 query(cn); 实际输出: %+v; 判定依据: 回退集为空 -> 空结果", res)
	if !res.Fallback || len(res.Endpoints) != 0 {
		t.Fatalf("want empty result with fallback=true, got %+v", res)
	}

	// 服务完全为空时同样为空。
	mustSync(t, m, "svc", nil)
	res = mustQuery(t, m, "svc", "cn")
	if !res.Fallback || len(res.Endpoints) != 0 {
		t.Fatalf("want empty result with fallback=true, got %+v", res)
	}
}

// 状态位变化即时反映到查询路径（就绪与回退之间迁移）。
func TestQueryReflectsStatusTransitions(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 2)
	mustSync(t, m, "svc", []Endpoint{readyEp("a", "cn")})

	mustSync(t, m, "svc", []Endpoint{ep("a", "cn", true, true)})
	res := mustQuery(t, m, "svc", "cn")
	t.Logf("输入: a 改为终止中后 query; 实际输出: %+v; 判定依据: a 进入回退集", res)
	if !res.Fallback || !equalStrings(idsOf(res.Endpoints), []string{"a"}) {
		t.Fatalf("a must move to fallback set: %+v", res)
	}

	mustSync(t, m, "svc", []Endpoint{ep("a", "cn", false, false)})
	res = mustQuery(t, m, "svc", "cn")
	if !res.Fallback || len(res.Endpoints) != 0 {
		t.Fatalf("unhealthy endpoint must appear nowhere: %+v", res)
	}
}
