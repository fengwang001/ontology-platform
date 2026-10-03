package view

import (
	"errors"
	"fmt"
	"testing"

	"ontology/agg"
)

func newStore(t *testing.T) *agg.Store {
	t.Helper()
	s, err := agg.New(
		map[string]int{"region": 1, "dept": 2, "secret": 3},
		map[string]int{"junior": 1, "mid": 2, "senior": 3},
		[3]int{2, 3, 5}, 1000, 100000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func ev(ts int64, region, dept string) agg.Event {
	return agg.Event{Ts: ts, Dims: map[string]string{"region": region, "dept": dept, "secret": "s"}}
}

func TestRejectOrder(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name           string
		role, row, col string
		from, to       int64
		want           error
	}{
		{"范围非法", "ghost", "nope", "nope2", 5, 5, agg.ErrInvalidParam},
		{"负端点", "ghost", "region", "dept", -1, 5, agg.ErrInvalidParam},
		{"同维度", "ghost", "region", "region", 0, 5, agg.ErrInvalidParam},
		{"维度未声明", "junior", "region", "nope", 0, 5, agg.ErrInvalidParam},
		{"角色未知", "ghost", "region", "dept", 0, 5, ErrUnknownRole},
		{"无权限", "junior", "region", "dept", 0, 5, ErrNoPermission},
	}
	for _, c := range cases {
		if _, err := Tabulate(s, c.role, c.row, c.col, c.from, c.to); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	t.Logf("判定依据: 参数非法→角色未知→无权限 只报第一个")
}

func TestPermissionBoundary(t *testing.T) {
	s := newStore(t)
	if err := s.Append([]agg.Event{ev(1, "r", "d")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := Tabulate(s, "mid", "region", "dept", 0, 10); err != nil {
		t.Fatalf("级别恰等(2≤2)应允许: %v", err)
	}
	if _, err := Tabulate(s, "mid", "region", "secret", 0, 10); !errors.Is(err, ErrNoPermission) {
		t.Fatalf("高一级(3>2)应拒绝: %v", err)
	}
	if _, err := Tabulate(s, "senior", "dept", "secret", 0, 10); err != nil {
		t.Fatalf("senior 应可见全部: %v", err)
	}
}

func TestKScalesWithSensitivity(t *testing.T) {
	s := newStore(t)
	// region×dept 各 2 个事件同格 -> c=2; k 取 max(1,2)=2 -> kByLevel[1]=3, c=2 被抑制
	if err := s.Append([]agg.Event{ev(1, "r", "d"), ev(2, "r", "d")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	tb, err := Tabulate(s, "senior", "region", "dept", 0, 10)
	if err != nil {
		t.Fatalf("Tabulate: %v", err)
	}
	if !tb.Cells[0][0].Suppressed || tb.Primary != 1 {
		t.Fatalf("k=3 时 c=2 应被主抑制: %+v", tb.Cells[0][0])
	}
	// region×region 非法, 用两个级别 1 的维度需另建存储验证 k=2 不抑制 c=2
	s2, err := agg.New(map[string]int{"a": 1, "b": 1}, map[string]int{"r": 1},
		[3]int{2, 3, 5}, 1000, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := func(ts int64) agg.Event {
		return agg.Event{Ts: ts, Dims: map[string]string{"a": "x", "b": "y"}}
	}
	if err := s2.Append([]agg.Event{e(1), e(2)}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	tb2, err := Tabulate(s2, "r", "a", "b", 0, 10)
	if err != nil {
		t.Fatalf("Tabulate: %v", err)
	}
	if tb2.Cells[0][0].Suppressed || tb2.Cells[0][0].Count != 2 {
		t.Fatalf("k=2 时 c=2 不应抑制: %+v", tb2.Cells[0][0])
	}
	t.Logf("敏感级别 2 使 k=3 (c=2 抑制); 级别 1 使 k=2 (c=2 可见), 与角色无关")
}

func TestEmptyRangeAndSizeLimit(t *testing.T) {
	s := newStore(t)
	tb, err := Tabulate(s, "senior", "region", "dept", 0, 10)
	if err != nil {
		t.Fatalf("Tabulate: %v", err)
	}
	if tb.Total != 0 || len(tb.Rows) != 0 || len(tb.Cells) != 0 {
		t.Fatalf("空范围应为空表: %+v", tb)
	}
	s2, err := agg.New(map[string]int{"a": 1, "b": 1}, map[string]int{"r": 3},
		[3]int{1, 1, 1}, 3, 1000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var batch []agg.Event
	for i := 0; i < 4; i++ {
		batch = append(batch, agg.Event{Ts: int64(i),
			Dims: map[string]string{"a": fmt.Sprintf("r%d", i), "b": fmt.Sprintf("c%d", i)}})
	}
	if err := s2.Append(batch); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := Tabulate(s2, "r", "a", "b", 0, 10); !errors.Is(err, agg.ErrTooLarge) {
		t.Fatalf("4x4=16 > Cmax=3, got %v, want ErrTooLarge", err)
	}
}
