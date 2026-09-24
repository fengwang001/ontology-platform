package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// TestSelfCheck：内置序列须核验通过，且不污染接收者状态。
func TestSelfCheck(t *testing.T) {
	d, err := api.New(10, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	before := d.Mem()
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if len(d.Emitted()) != 0 || d.Dups() != 0 || !reflect.DeepEqual(d.Mem(), before) {
		t.Errorf("SelfCheck 污染了接收者: em=%v dups=%d mem=%v", d.Emitted(), d.Dups(), d.Mem())
	}
}

// TestAPIErrors：三类错误经公开接口可判定、互不相同，被拒后状态不变、仍可使用。
func TestAPIErrors(t *testing.T) {
	if _, e := api.New(0, 1, 1); !errors.Is(e, api.ErrInvalidParam) {
		t.Errorf("参数非法: %v", e)
	}
	d, _ := api.New(10, 2, 1)
	if _, e := d.Feed([]api.Event{{ID: "", TS: 1}}); !errors.Is(e, api.ErrEmptyID) {
		t.Errorf("空 ID: %v", e)
	}
	if _, e := d.Feed([]api.Event{{ID: "a", TS: 1}, {ID: "b", TS: 1}}); !errors.Is(e, api.ErrTooMany) {
		t.Errorf("超限: %v", e)
	}
	if errors.Is(api.ErrEmptyID, api.ErrTooMany) || errors.Is(api.ErrEmptyID, api.ErrInvalidParam) ||
		errors.Is(api.ErrTooMany, api.ErrInvalidParam) {
		t.Fatal("三类哨兵错误不互异")
	}
	if len(d.Emitted()) != 0 || d.Dups() != 0 || len(d.Mem()) != 0 {
		t.Errorf("被拒后状态被改变: em=%v dups=%d mem=%v", d.Emitted(), d.Dups(), d.Mem())
	}
	if _, e := d.Feed([]api.Event{{ID: "a", TS: 1}}); e != nil {
		t.Errorf("拒绝后实例不可用: %v", e)
	}
}

// TestConcurrentReadOnly：N 个 goroutine 并发只读同一实例，结果逐字段一致；无 sleep。
func TestConcurrentReadOnly(t *testing.T) {
	d, _ := api.New(10, 2, 1_000_000)
	rng := rand.New(rand.NewSource(7))
	ts := int64(0)
	for i := 0; i < 500; i++ {
		ts += int64(rng.Intn(11)) - 2 // 含迟到事件
		if _, e := d.Feed([]api.Event{{ID: fmt.Sprintf("id%d", rng.Intn(10)), TS: ts}}); e != nil {
			t.Fatal(e)
		}
	}
	wantEm, wantMem := d.Emitted(), d.Mem()
	wantDups := d.Dups()
	wantWM, wantOK := d.Watermark()
	const N = 16
	type snap struct {
		em     []api.Event
		mem    map[string]int64
		dups   int64
		wm     int64
		wmOK   bool
		selfOK bool
	}
	snaps := make([]snap, N)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for k := 0; k < 50; k++ {
				snaps[g].em = d.Emitted()
				snaps[g].mem = d.Mem()
				snaps[g].dups = d.Dups()
				snaps[g].wm, snaps[g].wmOK = d.Watermark()
				snaps[g].selfOK = d.SelfCheck() == nil
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g, s := range snaps {
		if !reflect.DeepEqual(s.em, wantEm) || !reflect.DeepEqual(s.mem, wantMem) ||
			s.dups != wantDups || s.wm != wantWM || s.wmOK != wantOK || !s.selfOK {
			t.Errorf("goroutine %d 结果不一致: %+v", g, s)
		}
	}
}
