package agg

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func newStore(t *testing.T, emax int) *Store {
	t.Helper()
	s, err := New(map[string]int{"region": 1, "dept": 2},
		map[string]int{"analyst": 2}, [3]int{2, 3, 4}, 64, emax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func ev(ts int64, region, dept string) Event {
	return Event{Ts: ts, Dims: map[string]string{"region": region, "dept": dept}}
}

func TestAppendRejectsInvalidBatchAtomically(t *testing.T) {
	cases := map[string][]Event{
		"空批":     {},
		"超批":     make([]Event, 1001),
		"负时间":    {ev(-1, "a", "b")},
		"超时间":    {ev(MaxTs+1, "a", "b")},
		"缺维度":    {{Ts: 1, Dims: map[string]string{"region": "a"}}},
		"多维度":    {{Ts: 1, Dims: map[string]string{"region": "a", "dept": "b", "x": "y"}}},
		"空值":     {ev(1, "", "b")},
		"超长值":    {ev(1, string(make([]byte, 65)), "b")},
		"批内第二非法": {ev(1, "a", "b"), ev(2, "a", "")},
	}
	for name, batch := range cases {
		s := newStore(t, 100)
		if err := s.Append(batch); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("%s: got %v, want ErrInvalidParam", name, err)
		}
		if len(s.events) != 0 {
			t.Errorf("%s: 被拒批次改变了存储", name)
		}
	}
}

func TestAppendCapacity(t *testing.T) {
	s := newStore(t, 3)
	if err := s.Append([]Event{ev(1, "a", "b"), ev(2, "a", "c")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append([]Event{ev(3, "a", "b"), ev(4, "a", "b")}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("got %v, want ErrCapacity", err)
	}
	if len(s.events) != 2 {
		t.Fatalf("容量错误后存储被改变: %d", len(s.events))
	}
}

func TestQueryHalfOpenRange(t *testing.T) {
	s := newStore(t, 100)
	batch := []Event{ev(9, "r", "c"), ev(10, "r", "c"), ev(19, "r", "c"), ev(20, "r", "c")}
	if err := s.Append(batch); err != nil {
		t.Fatalf("Append: %v", err)
	}
	res, err := s.Query("region", "dept", 10, 20)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got := res.Counts[0][0]; got != 2 {
		t.Fatalf("[from,to) 半开区间计数 = %d, want 2", got)
	}
	t.Logf("输入 ts={9,10,19,20}, 范围 [10,20), 命中 2 条, 依据: 左闭右开")
}

func TestQueryExaminedEqualsInRange(t *testing.T) {
	build := func(total int) *Store {
		s := newStore(t, total)
		var all []Event
		for i := 0; i < 4; i++ { // 范围内 [0,7) 固定 4 条
			all = append(all, ev(int64(i*2), fmt.Sprintf("r%d", i%2), fmt.Sprintf("c%d", i%3)))
		}
		for i := 4; i < total; i++ { // 范围外填充
			all = append(all, ev(int64(1000+i*10), fmt.Sprintf("r%d", i%3), fmt.Sprintf("c%d", i%5)))
		}
		for len(all) > 0 {
			n := min(1000, len(all))
			if err := s.Append(all[:n]); err != nil {
				t.Fatalf("Append: %v", err)
			}
			all = all[n:]
		}
		return s
	}
	small, large := build(100), build(10000)
	r1, err1 := small.Query("region", "dept", 0, 7)
	r2, err2 := large.Query("region", "dept", 0, 7)
	if err1 != nil || err2 != nil {
		t.Fatalf("Query: %v %v", err1, err2)
	}
	n1, n2 := small.examined.Load(), large.examined.Load()
	if n1 != n2 {
		t.Fatalf("考察数不等: 100 事件档 %d, 10000 事件档 %d", n1, n2)
	}
	if n1 != 4 {
		t.Fatalf("考察数 = %d, want 4 (恰等于范围内事件数)", n1)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("两档结果不一致:\n%+v\n%+v", r1, r2)
	}
	t.Logf("范围内事件 %d 条, 两档考察数均为 %d, 未扫描范围外事件", n1, n2)
}

func TestQuerySizeLimitAndOutOfOrder(t *testing.T) {
	s, err := New(map[string]int{"a": 1, "b": 1}, nil, [3]int{1, 1, 1}, 4, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	batch := []Event{
		{Ts: 5, Dims: map[string]string{"a": "r2", "b": "c2"}},
		{Ts: 1, Dims: map[string]string{"a": "r1", "b": "c1"}},
		{Ts: 3, Dims: map[string]string{"a": "r1", "b": "c2"}},
		{Ts: 4, Dims: map[string]string{"a": "r2", "b": "c1"}},
		{Ts: 2, Dims: map[string]string{"a": "r3", "b": "c1"}},
	}
	if err := s.Append(batch); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := s.Query("a", "b", 0, 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("3x2=6 > Cmax=4, got %v, want ErrTooLarge", err)
	}
	res, err := s.Query("a", "b", 3, 6) // 只剩 r1,r2 × c1,c2 = 4 格
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want := &Result{Rows: []string{"r1", "r2"}, Cols: []string{"c1", "c2"},
		Counts: [][]int{{0, 1}, {1, 1}}}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("got %+v, want %+v", res, want)
	}
	t.Logf("乱序追加后行集 %v 列集 %v 按字节序升序, 计数 %v", res.Rows, res.Cols, res.Counts)
}
