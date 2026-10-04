package querier

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type opKind int

const (
	opReport opKind = iota
	opLeave
	opQuery
	opDrain
	opForward
)

type op struct {
	kind opKind
	t    int64
	p    int
	grp  uint32
	src  uint32
}

func qsort(qs []Query) {
	sort.SliceStable(qs, func(i, j int) bool {
		a, b := qs[i], qs[j]
		if a.Time != b.Time {
			return a.Time < b.Time
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Port < b.Port
	})
}

func sameQueries(a, b []Query) bool {
	if len(a) != len(b) {
		return false
	}
	qsort(a)
	qsort(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNaiveDifferential 与逐秒推进的朴素模型对照 1500 组的随机操作序列。
func TestNaiveDifferential(t *testing.T) {
	const (
		P       = 6
		nGroup  = 1500
		nOps    = 4000
		horizon = 2600
	)
	rng := rand.New(rand.NewSource(20261005))
	qi := int64(20)
	qri := int64(5)
	rb := int64(2)
	lmqi := int64(3)
	fast := make([]bool, P)
	fast[P-1] = true // 至少一个快速离开端口
	flood := rng.Intn(2) == 0
	gmax := 400
	lp := 30

	groups := make([]uint32, nGroup)
	for i := range groups {
		groups[i] = uint32(0xE1000000) + uint32(i)
	}

	c, err := New(P, 7, qi, qri, rb, lmqi, fast, flood, gmax, lp)
	if err != nil {
		t.Fatal(err)
	}
	n := newNaive(P, 7, qi, qri, rb, lmqi, fast, flood, gmax, lp)

	var now int64
	ops := make([]op, 0, nOps)
	for i := 0; i < nOps; i++ {
		now += int64(rng.Intn(3)) // 0,1,2 秒，允许同刻多事件
		if now > horizon {
			break
		}
		var grp uint32
		if rng.Intn(20) == 0 {
			grp = uint32(0xE0000000) + uint32(rng.Intn(256)) // 偶发本地链路组
		} else {
			grp = groups[rng.Intn(nGroup)]
		}
		o := op{t: now, p: 1 + rng.Intn(P), grp: grp}
		switch rng.Intn(10) {
		case 0, 1, 2, 3:
			o.kind = opReport
		case 4, 5:
			o.kind = opLeave
		case 6:
			o.kind = opQuery
			o.src = uint32(1 + rng.Intn(12)) // 含大于、小于、等于 ownIP
		case 7, 8:
			o.kind = opForward
		default:
			o.kind = opDrain
		}
		ops = append(ops, o)
	}

	var logBuf []string
	flushLog := func() {
		for _, line := range logBuf {
			t.Log(line)
		}
	}

	for i, o := range ops {
		why := ""
		switch o.kind {
		case opReport:
			n.runTo(o.t)
			got, e1 := c.Report(o.p, o.grp, o.t)
			want, e2 := n.report(o.p, o.grp, o.t)
			why = fmt.Sprintf("Report(p=%d,g=%08X,t=%d)", o.p, o.grp, o.t)
			if !sameErr(e1, e2) || !reflect.DeepEqual(got, want) {
				logBuf = append(logBuf, fmt.Sprintf("MISMATCH #%d %s got=%v,%v want=%v,%v", i, why, got, e1, want, e2))
				flushLog()
				t.Fatalf("differential mismatch at #%d %s", i, why)
			}
			logBuf = append(logBuf, fmt.Sprintf("#%d %s -> %v (err=%v)", i, why, got, e1))
		case opLeave:
			n.runTo(o.t)
			e1 := c.Leave(o.p, o.grp, o.t)
			e2 := n.leave(o.p, o.grp, o.t)
			why = fmt.Sprintf("Leave(p=%d,g=%08X,t=%d,fast=%v,q=%v)", o.p, o.grp, o.t, fast[o.p-1], n.isQuerier)
			if !sameErr(e1, e2) {
				flushLog()
				t.Fatalf("leave mismatch #%d %s: %v vs %v", i, why, e1, e2)
			}
			logBuf = append(logBuf, fmt.Sprintf("#%d %s -> err=%v", i, why, e1))
		case opQuery:
			e1 := c.Query(o.p, o.src, o.t)
			n.runTo(o.t)
			e2 := n.query(o.p, o.src, o.t)
			why = fmt.Sprintf("Query(p=%d,src=%d,t=%d)", o.p, o.src, o.t)
			if !sameErr(e1, e2) {
				flushLog()
				t.Fatalf("query mismatch #%d: %v vs %v", i, e1, e2)
			}
			logBuf = append(logBuf, fmt.Sprintf("#%d %s -> err=%v querier=%v/oq=%d", i, why, e1, !n.isQuerier, n.oq))
		case opDrain:
			got, e1 := c.Drain(o.t)
			n.runTo(o.t)
			want := n.drain(o.t)
			why = fmt.Sprintf("Drain(t=%d)", o.t)
			if !sameErr(e1, nil) || !sameQueries(got, want) {
				logBuf = append(logBuf, fmt.Sprintf("MISMATCH #%d %s got=%v want=%v", i, why, spec(got), spec(want)))
				flushLog()
				t.Fatalf("drain mismatch #%d", i)
			}
			logBuf = append(logBuf, fmt.Sprintf("#%d %s -> %s", i, why, spec(got)))
		case opForward:
			n.runTo(o.t)
			got, e1 := c.Forward(o.grp, o.p, o.t)
			want := n.forward(o.grp, o.p)
			why = fmt.Sprintf("Forward(g=%08X,in=%d,t=%d)", o.grp, o.p, o.t)
			if !sameErr(e1, nil) || !reflect.DeepEqual(got, want) {
				flushLog()
				t.Fatalf("forward mismatch #%d %s: %v vs %v", i, why, got, want)
			}
			logBuf = append(logBuf, fmt.Sprintf("#%d %s -> %v", i, why, got))
		}
		if len(logBuf) > 60 {
			logBuf = logBuf[len(logBuf)-40:]
		}
	}
	for _, line := range logBuf {
		t.Log(line)
	}
	t.Logf("differential done: %d ops over %d groups, flood=%v (仅打印末尾 %d 条输入/输出/判定依据)", len(ops), nGroup, flood, len(logBuf))
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

func spec(qs []Query) string {
	qsort(qs)
	return fmt.Sprintf("%v", qs)
}

// TestDeterministicReplay 相同操作序列重放两次，结果完全一致。
func TestDeterministicReplay(t *testing.T) {
	run := func() []string {
		c, _ := New(4, 5, 20, 5, 2, 3, []bool{false, false, false, true}, true, 50, 10)
		rng := rand.New(rand.NewSource(42))
		var log []string
		var now int64
		for i := 0; i < 500; i++ {
			now += int64(rng.Intn(3))
			grp := uint32(0xE1000000) + uint32(rng.Intn(30))
			p := 1 + rng.Intn(4)
			switch rng.Intn(5) {
			case 0:
				to, err := c.Report(p, grp, now)
				log = append(log, fmt.Sprintf("R%d:%x:%v:%v", now, grp, to, err))
			case 1:
				log = append(log, fmt.Sprintf("L%d:%x:%v", now, grp, c.Leave(p, grp, now)))
			case 2:
				log = append(log, fmt.Sprintf("Q%d:%v", now, c.Query(p, uint32(1+rng.Intn(10)), now)))
			case 3:
				f, _ := c.Forward(grp, p, now)
				log = append(log, fmt.Sprintf("F%d:%x:%v", now, grp, f))
			default:
				qs, _ := c.Drain(now)
				log = append(log, fmt.Sprintf("D%d:%v", now, qs))
			}
		}
		return log
	}
	a := run()
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("replay differs")
	}
}

// TestConcurrentSafe 并发调用结果不崩溃且时钟单调（-race 覆盖数据竞争）。
func TestConcurrentSafe(t *testing.T) {
	c, _ := New(8, 5, 100, 10, 2, 5, make([]bool, 8), true, 200, 50)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 200; i++ {
				t0 := int64(id*200 + i)
				grp := uint32(0xE1000000) + uint32(rng.Intn(50))
				_, _ = c.Report(1+rng.Intn(8), grp, t0)
				_ = c.Leave(1+rng.Intn(8), grp, t0+1)
				_, _ = c.Forward(grp, 1+rng.Intn(8), t0+2)
				_, _ = c.Drain(t0 + 3)
			}
		}(w)
	}
	wg.Wait()
}
