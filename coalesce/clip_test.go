package coalesce_test

import (
	"errors"
	"testing"

	"ontology/coalesce"
	"ontology/rangespec"
)

func normalizeHeader(t *testing.T, header string, size int64) []coalesce.Interval {
	t.Helper()
	specs, err := rangespec.Parse(header)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ivs, err := coalesce.Normalize(specs, size)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return ivs
}

func TestFromToClipEndNotError(t *testing.T) {
	// b 超过末尾：裁剪到末尾而非报错。
	ivs := normalizeHeader(t, "bytes=90-200", 100)
	if len(ivs) != 1 || ivs[0] != (coalesce.Interval{Start: 90, End: 99}) {
		t.Fatalf("got %+v", ivs)
	}
}

func TestFromEndClip(t *testing.T) {
	ivs := normalizeHeader(t, "bytes=95-", 100)
	if ivs[0] != (coalesce.Interval{Start: 95, End: 99}) {
		t.Fatalf("got %+v", ivs)
	}
}

func TestSuffixOversizeTakesAll(t *testing.T) {
	// n 大于总长：取全部而非报错。
	ivs := normalizeHeader(t, "bytes=-500", 100)
	if ivs[0] != (coalesce.Interval{Start: 0, End: 99}) {
		t.Fatalf("got %+v", ivs)
	}
	ivs = normalizeHeader(t, "bytes=-100", 100)
	if ivs[0] != (coalesce.Interval{Start: 0, End: 99}) {
		t.Fatalf("n==size: got %+v", ivs)
	}
	ivs = normalizeHeader(t, "bytes=-3", 10)
	if ivs[0] != (coalesce.Interval{Start: 7, End: 9}) {
		t.Fatalf("tail 3: got %+v", ivs)
	}
}

func TestStartPastEndUnsatisfiable(t *testing.T) {
	for _, h := range []string{"bytes=100-200", "bytes=100-", "bytes=999-"} {
		specs, _ := rangespec.Parse(h)
		_, err := coalesce.Normalize(specs, 100)
		var ue *coalesce.UnsatisfiableError
		if !errors.As(err, &ue) {
			t.Fatalf("%s: want Unsatisfiable, got %v", h, err)
		}
		if ue.TotalLength != 100 {
			t.Fatalf("%s: total=%d", h, ue.TotalLength)
		}
	}
}

func TestSuffixZeroIsUnsatisfiable(t *testing.T) {
	specs, err := rangespec.Parse("bytes=-0")
	if err != nil {
		t.Fatalf("-0 must be syntactically valid: %v", err)
	}
	_, err = coalesce.Normalize(specs, 100)
	var ue *coalesce.UnsatisfiableError
	if !errors.As(err, &ue) {
		t.Fatalf("want Unsatisfiable for -0, got %v", err)
	}

	// 资源长度为 0 时任意 suffix 也不可满足。
	specs, _ = rangespec.Parse("bytes=-5")
	_, err = coalesce.Normalize(specs, 0)
	if !errors.As(err, &ue) {
		t.Fatalf("empty resource suffix: got %v", err)
	}
}
