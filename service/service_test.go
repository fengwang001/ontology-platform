package service

import (
	"fmt"
	"sync"
	"testing"

	"ontology/store"
)

func newService() *Service { return New(store.New(store.NewFakeClock(1))) }

func TestTokenFormatValidation(t *testing.T) {
	svc := newService()
	for _, tok := range []string{"", "x1", "v", "v-1", "v1.5", "1"} {
		if _, err := svc.Write("T", "a", tok, 10, "p"); err == nil || err.Code != CodeInvalidArgument {
			t.Fatalf("凭证 %q 应报 INVALID_ARGUMENT, got %v", tok, err)
		}
	}
	if _, err := svc.Write("T", "a", "v0", 10, "p"); err != nil {
		t.Fatalf("合法凭证 v0 不应报错: %v", err)
	}
}

func TestErrorNormalization(t *testing.T) {
	svc := newService()
	if _, err := svc.Write("T", "a", "v0", 50, "p1"); err != nil {
		t.Fatal(err)
	}
	// 参数非法优先于凭证冲突。
	if _, err := svc.Write("", "a", "v9", 10, "x"); err.Code != CodeInvalidArgument {
		t.Fatalf("got %v", err)
	}
	// 凭证冲突优先于业务边界。
	if _, err := svc.Write("T", "a", "v9", 10, "x"); err.Code != CodeConcurrencyConflict {
		t.Fatalf("got %v", err)
	}
	// 业务边界。
	if _, err := svc.Write("T", "a", "v1", 10, "x"); err.Code != CodeBizBoundary {
		t.Fatalf("got %v", err)
	}
}

// TestInterleavedTokenCompetition 两个客户端基于同一前序凭证交错竞争，
// 恰好一个成功。
func TestInterleavedTokenCompetition(t *testing.T) {
	svc := newService()
	if _, err := svc.Write("T", "a", "v0", 10, "p1"); err != nil {
		t.Fatal(err)
	}
	// 两个客户端同时读到 v1。
	tokA, tokB := svc.LatestToken("T", "a"), svc.LatestToken("T", "a")
	if tokA != "v1" || tokB != "v1" {
		t.Fatalf("tokA=%s tokB=%s", tokA, tokB)
	}
	_, errA := svc.Write("T", "a", tokA, 20, "A")
	_, errB := svc.Write("T", "a", tokB, 20, "B")
	if (errA == nil) == (errB == nil) {
		t.Fatalf("基于同一前序的两个写入必须恰好一个成功: errA=%v errB=%v", errA, errB)
	}
	if errA != nil && errA.Code != CodeConcurrencyConflict {
		t.Fatalf("败者应报 CONCURRENCY_CONFLICT: %v", errA)
	}
	if errB != nil && errB.Code != CodeConcurrencyConflict {
		t.Fatalf("败者应报 CONCURRENCY_CONFLICT: %v", errB)
	}
	// 并发 goroutine 版本：N 个竞争者抢同一凭证，恰好一个成功。
	const n = 16
	svc2 := newService()
	if _, err := svc2.Write("T", "hot", "v0", 10, "seed"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc2.Write("T", "hot", "v1", 20, fmt.Sprintf("w%d", i))
			mu.Lock()
			if err == nil {
				wins++
			} else if err.Code != CodeConcurrencyConflict {
				t.Errorf("意外错误: %v", err)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("同一凭证的 %d 个竞争者中恰应 1 个成功, got %d", n, wins)
	}
}

func TestEqualBizStartOverrideService(t *testing.T) {
	svc := newService()
	v1, err := svc.Write("T", "a", "v0", 10, "old")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := svc.Write("T", "a", "v1", 10, "new") // 业务起点相同，系统时间更晚
	if err != nil {
		t.Fatal(err)
	}
	// 当前视角：新覆盖旧。
	if r := svc.Query("T", "a", v2.Sys, 10); r.Status != StatusFound || r.Version.Payload != "new" {
		t.Fatalf("应被新版本覆盖: %+v", r)
	}
	// 回溯到 v1 的系统时间：旧版本仍可见（两条记录都保留）。
	if r := svc.Query("T", "a", v1.Sys, 10); r.Status != StatusFound || r.Version.Payload != "old" {
		t.Fatalf("旧版本应保留可见: %+v", r)
	}
}
