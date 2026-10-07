package servicemesh

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentPublishAndRoute 验证：
//  1. 并发结果等价于某个串行顺序：每个请求要么拿到版本 v 的配置结果，
//     要么拿到 v+1 的结果，绝不可能混合；
//  2. 只有一个发布者以正确基版本成功，版本号从 1 严格单调到最终值；
//  3. -race 下无数据竞争。
func TestConcurrentPublishAndRoute(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	v0 := &ServiceConfig{Rules: []Rule{{
		Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
		Targets: []Target{{Subset: "v0", Weight: 100}},
	}}}
	v1 := &ServiceConfig{Rules: []Rule{{
		Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
		Targets: []Target{{Subset: "v1", Weight: 100}},
	}}}
	if _, e := m.Publish("svc", v0, 0); e != nil {
		t.Fatal(e)
	}
	m.RegisterSubsets("svc", map[string][]Endpoint{
		"v0": {{Name: "e0", Ready: true}},
		"v1": {{Name: "e1", Ready: true}},
	})

	const publishers = 16
	const routers = 16
	const routesEach = 2000

	var wg sync.WaitGroup
	var successPub int64
	var failPub int64
	start := make(chan struct{})

	// 发布者都基于版本 1：恰有一个成功，其余版本冲突。
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := m.Publish("svc", v1, 1)
			if e == nil {
				atomic.AddInt64(&successPub, 1)
			} else {
				if e.Class != ClassVersionConflict {
					t.Errorf("非预期发布错误: %v", e)
				}
				atomic.AddInt64(&failPub, 1)
			}
		}()
	}

	var sawV0, sawV1 int64
	var invalid int64
	for r := 0; r < routers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < routesEach; i++ {
				res, e := m.Route("svc", Request{Path: "/", Bucket: 0})
				if e != nil {
					atomic.AddInt64(&invalid, 1)
					continue
				}
				switch res.Subset {
				case "v0":
					atomic.AddInt64(&sawV0, 1)
				case "v1":
					atomic.AddInt64(&sawV1, 1)
				default:
					atomic.AddInt64(&invalid, 1)
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	if successPub != 1 || failPub != publishers-1 {
		t.Fatalf("恰好一个发布成功: success=%d fail=%d", successPub, failPub)
	}
	if m.Version("svc") != 2 {
		t.Fatalf("最终版本应为2 got %d", m.Version("svc"))
	}
	if invalid != 0 {
		t.Fatalf("出现非法/混合结果 %d", invalid)
	}
	l.log("并发: %d 发布者(%d成功/%d冲突) + %d 路由者x%d 请求: v0结果=%d v1结果=%d 非法=%d | 判定依据: 每请求只观察到整份v0或整份v1",
		publishers, successPub, failPub, routers, routesEach, sawV0, sawV1, invalid)
}

// TestConcurrentRegisterAndRoute 并发端点注册与路由下结果必须自洽。
func TestConcurrentRegisterAndRoute(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{Rules: []Rule{{
		Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
		Targets: []Target{{Subset: "only", Weight: 100}},
	}}}
	if _, e := m.Publish("svc", cfg, 0); e != nil {
		t.Fatal(e)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	var ready, noEp int64
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 200; i++ {
			m.RegisterSubsets("svc", map[string][]Endpoint{
				"only": {{Name: "ep", Ready: i%2 == 0}},
			})
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 4000; i++ {
			_, e := m.Route("svc", Request{Path: "/", Bucket: 0})
			if e == nil {
				atomic.AddInt64(&ready, 1)
			} else if e.Class == ClassNoEndpoint {
				atomic.AddInt64(&noEp, 1)
			} else {
				t.Errorf("非预期错误 %v", e)
			}
		}
	}()
	close(start)
	wg.Wait()
	l.log("并发注册/路由: 就绪结果=%d 无端点结果=%d | 判定依据: 只可能是两种一致快照之一",
		ready, noEp)
	if ready+noEp != 4000 {
		t.Fatal("存在既非就绪也非无端点的结果")
	}
}

// TestSerializabilityAcrossVersions 连续串行版本发布时，路由观察到的版本序列必须单调。
func TestSerializabilityAcrossVersions(t *testing.T) {
	m := NewMesh()
	base := func(sub string) *ServiceConfig {
		return &ServiceConfig{Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
			Targets: []Target{{Subset: sub, Weight: 100}},
		}}}
	}
	m.Publish("svc", base("s0"), 0)
	for i := 1; i <= 20; i++ {
		sub := fmt.Sprintf("s%d", i)
		m.RegisterSubsets("svc", map[string][]Endpoint{sub: {{Name: "ep", Ready: true}}})
		v, e := m.Publish("svc", base(sub), uint64(i))
		if e != nil || v != uint64(i+1) {
			t.Fatalf("版本 %d 发布失败: v=%d e=%v", i, v, e)
		}
		res, e := m.Route("svc", Request{Path: "/", Bucket: 0})
		if e != nil {
			t.Fatal(e)
		}
		if res.Subset != sub {
			t.Fatalf("发布后立即路由应读到版本%d的子集 %s got %s", i+1, sub, res.Subset)
		}
	}
}
