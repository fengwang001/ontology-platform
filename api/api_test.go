package api

import (
	"fmt"
	"maps"
	"sync"
	"testing"
)

func TestBatchEquivalence(t *testing.T) {
	cases := map[string]seq{
		"seven": selfSeq,
		"generated": func() seq {
			var s seq
			for i := 1; i <= 300; i++ { // 循环生成多写混合序列
				k := fmt.Sprintf("k%d", i%17)
				switch i % 4 {
				case 0:
					s = append(s, wr{op: 4, k: k, ver: int64(i)})
				case 1:
					s = append(s, wr{op: 0, k: k, v: fmt.Sprintf("v%d", i), ver: int64(i)})
				case 2:
					s = append(s, wr{op: 3, k: k, v: fmt.Sprintf("v%d", i), ver: int64(i)})
				case 3:
					s = append(s, wr{op: 5, k: k, ver: int64(i)})
				}
			}
			return s
		}(),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			a := New()
			okWrites, err := a.run(s)
			if err != nil {
				t.Fatal(err)
			}
			a.Reconcile()
			if want := batch(okWrites); !maps.Equal(a.View(), want) {
				t.Fatalf("view=%v want %v", a.View(), want)
			}
		})
	}
}

func TestConcurrentView(t *testing.T) {
	a := New()
	if _, err := a.run(selfSeq); err != nil {
		t.Fatal(err)
	}
	a.Reconcile()
	want := a.View()
	const n = 32
	views := make([]map[string]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ { // 并发只读 + 对账 + 自检，无 sleep
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a.Reconcile()
			views[i] = a.View()
			if err := a.SelfCheck(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if !maps.Equal(views[i], want) {
			t.Fatalf("goroutine %d view=%v want %v", i, views[i], want)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	for i := 0; i < 3; i++ {
		if err := New().SelfCheck(); err != nil {
			t.Fatal(err)
		}
	}
}
