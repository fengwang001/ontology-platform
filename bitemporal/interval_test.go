package bitemporal

import (
	"testing"
	"time"
)

func TestIntervalHalfOpenBoundaries(t *testing.T) {
	base := time.Unix(0, 0).UTC()
	iv := Interval{From: base, To: base.Add(time.Hour)}

	if !iv.Contains(base) {
		t.Fatal("左边界 From 必须包含（左闭）")
	}
	if iv.Contains(base.Add(time.Hour)) {
		t.Fatal("右边界 To 必须不包含（右开）")
	}
	if !iv.Contains(base.Add(30 * time.Minute)) {
		t.Fatal("区间内部时间点必须包含")
	}

	empty := Interval{From: base, To: base}
	if !empty.Empty() || empty.Contains(base) {
		t.Fatal("From == To 必须为空区间")
	}
	inverted := Interval{From: base.Add(time.Hour), To: base}
	if !inverted.Empty() {
		t.Fatal("From > To 必须视为空区间")
	}
}
