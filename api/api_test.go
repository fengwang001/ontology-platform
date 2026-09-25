package api_test

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"ontology/api"
)

// TestFailuresDistinctAndNoTrace 不变量3、4：四类失败错误可判定、互不相同、不留痕，之后可正常使用。
func TestFailuresDistinctAndNoTrace(t *testing.T) {
	y, err := api.New(3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ { // 打满容量
		if err := y.Apply("K", fmt.Sprintf("v%d", i), int64(10*i), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	_, eBad := api.New(0)
	ops := []struct {
		name string
		err  error
		want error
	}{
		{"参数非法", eBad, api.ErrBadConfig},
		{"空Key", y.Apply("", "x", 1, 99), api.ErrEmptyKey},
		{"In不递增-相等", y.Apply("K", "x", 1, 3), api.ErrNonMonotonicIngest},
		{"In不递增-倒退", y.Apply("K", "x", 1, 1), api.ErrNonMonotonicIngest},
		{"超容量", y.Apply("K", "x", 1, 4), api.ErrCapacity},
	}
	for _, o := range ops {
		if !errors.Is(o.err, o.want) {
			t.Fatalf("%s: 得到 %v, 期望 %v", o.name, o.err, o.want)
		}
		for _, p := range ops { // 互不相同
			if o.name != p.name && errors.Is(o.err, p.want) && o.want != p.want {
				t.Fatalf("%s 与 %s 的错误不可区分", o.name, p.name)
			}
		}
	}
	le, _ := y.LatestEvent("K")
	li, _ := y.LatestIngest("K")
	if le.Value != "v3" || le.Ev != 30 || li.In != 3 { // 状态与打满时一致
		t.Fatalf("被拒操作改变了状态: %+v %+v", le, li)
	}
	z, _ := api.New(2) // 被拒后系统仍可正常使用；负 Ev 合法
	if err := z.Apply("K", "a", -5, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := z.LatestEvent("K"); got.Ev != -5 {
		t.Fatalf("负 Ev 未被接受: %+v", got)
	}
	if _, err := z.LatestEvent("不存在"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("未知 key 应返回 ErrNotFound: %v", err)
	}
}

// TestConcurrent 并发：N 个不同 key 各写一版 + M 个 goroutine 对同一 key 写严格递增
// In（CAS 重试，不用 sleep）+ 并发只读不 panic；结束后校验各 key 状态。
func TestConcurrent(t *testing.T) {
	y, err := api.New(64)
	if err != nil {
		t.Fatal(err)
	}
	const keys, writers = 8, 32
	var wWG, rWG sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ { // 并发只读
		rWG.Add(1)
		go func() {
			defer rWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					y.LatestEvent("S")
					y.LatestIngest("S")
					y.AtEvent("S", 10)
					y.AtIngest("S", 1<<60)
					api.SelfCheck()
				}
			}
		}()
	}
	for g := 0; g < keys; g++ { // 不同 key 各一版
		wWG.Add(1)
		go func(g int) {
			defer wWG.Done()
			if err := y.Apply(fmt.Sprintf("k%d", g), "v", 1, 1); err != nil {
				t.Error(err)
			}
		}(g)
	}
	for j := 0; j < writers; j++ { // 同一 key，CAS 式取 maxIn+1 写入
		wWG.Add(1)
		go func(j int) {
			defer wWG.Done()
			for {
				li, _ := y.LatestIngest("S")
				err := y.Apply("S", fmt.Sprintf("v%d", j), int64(j), li.In+1)
				if err == nil {
					return
				}
				if !errors.Is(err, api.ErrNonMonotonicIngest) {
					t.Error(err)
					return
				}
				runtime.Gosched()
			}
		}(j)
	}
	wWG.Wait()
	close(stop)
	rWG.Wait()
	li, err := y.LatestIngest("S") // 同 key：writers 个版本全部写入，In 序列 1..writers 严格递增
	if err != nil || li.In != writers {
		t.Fatalf("LatestIngest.In=%d, 期望 %d", li.In, writers)
	}
	for in := int64(1); in <= writers; in++ {
		if v, err := y.AtIngest("S", in); err != nil || v.In != in {
			t.Fatalf("AtIngest(%d)=%+v,%v", in, v, err)
		}
	}
	for g := 0; g < keys; g++ { // 不同 key 各一版，In=1
		if v, err := y.LatestIngest(fmt.Sprintf("k%d", g)); err != nil || v.In != 1 {
			t.Fatalf("k%d: %+v,%v", g, v, err)
		}
	}
}
