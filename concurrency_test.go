package ontology

import (
	"reflect"
	"sync"
	"testing"
)

// TestConcurrentProjectionIsolation 用两套不同规则并发投影同一对象，
// 验证结果互不影响、不共享中间状态，且原对象不被修改（配合 -race）。
func TestConcurrentProjectionIsolation(t *testing.T) {
	obj := sampleObject()
	snapshot := sampleObject()
	allowOnly := mustCompile(t, []string{"id", "name"}, nil)
	denyName := mustCompile(t, nil, []string{"name"})

	const workers = 64
	const rounds = 50
	errs := make(chan string, workers*rounds*2)
	var wg sync.WaitGroup

	project := func(rs *RuleSet, check func(map[string]any) string) {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			out, err := Project(obj, rs, nil)
			if err != nil {
				errs <- err.Error()
				return
			}
			out["marker"] = i
			if msg := check(out); msg != "" {
				errs <- msg
				return
			}
		}
	}

	for w := 0; w < workers; w++ {
		wg.Add(2)
		go project(allowOnly, func(out map[string]any) string {
			if out["name"] != "alice" || out["id"] != "o-1" || len(out) != 3 {
				return "allow-only projection corrupted"
			}
			return ""
		})
		go project(denyName, func(out map[string]any) string {
			if _, ok := out["name"]; ok {
				return "deny-name projection leaked name"
			}
			if out["secret"] != "s3cr3t" {
				return "deny-name projection lost secret"
			}
			return ""
		})
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
	if !reflect.DeepEqual(obj, snapshot) {
		t.Error("original object mutated during concurrent projection")
	}
}

// TestConcurrentCompiledRuleSetReuse 验证同一编译结果可并发复用。
func TestConcurrentCompiledRuleSetReuse(t *testing.T) {
	rs := mustCompile(t, []string{"id", "addr.*"}, []string{"addr.zip"})
	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if !rs.Visible("addr.city") || rs.Visible("addr.zip") {
					t.Error("compiled ruleset changed under concurrency")
					return
				}
				out, err := Project(sampleObject(), rs, nil)
				if err != nil {
					t.Error(err)
					return
				}
				addr := out["addr"].(map[string]any)
				if addr["city"] != "SH" {
					t.Error("unexpected projection under concurrency")
					return
				}
			}
		}()
	}
	wg.Wait()
}
