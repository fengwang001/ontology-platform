package etime

import "testing"

// TestScanCountConstant 证明 maxEt 是增量维护的单个整数：
// 先放 m 个递增按时事件，再放一个更大的按时事件，
// 最近一次 Ingest 逐条检查的事件数恒为小常数（此处为 0），不随 m 增长。
func TestScanCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		w := New(5)
		for i := 0; i < m; i++ {
			if !w.Ingest(int64(i)) {
				t.Fatalf("m=%d: 第 %d 个事件应按时", m, i)
			}
		}
		if !w.Ingest(int64(m)) {
			t.Fatalf("m=%d: 更大的事件应按时", m)
		}
		if w.scanCnt > 2 {
			t.Fatalf("m=%d: scanCnt=%d，随规模线性增长，疑似扫描已接受集合", m, w.scanCnt)
		}
	}
}

func TestIngest(t *testing.T) {
	cases := []struct {
		name     string
		lateness int64
		ets      []int64
		want     []bool // 每个事件是否按时
		wantETW  int64
		wantMax  int64
	}{
		{"首事件恒按时", 5, []int64{10}, []bool{true}, 5, 10},
		{"乱序按时", 5, []int64{10, 8, 9}, []bool{true, true, true}, 5, 10},
		{"等于水位迟到", 5, []int64{10, 5}, []bool{true, false}, 5, 10},
		{"低于水位迟到", 5, []int64{10, 4}, []bool{true, false}, 5, 10},
		{"推进后旧事件迟到", 5, []int64{10, 20, 19, 15, 16}, []bool{true, true, true, false, true}, 15, 20},
		{"零迟到容忍", 0, []int64{7, 7, 6}, []bool{true, false, false}, 7, 7},
	}
	for _, c := range cases {
		w := New(c.lateness)
		for i, et := range c.ets {
			if got := w.Ingest(et); got != c.want[i] {
				t.Errorf("%s: Ingest(%d)=%v, 想要 %v", c.name, et, got, c.want[i])
			}
		}
		if v, ok := w.ETW(); !ok || v != c.wantETW {
			t.Errorf("%s: ETW=(%d,%v), 想要 (%d,true)", c.name, v, ok, c.wantETW)
		}
		if v, ok := w.MaxEt(); !ok || v != c.wantMax {
			t.Errorf("%s: MaxEt=(%d,%v), 想要 (%d,true)", c.name, v, ok, c.wantMax)
		}
	}
}

func TestEmptyAndLateNoTrace(t *testing.T) {
	w := New(5)
	if _, ok := w.ETW(); ok {
		t.Fatal("空水位 ETW 应为负无穷")
	}
	if _, ok := w.MaxEt(); ok {
		t.Fatal("空水位 MaxEt 应为负无穷")
	}
	w.Ingest(10)
	etwBefore, _ := w.ETW()
	maxBefore, _ := w.MaxEt()
	if w.Ingest(3) { // 迟到
		t.Fatal("et=3 应迟到")
	}
	etwAfter, _ := w.ETW()
	maxAfter, _ := w.MaxEt()
	if etwAfter != etwBefore || maxAfter != maxBefore {
		t.Fatal("迟到事件不得改变 maxEt/ETW")
	}
}
