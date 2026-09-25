package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/elect"
	"ontology/ring"
)

func shuffledIDs(r *rand.Rand, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("id%07d", i)
	}
	r.Shuffle(n, func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	return ids
}

func build(t *testing.T, ids []string) *api.System {
	s := api.New()
	for _, id := range ids {
		if err := s.AddNode(id); err != nil {
			t.Fatalf("add %q: %v", id, err)
		}
	}
	return s
}

// 不变量 1+2：多档规模、随机序 ID 下 Elect 必返回，胜者 == 朴素最大，消息数 ≤ n²。
func TestElectMatchesNaiveMax(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for _, n := range []int{1, 2, 3, 4, 7, 16, 64, 200} {
		ids := shuffledIDs(r, n)
		winner, msgs, err := build(t, ids).Elect()
		if err != nil {
			t.Fatalf("n=%d elect: %v", n, err)
		}
		if want := slices.Max(ids); winner != want {
			t.Fatalf("n=%d: winner %q != naive max %q", n, winner, want)
		}
		if msgs > n*n {
			t.Fatalf("n=%d: messages %d exceed bound %d", n, msgs, n*n)
		}
	}
}

// 第三节推导：环 3→7→2→5，胜者 7，消息数 8，八行轨迹逐行一致。
func TestCanonicalElect(t *testing.T) {
	rg := ring.New()
	for _, id := range []string{"3", "7", "2", "5"} {
		_ = rg.Add(id)
	}
	res, err := elect.Elect(rg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Winner != "7" || res.Messages != 8 {
		t.Fatalf("winner=%q messages=%d, want 7/8", res.Winner, res.Messages)
	}
	want := []elect.Event{
		{At: "7", Msg: "3", From: "3", Action: "discard"}, {At: "2", Msg: "7", From: "7", Action: "forward", To: "5"},
		{At: "5", Msg: "2", From: "2", Action: "discard"}, {At: "3", Msg: "5", From: "5", Action: "forward", To: "7"},
		{At: "5", Msg: "7", From: "2", Action: "forward", To: "3"}, {At: "7", Msg: "5", From: "3", Action: "discard"},
		{At: "3", Msg: "7", From: "5", Action: "forward", To: "7"}, {At: "7", Msg: "7", From: "3", Action: "declare"},
	}
	if !reflect.DeepEqual(res.Trace, want) {
		t.Fatalf("trace = %+v, want %+v", res.Trace, want)
	}
}

// 不变量 4：三类故障注入错误互不相同，被拒后状态不变且系统仍可用。
func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	s := build(t, []string{"a", "b", "c"})
	ops := []struct {
		op   func() error
		want error
	}{
		{func() error { return s.AddNode("") }, api.ErrEmptyID},
		{func() error { return s.AddNode("a") }, api.ErrDupID},
		{func() error { return s.RemoveNode("zzz") }, api.ErrNotFound},
	}
	var errs []error
	for _, o := range ops {
		err := o.op()
		if !errors.Is(err, o.want) {
			t.Fatalf("got %v, want %v", err, o.want)
		}
		errs = append(errs, err)
	}
	if errs[0] == errs[1] || errs[1] == errs[2] || errs[0] == errs[2] {
		t.Fatalf("fault-injection errors not distinct: %v", errs)
	}
	if s.Size() != 3 {
		t.Fatalf("size = %d after rejections, want 3", s.Size())
	}
	if w, _, err := s.Elect(); err != nil || w != "c" {
		t.Fatalf("elect after rejections = %q, %v; want c, nil", w, err)
	}
}

// 并发：64 个 goroutine 并发 Elect 胜者相同；并发 AddNode 后胜者仍为最大者。
func TestConcurrentElect(t *testing.T) {
	ids := shuffledIDs(rand.New(rand.NewSource(7)), 50)
	s := build(t, ids)
	want := slices.Max(ids)
	var wg sync.WaitGroup
	winners := make([]string, 64)
	for k := range winners {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			if w, _, err := s.Elect(); err == nil {
				winners[k] = w
			}
		}(k)
	}
	wg.Wait()
	for k, w := range winners {
		if w != want {
			t.Fatalf("goroutine %d: winner %q != %q", k, w, want)
		}
	}
	extra := shuffledIDs(rand.New(rand.NewSource(99)), 16)
	for _, id := range extra {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = s.AddNode("x" + id)
		}(id)
	}
	wg.Wait()
	// "x" 前缀恒大于 "id" 前缀，胜者必为并发加入的最大者。
	if w, _, err := s.Elect(); err != nil || w != "x"+slices.Max(extra) {
		t.Fatalf("after concurrent adds: winner %q, err %v", w, err)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
