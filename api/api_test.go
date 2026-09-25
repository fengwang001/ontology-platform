package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func feed(t *testing.T, a *api.API, evs ...api.Event) {
	t.Helper()
	if _, err := a.Feed(evs); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReadOnlyView：N 个 goroutine 并发只读同一已喂满实例，
// 视图与丢弃数必须逐字段相同；以 -race 运行必须干净。无 sleep，用 start 通道对齐。
func TestConcurrentReadOnlyView(t *testing.T) {
	a, err := api.New(3, 64)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, a,
		api.Event{Key: "K", TS: 10}, api.Event{Key: "K", TS: 13}, api.Event{Key: "K", TS: 20},
		api.Event{Key: "K", TS: 16}, api.Event{Key: "K", TS: 23}, api.Event{Key: "K", TS: 17},
		api.Event{Key: "K", TS: 25}, api.Event{Key: "K", TS: 11},
		api.Event{Key: "Q", TS: 0}, api.Event{Key: "Q", TS: 7}, api.Event{Key: "Q", TS: 100},
	)
	const n = 64
	views := make([]map[string][]api.Session, n)
	drops := make([]int64, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 同步起跑，制造真实并发而非顺序执行
			for j := 0; j < 4; j++ {
				views[i] = a.View()
				drops[i] = a.Dropped()
				if err := a.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(views[0], views[i]) || drops[0] != drops[i] {
			t.Fatalf("reader %d differs: %v vs %v, %d vs %d", i, views[i], views[0], drops[i], drops[0])
		}
	}
	want := map[string][]api.Session{
		"K": {{Start: 10, End: 13, Count: 2}, {Start: 17, End: 25, Count: 4}},
		// Q 的 0/7 在 wm=25 时到达，互不相邻且出生即闭合；100 推进水位线后开放
		"Q": {{Start: 0, End: 0, Count: 1}, {Start: 7, End: 7, Count: 1}, {Start: 100, End: 100, Count: 1}},
	}
	if !reflect.DeepEqual(views[0], want) || drops[0] != 2 {
		t.Fatalf("view=%v drops=%d", views[0], drops[0])
	}
}

// TestFeedRejectsBatchAtomically：批中任一条非法 → 整批不生效，之后仍可正常使用。
func TestFeedRejectsBatchAtomically(t *testing.T) {
	cases := []struct {
		name string
		bad  []api.Event
		want error
	}{
		{"empty key at head", []api.Event{{Key: "", TS: 5}, {Key: "A", TS: 2}}, api.ErrEmptyKey},
		{"empty key at tail", []api.Event{{Key: "A", TS: 2}, {Key: "", TS: 5}}, api.ErrEmptyKey},
		{"over limit mid", []api.Event{{Key: "A", TS: 2}, {Key: "B", TS: 3}}, api.ErrTooManyOpen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := api.New(3, 1)
			feed(t, a, api.Event{Key: "A", TS: 1})
			before := a.View()
			if _, err := a.Feed(c.bad); !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			if !reflect.DeepEqual(a.View(), before) || a.Dropped() != 0 {
				t.Fatalf("rejected batch left state: %v drops=%d", a.View(), a.Dropped())
			}
			feed(t, a, api.Event{Key: "A", TS: 2}) // 被拒后仍可继续正常使用
			if got := a.View()["A"][0]; got != (api.Session{Start: 1, End: 2, Count: 2}) {
				t.Fatalf("after reuse A=%+v", got)
			}
		})
	}
}

// TestNewRejectsBadGap：gap 非正整体失败，三类哨兵互不相同。
func TestNewRejectsBadGap(t *testing.T) {
	if a, err := api.New(0, 1); !errors.Is(err, api.ErrBadGap) || a != nil {
		t.Fatal("gap=0 must be rejected")
	}
	if a, err := api.New(-3, 1); !errors.Is(err, api.ErrBadGap) || a != nil {
		t.Fatal("gap<0 must be rejected")
	}
	if api.ErrBadGap == api.ErrEmptyKey || api.ErrEmptyKey == api.ErrTooManyOpen {
		t.Fatal("sentinel errors must be distinct")
	}
}

// TestViewIsolation：外部修改返回的 map/切片不得影响内部状态。
func TestViewIsolation(t *testing.T) {
	a, _ := api.New(3, 4)
	feed(t, a, api.Event{Key: "K", TS: 1})
	v := a.View()
	v["K"][0] = api.Session{Start: 9, End: 9, Count: 9}
	delete(v, "K")
	if got := a.View()["K"][0]; got != (api.Session{Start: 1, End: 1, Count: 1}) {
		t.Fatalf("internal state leaked: %+v", got)
	}
}

// TestSelfCheck：对外方法型自检必须通过。
func TestSelfCheck(t *testing.T) {
	a, err := api.New(3, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
