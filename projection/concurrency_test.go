package projection

import (
	"sync"
	"testing"
)

// TestConcurrentProjectionWithDistinctRuleSets 用两套规则并发投影同一对象，
// 验证结果互不影响；配合 go test -race 检测数据竞争。
func TestConcurrentProjectionWithDistinctRuleSets(t *testing.T) {
	rsA := mustCompile(t, Config{Deny: []string{"salary"}})
	rsB := mustCompile(t, Config{Deny: []string{"age", "addr.geo.*"}})
	shared := sampleObject()

	const workers = 64
	var wg sync.WaitGroup
	errs := make(chan string, workers*2)

	verify := func(tag string, out map[string]any, err error, absent ...string) {
		if err != nil {
			errs <- tag + ": " + err.Error()
			return
		}
		for _, key := range absent {
			if _, ok := out[key]; ok {
				errs <- tag + ": 字段 " + key + " 应被裁掉"
			}
		}
		if len(out) != 4 {
			errs <- tag + ": 字段数异常"
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			out, err := rsA.Project(shared)
			verify("A", out, err, "salary")
		}()
		go func() {
			defer wg.Done()
			out, err := rsB.Project(shared)
			verify("B", out, err, "age")
			addr, ok := out["addr"].(map[string]any)
			if !ok {
				errs <- "B: addr 应保留"
				return
			}
			if _, ok := addr["geo"]; ok {
				errs <- "B: addr.geo 子字段全裁后应消失"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
	if shared["salary"] != 123456 {
		t.Error("并发投影修改了原对象")
	}
}

// TestSharedRuleSetConcurrentReuse 验证同一编译结果可并发复用于不同对象。
func TestSharedRuleSetConcurrentReuse(t *testing.T) {
	rs := mustCompile(t, Config{Deny: []string{"addr.*"}})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := rs.Project(sampleObject())
			if err != nil {
				t.Error(err)
				return
			}
			if _, ok := out["addr"]; ok {
				t.Error("addr 子字段全裁后应消失")
			}
		}()
	}
	wg.Wait()
}
