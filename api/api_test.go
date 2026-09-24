package api_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/seg"
)

// TestSequenceGets 不变量 2：第三节 17 步序列的 Get 必须等于逐步推导的答案。
func TestSequenceGets(t *testing.T) {
	e, _ := api.New(2, 2, 1)
	var gets []string
	for _, tok := range strings.Fields("pa1 pb2 pc3 gc pa9 pd4 db pe5 gb pf6 pg7 gb ga pb8 ph1 gb gh") {
		switch tok[0] {
		case 'p':
			_ = e.Put(tok[1:2], tok[2:])
		case 'd':
			_ = e.Del(tok[1:2])
		case 'g':
			v, ok := e.Get(tok[1:2])
			if !ok {
				v = "-"
			}
			gets = append(gets, v)
		}
	}
	if fmt.Sprint(gets) != fmt.Sprint([]string{"3", "-", "-", "9", "8", "1"}) { // 第 4/9/12/13/16/17 步
		t.Fatalf("gets=%v", gets)
	}
}

// TestReferenceConsistency 不变量 1：多档参数 × 多个随机种子，与朴素 map 参照逐键一致。
func TestReferenceConsistency(t *testing.T) {
	cfgs := [][3]int{{2, 2, 1}, {3, 3, 2}, {1, 2, 3}, {5, 4, 2}}
	for _, c := range cfgs {
		for seed := uint64(1); seed <= 3; seed++ {
			e, _ := api.New(c[0], c[1], c[2])
			ref, s := map[string]string{}, seed
			rnd := func() uint64 { s = s*6364136223846793005 + 1442695040888963407; return s >> 33 }
			for i := 0; i < 800; i++ {
				k := strconv.Itoa(int(rnd() % 25))
				if rnd()%3 == 0 {
					_ = e.Del(k)
					delete(ref, k)
					continue
				}
				v := strconv.Itoa(int(rnd() % 1000))
				_ = e.Put(k, v)
				ref[k] = v
			}
			for i := 0; i < 30; i++ {
				k := strconv.Itoa(i)
				v, ok := e.Get(k)
				rv, rok := ref[k]
				if ok != rok || (ok && v != rv) {
					t.Fatalf("cfg=%v seed=%d key=%s: got %q,%v want %q,%v", c, seed, k, v, ok, rv, rok)
				}
			}
		}
	}
}

// TestRejectedOps 不变量 4：三类错误互不相同，被拒操作不改状态，之后可正常使用。
func TestRejectedOps(t *testing.T) {
	for _, p := range [][3]int{{0, 2, 1}, {2, 0, 1}, {2, 1, 1}, {-1, 2, 1}, {2, 2, 0}} {
		if _, err := api.New(p[0], p[1], p[2]); !errors.Is(err, api.ErrParam) {
			t.Fatalf("New%v: err=%v, want ErrParam", p, err)
		}
	}
	e, _ := api.New(2, 2, 1)
	_ = e.Put("a", "1")
	if err := e.Put("", "x"); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("Put empty key: %v", err)
	}
	if err := e.Del(""); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("Del empty key: %v", err)
	}
	corrupts := [][]byte{{1, 'k', 9, 0}, {1}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1}}
	for _, b := range corrupts {
		if _, _, err := seg.LoadSegment(b); !errors.Is(err, seg.ErrCorrupt) {
			t.Fatalf("LoadSegment(%v): want ErrCorrupt", b)
		}
	}
	if api.ErrParam == api.ErrEmptyKey || api.ErrParam == api.ErrCorrupt || api.ErrEmptyKey == api.ErrCorrupt {
		t.Fatal("三类错误必须互不相同")
	}
	if v, ok := e.Get("a"); !ok || v != "1" {
		t.Fatalf("被拒操作改变了状态: %q,%v", v, ok)
	}
	if err := e.Put("b", "2"); err != nil {
		t.Fatalf("拒绝后不可继续使用: %v", err)
	}
}

// TestConcurrentGet 不变量 2：并发读到的值必须属于已写集合，终值等于最后写入；无 sleep。
func TestConcurrentGet(t *testing.T) {
	e, _ := api.New(2, 2, 1)
	const writes = 3000
	done := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int64
	loop := func(f func()) { // 并发执行 f 直到写入结束
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			f()
		}
	}
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go loop(func() {
			if v, ok := e.Get("a"); ok {
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 || n >= writes {
					bad.Add(1)
				}
			}
		})
	}
	wg.Add(1)
	go loop(func() { // SelfCheck 与读写并发也必须安全
		if err := e.SelfCheck(); err != nil {
			bad.Add(1)
		}
	})
	for i := 0; i < writes; i++ {
		if err := e.Put("a", strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatalf("%d 次读到非法值或自检失败", bad.Load())
	}
	if v, ok := e.Get("a"); !ok || v != strconv.Itoa(writes-1) {
		t.Fatalf("终值=%q,%v, want %d,true", v, ok, writes-1)
	}
}
