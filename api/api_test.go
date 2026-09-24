package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

func feed(t *testing.T, a *api.API, evs ...api.Event) {
	t.Helper()
	if err := a.Feed(evs); err != nil {
		t.Fatalf("feed %v: %v", evs, err)
	}
}

func TestSelfCheck(t *testing.T) {
	a, err := api.New(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil { // 内置序列核验四条不变量
		t.Fatalf("SelfCheck: %v", err)
	}
	// SelfCheck 在独立引擎上运行，接收者状态保持初值。
	if a.Sum() != 0 {
		t.Errorf("SelfCheck mutated receiver: sum=%d", a.Sum())
	}
}

func TestEightEventsViaAPI(t *testing.T) { // 第三节八事件经对外接口的结果
	a, _ := api.New(2)
	evs := []api.Event{
		{api.Rec, 0, 5}, {api.Rec, 1, 10}, {api.Bar, 0, 1}, {api.Rec, 0, 7},
		{api.Rec, 1, 3}, {api.Bar, 1, 1}, {api.Rec, 0, 2}, {api.Rec, 1, 1},
	}
	want := []int{5, 15, 15, 15, 18, 25, 27, 28}
	for i, ev := range evs {
		feed(t, a, ev)
		if a.Sum() != want[i] {
			t.Fatalf("step %d: sum=%d want %d", i+1, a.Sum(), want[i])
		}
	}
	if v, ok := a.Snapshot(1); !ok || v != 18 {
		t.Fatalf("snap[1]=%d ok=%v want 18", v, ok)
	}
}

func TestSentinelErrorsViaAPI(t *testing.T) { // 四类错误经公开接口可判定且互不相同
	if _, err := api.New(0); !errors.Is(err, api.ErrInvalidChannels) {
		t.Errorf("New(0): %v", err)
	}
	a, _ := api.New(2)
	cases := []struct {
		bad  []api.Event
		want error
	}{
		{[]api.Event{{api.Rec, -1, 1}}, api.ErrChannelOutOfRange},
		{[]api.Event{{api.Bar, 9, 1}}, api.ErrChannelOutOfRange},
		{[]api.Event{{api.Bar, 0, 0}}, api.ErrBarrierNonPositive},
		{[]api.Event{{api.Bar, 0, -2}}, api.ErrBarrierNonPositive},
		{[]api.Event{{api.Bar, 0, 1}, {api.Bar, 0, 1}}, api.ErrBarrierOutOfOrder},
	}
	for i, tc := range cases {
		if err := a.Feed(tc.bad); !errors.Is(err, tc.want) {
			t.Errorf("case %d: err=%v want %v", i, err, tc.want)
		}
		if a.Sum() != 0 {
			t.Errorf("case %d: sum=%d want 0 (batch left trace)", i, a.Sum())
		}
	}
}

func TestRejectedBatchAtomicViaAPI(t *testing.T) { // 整批中任一非法：sum/快照/缓冲全不变
	a, _ := api.New(2)
	feed(t, a,
		api.Event{api.Rec, 0, 5}, api.Event{api.Rec, 1, 10},
		api.Event{api.Bar, 0, 1}, api.Event{api.Rec, 0, 7},
		api.Event{api.Rec, 1, 3}) // 通道1未栅：+3 立即入状态；sum=18，+7 在缓冲
	err := a.Feed([]api.Event{{api.Rec, 0, 999}, {api.Bar, 1, 0}})
	if !errors.Is(err, api.ErrBarrierNonPositive) {
		t.Fatalf("err=%v", err)
	}
	if a.Sum() != 18 {
		t.Fatalf("sum=%d want 18", a.Sum())
	}
	if _, ok := a.Snapshot(1); ok {
		t.Fatal("phantom snapshot after rejected batch")
	}
	feed(t, a, api.Event{api.Bar, 1, 1}) // 缓冲只含 +7（不含+999）：snap=18，排空后 25
	if v, _ := a.Snapshot(1); v != 18 || a.Sum() != 25 {
		t.Fatalf("snap=%d sum=%d, want 18/25", v, a.Sum())
	}
}

func TestConcurrentReaders(t *testing.T) { // 喂满含一次对齐后，N 个只读 goroutine 结果逐字段相同
	a, _ := api.New(3)
	feed(t, a,
		api.Event{api.Rec, 0, 1}, api.Event{api.Rec, 1, 2}, api.Event{api.Rec, 2, 3},
		api.Event{api.Bar, 0, 1}, api.Event{api.Bar, 1, 1}, api.Event{api.Bar, 2, 1},
		api.Event{api.Rec, 0, 10})
	const n = 64
	var wg sync.WaitGroup
	type res struct {
		sum, snap int
		ok        bool
	}
	got := make([]res, n)
	start := make(chan struct{}) // 不用 sleep：关闸放行后所有 goroutine 同时读
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s := a.Sum()
			v, ok := a.Snapshot(1)
			got[i] = res{s, v, ok}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatalf("reader %d got %+v, reader 0 got %+v", i, got[i], got[0])
		}
	}
	if got[0].sum != 16 || got[0].snap != 6 || !got[0].ok {
		t.Fatalf("values %+v, want sum=16 snap=6", got[0])
	}
}
