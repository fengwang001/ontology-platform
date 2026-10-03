package plan

import (
	"sync"
	"testing"

	"ontology/diff"
	"ontology/norm"
)

// TestConcurrentSmoke 在 -race 下验证所有操作可并发调用且不发生数据竞争；
// 多个修复员反复“生成计划→应用（失败则重试）”，最终 Compare 必须收敛。
func TestConcurrentSmoke(t *testing.T) {
	e, err := diff.New(2, norm.RMHalfEven, true)
	if err != nil {
		t.Fatal(err)
	}
	const ids = 200
	for id := int64(1); id <= ids; id++ {
		v := id * 10000
		if err := e.SrcPut(diff.Row{ID: id, D: &v, C: []byte("v ")}); err != nil {
			t.Fatal(err)
		}
		if id%2 == 0 {
			z := v + 1
			if err := e.TgtPut(diff.Row{ID: id, D: &z, C: []byte("v")}); err != nil {
				t.Fatal(err)
			}
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 干扰写者：不断覆写/删除/恢复目标行，制造 ErrStale。
	wg.Add(1)
	go func() {
		defer wg.Done()
		seed := int64(100)
		for {
			select {
			case <-stop:
				return
			default:
			}
			id := (seed % ids) + 1
			seed += 7
			switch seed % 3 {
			case 0:
				_ = e.TgtPut(diff.Row{ID: id, D: &seed, C: []byte("w ")})
			case 1:
				_ = e.TgtDel(id)
			default:
				_ = e.TgtPut(diff.Row{ID: id, D: nil, C: nil})
			}
		}
	}()

	// 只读 Compare 调用者。
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if _, err := e.Compare(1, ids+1); err != nil {
					t.Errorf("concurrent Compare: %v", err)
					return
				}
				_ = e.LastVisitCount()
			}
		}()
	}

	// 修复员：Build→Apply 循环；一旦成功（无干扰窗口）则计划原子落库。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 500; j++ {
			p, err := Build(e, 1, ids+1, false)
			if err != nil {
				t.Errorf("concurrent Build: %v", err)
				return
			}
			_, _, _, _ = Apply(e, RoleRepairer, p) // ErrStale 属正常
		}
	}()

	close(stop)
	wg.Wait()

	// 停止干扰后，反复修复直到收敛（有界重试）。
	for attempt := 0; attempt < 50; attempt++ {
		p, _ := Build(e, 1, ids+1, false)
		if len(p.Items) == 0 {
			break
		}
		_, _, _, err := Apply(e, RoleRepairer, p)
		if err != nil {
			continue
		}
	}
	rs, _ := e.Compare(1, ids+1)
	for _, r := range rs {
		if r.Kind == diff.Missing || r.Kind == diff.Changed {
			t.Fatalf("concurrent repair did not converge: id %d kind %d", r.ID, r.Kind)
		}
	}
}
