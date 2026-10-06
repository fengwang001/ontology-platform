package inline

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func sampleFuncs() []Function {
	return []Function{
		{Name: "F", Size: 100, CallSites: []CallSite{
			{Callee: "G", Hotness: 0.9}, {Callee: "H", Hotness: 0.8}}},
		{Name: "G", Size: 60, CallSites: []CallSite{{Callee: "F", Hotness: 1}}},
		{Name: "H", Size: 20},
	}
}

func sampleRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	for _, f := range sampleFuncs() {
		if err := reg.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// 决策任务进行期间修改请求被拒绝；任务完成后修改生效；
// 已完成任务的快照不受后续修改影响。
func TestModificationRejectedDuringDecision(t *testing.T) {
	reg := sampleRegistry(t)
	sess := reg.Begin()
	if err := reg.Add(Function{Name: "X", Size: 1}); !errors.Is(err, ErrBusy) {
		t.Fatalf("决策期间 Add 应返回 ErrBusy, 实际: %v", err)
	}
	if err := reg.Upsert(Function{Name: "F", Size: 999}); !errors.Is(err, ErrBusy) {
		t.Fatalf("决策期间 Upsert 应返回 ErrBusy, 实际: %v", err)
	}
	sess.Close()
	if err := reg.Upsert(Function{Name: "F", Size: 999}); err != nil {
		t.Fatalf("会话关闭后修改应被接受, 实际: %v", err)
	}
	sess2 := reg.Begin()
	defer sess2.Close()
	if f, _ := sess2.Program().Lookup("F"); f.Size != 999 {
		t.Fatalf("修改应对后续会话生效, F.Size = %d", f.Size)
	}
	if f, _ := sess.Program().Lookup("F"); f.Size != 100 {
		t.Fatalf("已完成任务的快照被后续修改污染, F.Size = %d", f.Size)
	}
}

// 多个独立决策任务并发执行，结果与顺序执行完全一致，互不影响。
func TestConcurrentSessionsIsolation(t *testing.T) {
	reg := sampleRegistry(t)
	cfgA := Config{CallOverhead: 10, GrowthNum: 1, GrowthDen: 1, MaxSize: 1000, MaxChainRepeat: 2}
	cfgB := Config{CallOverhead: 0, GrowthNum: 5, GrowthDen: 1, MaxSize: 100000, MaxChainRepeat: 3}
	sess1, sess2 := reg.Begin(), reg.Begin()
	defer sess1.Close()
	defer sess2.Close()
	var gotA1, gotA2, gotB *Report
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		r, err := Decide(sess1, cfgA)
		if err != nil {
			t.Error(err)
		}
		gotA1 = r
	}()
	go func() {
		defer wg.Done()
		r, err := Decide(sess1, cfgA)
		if err != nil {
			t.Error(err)
		}
		gotA2 = r
	}()
	go func() {
		defer wg.Done()
		r, err := Decide(sess2, cfgB)
		if err != nil {
			t.Error(err)
		}
		gotB = r
	}()
	wg.Wait()
	s3 := reg.Begin()
	wantA, err := Decide(s3, cfgA)
	if err != nil {
		t.Fatal(err)
	}
	s3.Close()
	s4 := reg.Begin()
	wantB, err := Decide(s4, cfgB)
	if err != nil {
		t.Fatal(err)
	}
	s4.Close()
	if !reflect.DeepEqual(gotA1, wantA) || !reflect.DeepEqual(gotA2, wantA) {
		t.Fatal("并发执行的同配置任务结果与顺序执行不一致")
	}
	if !reflect.DeepEqual(gotB, wantB) {
		t.Fatal("并发执行的异配置任务结果与顺序执行不一致")
	}
}

// 多个相互独立的决策任务（各自注册表）并发执行，结果一致。
func TestConcurrentIndependentTasks(t *testing.T) {
	cfg := Config{CallOverhead: 10, GrowthNum: 2, GrowthDen: 1, MaxSize: 10000, MaxChainRepeat: 2}
	reg := sampleRegistry(t)
	sess := reg.Begin()
	want, err := Decide(sess, cfg)
	sess.Close()
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	errs := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := NewRegistry()
			for _, f := range sampleFuncs() {
				if err := r.Add(f); err != nil {
					errs <- err.Error()
					return
				}
			}
			s := r.Begin()
			defer s.Close()
			rep, err := Decide(s, cfg)
			if err != nil {
				errs <- err.Error()
				return
			}
			if !reflect.DeepEqual(rep, want) {
				errs <- "并发任务结果与参照不一致"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
