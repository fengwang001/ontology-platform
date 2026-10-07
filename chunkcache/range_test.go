package chunkcache

import (
	"errors"
	"testing"
)

func TestRangeConstructorsDistinguishErrors(t *testing.T) {
	if _, err := RangeStartEnd(-1, 10); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative start: %v", err)
	}
	if _, err := RangeStartEnd(5, 4); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("end<start: %v", err)
	} else if e := err.(*Error); e.Reason != ReasonEndBeforeStart {
		t.Fatalf("reason = %q", e.Reason)
	}
	if _, err := RangeFrom(-3); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative from: %v", err)
	}
	if _, err := RangeSuffix(-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative suffix: %v", err)
	}
	r, err := RangeSuffix(0)
	if err != nil {
		t.Fatalf("suffix 0 constructs fine: %v", err)
	}
	if _, err := r.resolve("test", 100); !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("suffix 0 resolve: %v", err)
	} else if e := err.(*Error); e.Reason != ReasonZeroSuffix {
		t.Fatalf("reason = %q", e.Reason)
	}
}

func TestResolveClampingAndBoundaries(t *testing.T) {
	const s = int64(4)
	// 终点超出总长度截到末尾。
	r, _ := RangeStartEnd(9, 50)
	got, err := r.resolve("test", 12)
	if err != nil {
		t.Fatal(err)
	}
	if got.start != 9 || got.end != 12 {
		t.Fatalf("clamp = %+v", got)
	}
	// 起点恰好等于总长度：不可满足且原因可区分。
	r2, _ := RangeStartEnd(12, 12)
	_, err = r2.resolve("test", 12)
	if !errors.Is(err, ErrRangeNotSatisfiable) || err.(*Error).Reason != ReasonStartAtOrAfterSize {
		t.Fatalf("at length: %v", err)
	}
	// 起点超过总长度。
	r3, _ := RangeFrom(13)
	if _, err := r3.resolve("test", 12); !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("beyond length: %v", err)
	}
	// 总长度为零：后缀0 与 起点0 分别区分。
	r4, _ := RangeSuffix(0)
	_, err = r4.resolve("test", 0)
	if !errors.Is(err, ErrRangeNotSatisfiable) || err.(*Error).Reason != ReasonZeroSuffix {
		t.Fatalf("zero length zero suffix: %v", err)
	}
	r5, _ := RangeFrom(0)
	_, err = r5.resolve("test", 0)
	if !errors.Is(err, ErrRangeNotSatisfiable) || err.(*Error).Reason != ReasonStartAtOrAfterSize {
		t.Fatalf("zero length from 0: %v", err)
	}
	// 非零后缀超过总长则从 0 开始。
	r6, _ := RangeSuffix(1000)
	got6, err := r6.resolve("test", 12)
	if err != nil || got6.start != 0 || got6.end != 12 {
		t.Fatalf("big suffix = %+v err=%v", got6, err)
	}
	// 恰在切片边界与差一字节。
	for i, tc := range []struct {
		start, end int64
		lo, hi     int
	}{
		{0, 3, 0, 0},  // 整一个切片
		{0, 4, 0, 1},  // 终点含第 4 字节 => 跨两片（差一字节过界）
		{3, 4, 0, 1},  // 跨边界两个字节
		{4, 7, 1, 1},  // 第二片整体
		{8, 11, 2, 2}, // 末片
		{0, 11, 0, 2}, // 全长 12 字节 => 3 片
		{7, 8, 1, 2},  // 边界相邻字节
	} {
		rr, _ := RangeStartEnd(tc.start, tc.end)
		g, err := rr.resolve("test", 12)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		lo, hi := chunkSpan(s, g.start, g.end)
		if lo != tc.lo || hi != tc.hi {
			t.Fatalf("case %d span=(%d,%d) want (%d,%d)", i, lo, hi, tc.lo, tc.hi)
		}
	}
	// 总长度恰为切片整数倍时最后字节落在新切片边界内。
	rr, _ := RangeStartEnd(15, 15)
	g, _ := rr.resolve("test", 16)
	if lo, hi := chunkSpan(4, g.start, g.end); lo != 3 || hi != 3 {
		t.Fatalf("exact multiple last byte span=(%d,%d)", lo, hi)
	}
}
