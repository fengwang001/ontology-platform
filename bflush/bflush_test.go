package bflush_test

import (
	"errors"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/bflush"
	"ontology/buf"
)

type recSink struct {
	got           []buf.Entry
	failOn, calls int
}

func (s *recSink) Apply(b []buf.Entry) error {
	if s.calls++; s.failOn > 0 && s.calls == s.failOn {
		return errors.New("boom")
	}
	s.got = append(s.got, b...)
	return nil
}

func key(i int) string { return "k" + strconv.Itoa(i) }

func chk(t *testing.T, ok bool, m string) {
	t.Helper()
	if !ok {
		t.Fatal(m)
	}
}

func w(e *bflush.Engine, ks ...string) {
	for _, x := range ks {
		e.Write(x, 0)
	}
}

// propModel 参考 FIFO 跑随机序列：按交付增量弹参考队列，逐拍校验残留 age<L（不变量3），最终比对交付拼接==FIFO（不变量1）。
func propModel(t *testing.T, B, H, L int) {
	s := &recSink{}
	e, _ := bflush.New(B, H, L, s)
	var qk []string
	var qat []int
	var acc []buf.Entry
	now, del := 0, 0
	pop := func() {
		d := int(e.Delivered()) - del
		del = int(e.Delivered())
		qk, qat = qk[d:], qat[d:]
	}
	x := int64(B + H + L)
	for i := 0; i < 200; i++ {
		x = x*6364136223846793005 + 1
		switch x % 5 {
		case 0, 1, 2:
			kk := key(i)
			if e.Write(kk, i) == nil {
				qk, qat = append(qk, kk), append(qat, now)
				acc = append(acc, buf.Entry{Key: kk, Val: i})
			}
		case 3:
			n, err := e.Tick()
			chk(t, err == nil, "tick")
			now = n
			pop()
			for j := range qk {
				chk(t, now-qat[j] < L, "残留 age>=L")
			}
		default:
			_, err := e.Flush()
			chk(t, err == nil, "flush")
			pop()
		}
	}
	chk(t, e.FlushAll() == nil, "flushall")
	pop()
	chk(t, reflect.DeepEqual(s.got, acc) && len(qk) == 0, "FIFO 不一致")
}
func TestNaiveReplay(t *testing.T) { propModel(t, 3, 12, 2) }
func TestLatencyBound(t *testing.T) {
	for _, c := range [][3]int{{3, 8, 2}, {4, 10, 3}, {2, 6, 1}} {
		propModel(t, c[0], c[1], c[2])
	}
}

// TestFailureNoTrace 不变量4：四类错误互异、拒收/非法参数不留痕、Sink 失败整体回滚且可恢复。
func TestFailureNoTrace(t *testing.T) {
	for _, c := range [][3]int{{0, 1, 1}, {2, 1, 1}, {1, 1, 0}} {
		_, err := bflush.New(c[0], c[1], c[2], &recSink{})
		chk(t, errors.Is(err, bflush.ErrInvalidParam), "非法参数")
	}
	_, err := bflush.New(1, 1, 1, nil)
	chk(t, errors.Is(err, bflush.ErrInvalidParam), "nil sink")
	e, _ := bflush.New(1, 1, 1, &recSink{})
	e.Write("k", 1)
	chk(t, errors.Is(e.Write("j", 2), bflush.ErrHighWater) && e.Buffered() == 1 && e.Delivered() == 0, "高水位留痕")
	chk(t, errors.Is(e.Write("", 1), bflush.ErrEmptyKey) && e.Buffered() == 1, "空key留痕")
	f := &recSink{failOn: 2}
	r, _ := bflush.New(3, 4, 2, f)
	w(r, "a", "b", "c")
	r.Tick()
	w(r, "d", "e", "f")
	n, errT := r.Tick()
	chk(t, errors.Is(errT, bflush.ErrSink) && n == 1 && r.Buffered() == 3 && r.Delivered() == 3, "失败未回滚")
	f.failOn = 0
	chk(t, r.FlushAll() == nil && r.Buffered() == 0 && r.Delivered() == 6, "回滚后不可恢复")
	want := []buf.Entry{{Key: "a"}, {Key: "b"}, {Key: "c"}, {Key: "d"}, {Key: "e"}, {Key: "f"}}
	chk(t, reflect.DeepEqual(f.got, want), "重试丢/重")
}

// TestConcurrentWrites 并发写不丢不重；写期间并发读 Buffered 在 [0,H]、Delivered 单调不减。
func TestConcurrentWrites(t *testing.T) {
	const N, H = 300, 400
	s := &recSink{}
	e, _ := bflush.New(7, H, 1<<30, s)
	var wg sync.WaitGroup
	var bad int32
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d0 := e.Delivered()
			if v := e.Buffered(); v < 0 || v > H {
				atomic.StoreInt32(&bad, 1)
			}
			if e.Write(key(i), i) != nil {
				atomic.StoreInt32(&bad, 1) // H>N，不应拒收
			}
			v, d1 := e.Buffered(), e.Delivered()
			if v < 0 || v > H || d1 < d0 {
				atomic.StoreInt32(&bad, 1)
			}
		}(i)
	}
	wg.Wait()
	chk(t, atomic.LoadInt32(&bad) == 0, "并发读越界或写被拒")
	chk(t, e.FlushAll() == nil && e.Buffered() == 0 && e.Delivered() == N, "并发排空")
	seen := map[string]bool{}
	for _, z := range s.got {
		chk(t, !seen[z.Key], "重复 key")
		seen[z.Key] = true
	}
}
