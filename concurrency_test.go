package ontology_test

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology"
)

// 并发调用安全性：所有操作共享同一 now（时钟不因交错而回退），
// 变更操作作用于互不相交的患者集合（与顺序无关地可交换），
// 因此并发执行的结果必等价于某个串行顺序。
// 运行 go test -race 以检测数据竞争。
func TestConcurrentAccess(t *testing.T) {
	const now = 5000
	e := ontology.NewEngine()
	for _, op := range []func() error{
		func() error { return e.BackfillStay("P", "WH", 1000, 5000, now) },
		func() error { return e.BackfillStay("X", "WH", 1100, 1300, now) },
		func() error { return e.RegisterCase("C-HOT", "P", 2000, now) },
	} {
		if err := op(); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker*2)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				p := fmt.Sprintf("G%d-P%d", g, i)
				w := fmt.Sprintf("G%d-W", g)
				if err := e.BackfillStay(p, w, 10, 500, now); err != nil {
					errs <- fmt.Errorf("backfill %s: %w", p, err)
				}
				if _, err := e.Status("X", now); err != nil {
					errs <- fmt.Errorf("status: %w", err)
				}
				if _, err := e.CaseContacts("C-HOT", now); err != nil {
					errs <- fmt.Errorf("contacts: %w", err)
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// 与串行执行相同操作序列的参照引擎对比最终查询结果
	ref := ontology.NewEngine()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(ref.BackfillStay("P", "WH", 1000, 5000, now))
	must(ref.BackfillStay("X", "WH", 1100, 1300, now))
	must(ref.RegisterCase("C-HOT", "P", 2000, now))
	for g := 0; g < workers; g++ {
		for i := 0; i < perWorker; i++ {
			must(ref.BackfillStay(fmt.Sprintf("G%d-P%d", g, i), fmt.Sprintf("G%d-W", g), 10, 500, now))
		}
	}
	for _, p := range []string{"X", "P", "G0-P0", "G7-P49"} {
		sGot, err1 := e.Status(p, now)
		sRef, err2 := ref.Status(p, now)
		if err1 != nil || err2 != nil {
			t.Fatalf("status errors: %v %v", err1, err2)
		}
		if !reflect.DeepEqual(sGot, sRef) {
			t.Fatalf("并发与串行结果不一致 Status(%s):\n并发 %+v\n串行 %+v", p, sGot, sRef)
		}
	}
	cGot, _ := e.CaseContacts("C-HOT", now)
	cRef, _ := ref.CaseContacts("C-HOT", now)
	if !reflect.DeepEqual(cGot, cRef) {
		t.Fatalf("并发与串行清单不一致:\n并发 %+v\n串行 %+v", cGot, cRef)
	}
}
