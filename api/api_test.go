package api

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func ev(k string, ts int64) Event { return Event{Key: k, TS: ts} }

var eight = []Event{ev("K", 2), ev("K", 7), ev("K", 13), ev("K", 9), ev("K", 18), ev("K", 4), ev("K", 10), ev("K", 23)}

func feedAll(t *testing.T, a *API, evs []Event) []Change {
	t.Helper()
	var log []Change
	for _, e := range evs {
		ch, err := a.Feed([]Event{e})
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, ch...)
	}
	return append(log, a.Flush()...)
}

// 不变量 1：Flush 后 View 等于「只取被接受事件」的批量重算；多序列 + 循环生成随机到达顺序。
func TestFlushMatchesBatch(t *testing.T) {
	seqs := [][]Event{eight, {ev("a", -25), ev("a", -5), ev("b", 3), ev("a", 30), ev("a", -15)}}
	seed := int64(7)
	for r := 0; r < 4; r++ { // LCG 洗牌生成随机到达顺序
		base := []Event{ev("a", 1), ev("a", 8), ev("b", 4), ev("a", 12), ev("b", 19), ev("a", 3), ev("c", 25)}
		for i := len(base) - 1; i > 0; i-- {
			seed = seed*6364136223846793005 + 1442695040888963407
			j := int(uint64(seed>>33) % uint64(i+1))
			base[i], base[j] = base[j], base[i]
		}
		seqs = append(seqs, base)
	}
	for i, seq := range seqs {
		t.Run(fmt.Sprintf("seq%d", i), func(t *testing.T) {
			a, _ := New(10, 3, 5, 0)
			feedAll(t, a, seq)
			if got, want := a.View(), reference(seq, 10, 3, 5); !reflect.DeepEqual(got, want) {
				t.Fatalf("view %v != batch %v", got, want)
			}
		})
	}
}

// 第三节(丙)：另一种到达顺序最终视图必须不同，差异源于水位线提前推进导致丢弃。
func TestReorderDifferentResult(t *testing.T) {
	reord := []Event{ev("K", 23), ev("K", 2), ev("K", 7), ev("K", 13), ev("K", 9), ev("K", 18), ev("K", 4), ev("K", 10)}
	a1, _ := New(10, 3, 5, 0)
	a2, _ := New(10, 3, 5, 0)
	feedAll(t, a1, eight)
	feedAll(t, a2, reord)
	v := a2.View()
	if reflect.DeepEqual(a1.View(), v) || v[ViewKey{Key: "K", Start: 0, End: 10}] != 0 ||
		v[ViewKey{Key: "K", Start: 10, End: 20}] != 3 {
		t.Fatalf("reordered view = %v", v)
	}
}

// 不变量 2：变更日志每个前缀自洽，且终态等于 View。
func TestChangelogPrefixConsistency(t *testing.T) {
	a, _ := New(10, 3, 5, 0)
	if err := replay(feedAll(t, a, eight)); err != nil {
		t.Fatal(err)
	}
}

// 不变量 4a：参数非法表驱动；三个哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	for _, p := range [][3]int64{{0, 3, 5}, {-1, 3, 5}, {10, -1, 5}, {10, 3, -1}} {
		if _, err := New(p[0], p[1], p[2], 0); err != ErrParam {
			t.Fatalf("New%v err = %v, want ErrParam", p, err)
		}
	}
	if ErrParam == ErrMaxOpen || ErrParam == ErrEmptyKey || ErrMaxOpen == ErrEmptyKey {
		t.Fatal("sentinel errors not distinct")
	}
}

// 不变量 4b：任一条被拒整批不生效（视图、丢弃数不变），之后仍可用。
func TestFailureAtomicity(t *testing.T) {
	a, _ := New(10, 3, 5, 1)
	if _, err := a.Feed([]Event{ev("a", 1)}); err != nil {
		t.Fatal(err)
	}
	snap, dropped := a.View(), a.Dropped()
	for _, bad := range [][]Event{{ev("b", 11)}, {ev("a", 5), {Key: "", TS: 2}}} {
		if _, err := a.Feed(bad); err != ErrMaxOpen && err != ErrEmptyKey {
			t.Fatalf("Feed(%v) err = %v", bad, err)
		}
		if !reflect.DeepEqual(a.View(), snap) || a.Dropped() != dropped {
			t.Fatalf("rejected batch %v changed state", bad)
		}
	}
	if ch := a.Flush(); len(ch) != 1 || ch[0].Count != 1 { // 拒绝后仍可用：a@1 还在
		t.Fatalf("post-rejection flush = %v", ch)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}

// 并发：N 个 goroutine 并发只读同一喂满实例，视图逐字段相同；不用 sleep。
func TestConcurrentReaders(t *testing.T) {
	a, _ := New(10, 3, 5, 0)
	var evs []Event
	for i := 0; i < 200; i++ {
		evs = append(evs, ev(fmt.Sprintf("k%d", i%7), int64(i)))
	}
	feedAll(t, a, evs)
	want := a.View()
	start, errs := make(chan struct{}), make(chan error, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 100; r++ {
				if !reflect.DeepEqual(a.View(), want) || a.Dropped() < 0 || a.SelfCheck() != nil {
					errs <- fmt.Errorf("read mismatch")
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}
