package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// 第五节：三类故障各有可判定且互不相同的哨兵错误。
func TestSentinelErrors(t *testing.T) {
	f, err := api.New(64, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add([]byte("x")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"m=0", func() error { _, e := api.New(0, 3, 10); return e }(), api.ErrBadParam},
		{"k=0", func() error { _, e := api.New(10, 0, 10); return e }(), api.ErrBadParam},
		{"m<0", func() error { _, e := api.New(-5, 3, 10); return e }(), api.ErrBadParam},
		{"空串Add", f.Add(nil), api.ErrEmpty},
		{"空串Test", func() error { _, e := f.Test(nil); return e }(), api.ErrEmpty},
		{"超容量", f.Add([]byte("y")), api.ErrFull},
	}
	all := []error{api.ErrBadParam, api.ErrEmpty, api.ErrFull}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: err=%v 期望%v", c.name, c.err, c.want)
		}
		for _, other := range all {
			if other != c.want && errors.Is(c.err, other) {
				t.Errorf("%s: err=%v 不应可判定为%v", c.name, c.err, other)
			}
		}
	}
}

// 不变量4：被拒操作不改变任何状态，且过滤器仍可正常使用。
func TestRejectedNoMutation(t *testing.T) {
	f, err := api.New(64, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []string{"a", "b"} {
		if err := f.Add([]byte(x)); err != nil {
			t.Fatal(err)
		}
	}
	before := f.Count()
	if err := f.Add(nil); !errors.Is(err, api.ErrEmpty) {
		t.Fatal("空串未被拒绝")
	}
	if err := f.Add([]byte("c")); !errors.Is(err, api.ErrFull) {
		t.Fatal("超容量未被拒绝")
	}
	if _, err := api.New(0, 0, 0); !errors.Is(err, api.ErrBadParam) {
		t.Fatal("非法参数未被拒绝")
	}
	if f.Count() != before {
		t.Errorf("拒绝后 Count=%d 期望%d", f.Count(), before)
	}
	for _, x := range []string{"a", "b"} {
		if ok, _ := f.Test([]byte(x)); !ok {
			t.Errorf("拒绝后已插入元素 %q 测试为假", x)
		}
	}
	if ok, _ := f.Test([]byte("c")); ok {
		t.Error("被拒的元素 c 不应测出 true（除非假阳性，此处 m=64 两元素不可能）")
	}
}

// SelfCheck 必须真实核验四条不变量并通过。
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 第六节：并发 Add 不同元素、并发只读同一元素、并发 Count/SelfCheck，无 sleep。
func TestConcurrentAddTest(t *testing.T) {
	const n = 128
	f, err := api.New(1<<20, 5, 2*n)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add([]byte("shared")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 3*n)
	for i := 0; i < n; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			if err := f.Add([]byte(fmt.Sprintf("c-%d", i))); err != nil {
				errs <- err
			}
		}(i)
		go func() {
			defer wg.Done()
			ok, err := f.Test([]byte("shared"))
			if err != nil || !ok {
				errs <- fmt.Errorf("并发读 shared: ok=%v err=%v", ok, err)
			}
		}()
		go func() {
			defer wg.Done()
			_ = f.Count()
			if err := api.SelfCheck(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := f.Count(); got != n+1 {
		t.Errorf("Count=%d 期望%d", got, n+1)
	}
	for i := 0; i < n; i++ {
		if ok, _ := f.Test([]byte(fmt.Sprintf("c-%d", i))); !ok {
			t.Errorf("并发 Add 后 c-%d 测试为假", i)
		}
	}
}
