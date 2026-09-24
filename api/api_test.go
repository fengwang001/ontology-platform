package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/snapchain"
)

// 不变量 1：随机提交与随机 N、T 下，保留快照与删除文件逐步等于朴素参照；
// 不变量 2、3：每步校验文件存储恰好等于现存快照文件集的并集。
func TestRandomMatchesNaive(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		g, rng := api.New(), rand.New(rand.NewSource(seed))
		var cur []string
		ts := int64(0)
		for i := 0; i < 300; i++ {
			if i == 0 || rng.Intn(3) > 0 {
				ts += 1 + rng.Int63n(9)
				add := fmt.Sprintf("f%d", i)
				var rmv []string
				if len(cur) > 0 && rng.Intn(2) == 0 {
					j := rng.Intn(len(cur))
					rmv = []string{cur[j]}
					cur = append(cur[:j], cur[j+1:]...)
				}
				if _, err := g.Commit(ts, []string{add}, rmv); err != nil {
					t.Fatal(err)
				}
				cur = append(cur, add)
				sort.Strings(cur)
				snaps := g.Snapshots()
				if got := snaps[len(snaps)-1].Files; fmt.Sprint(got) != fmt.Sprint(cur) {
					t.Fatalf("seed=%d i=%d: commit files=%v", seed, i, got)
				}
			} else {
				num, tt := rng.Intn(4), ts-rng.Int63n(30)
				wantKept, wantDel := snapchain.NaiveExpire(g.Snapshots(), num, tt)
				del, err := g.Expire(num, tt)
				if err != nil || fmt.Sprint(del) != fmt.Sprint(wantDel) || fmt.Sprint(g.Snapshots()) != fmt.Sprint(wantKept) {
					t.Fatalf("seed=%d i=%d: expire del=%v err=%v", seed, i, del, err)
				}
			}
			union := map[string]bool{}
			for _, sn := range g.Snapshots() {
				for _, f := range sn.Files {
					union[f] = true
				}
			}
			if len(union) != len(g.Files()) {
				t.Fatalf("seed=%d i=%d: leak or unreadable", seed, i)
			}
			for _, f := range g.Files() {
				if !union[f] {
					t.Fatalf("seed=%d i=%d: leak or unreadable", seed, i)
				}
			}
		}
	}
}

// 不变量 4：四类可判定错误互不相同，被拒后状态不变且仍可正常使用。
func TestFaultInjection(t *testing.T) {
	if del, err := api.New().Expire(0, 0); err != nil || len(del) != 0 {
		t.Fatal("空表 Expire 应幂等成功、不删除任何东西")
	}
	cm := func(g *api.API, ts int64, add, rm []string) error { _, e := g.Commit(ts, add, rm); return e }
	cases := []struct {
		name string
		op   func(g *api.API) error
		want error
	}{
		{"N为负", func(g *api.API) error { _, e := g.Expire(-1, 0); return e }, api.ErrInvalidParam},
		{"ts不递增", func(g *api.API) error { return cm(g, 10, []string{"x"}, nil) }, api.ErrNonMonotonicTS},
		{"add重复", func(g *api.API) error { return cm(g, 20, []string{"x", "x"}, nil) }, api.ErrNameConflict},
		{"add曾出现", func(g *api.API) error { return cm(g, 20, []string{"a"}, nil) }, api.ErrNameConflict},
		{"addremove相交", func(g *api.API) error { return cm(g, 20, []string{"a"}, []string{"a"}) }, api.ErrNameConflict},
		{"remove不在当前", func(g *api.API) error { return cm(g, 20, []string{"x"}, []string{"zz"}) }, api.ErrNotInCurrent},
	}
	if api.ErrInvalidParam == api.ErrNonMonotonicTS || api.ErrInvalidParam == api.ErrNameConflict ||
		api.ErrInvalidParam == api.ErrNotInCurrent || api.ErrNonMonotonicTS == api.ErrNameConflict ||
		api.ErrNonMonotonicTS == api.ErrNotInCurrent || api.ErrNameConflict == api.ErrNotInCurrent {
		t.Fatal("哨兵错误不互不相同")
	}
	for _, c := range cases {
		g := api.New()
		cm(g, 10, []string{"a", "b"}, nil)
		before := fmt.Sprint(g.Snapshots(), g.Files())
		if err := c.op(g); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if fmt.Sprint(g.Snapshots(), g.Files()) != before || cm(g, 30, []string{"ok"}, nil) != nil {
			t.Fatalf("%s: 拒绝后状态改变或不可用", c.name)
		}
	}
}

// 并发：一个写 goroutine 连续提交并周期性过期，多个读 goroutine 并发读
// Snapshots/Files/SelfCheck；双读校验两次读之间无写时状态一致。
func TestConcurrentReads(t *testing.T) {
	g := api.New()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s1 := g.Snapshots()
				have := map[string]bool{}
				for _, f := range g.Files() {
					have[f] = true
				}
				if fmt.Sprint(s1) != fmt.Sprint(g.Snapshots()) {
					continue
				}
				for _, sn := range s1 {
					for _, f := range sn.Files {
						if !have[f] {
							t.Error("读到的快照引用了不在文件集合里的文件")
							return
						}
					}
				}
				if err := g.SelfCheck(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 300; i++ {
		g.Commit(int64(i+1), []string{fmt.Sprintf("c%d", i)}, nil)
		if i%7 == 6 {
			g.Expire(2, int64(i))
		}
	}
	close(stop)
	wg.Wait()
}
