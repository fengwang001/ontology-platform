package rollup_test

import (
	"errors"
	"testing"

	"ontology/hold"
	"ontology/rollup"
	"ontology/tier"
)

func must(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", ctx, err)
	}
}

func wantErr(t *testing.T, err, target error, ctx string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: want %v, got %v", ctx, target, err)
	}
}

func eqBucket(b tier.Bucket, want tier.Bucket) bool { return b == want }

// 规格完整例子：折叠、部分覆盖跳过、迟到并入、L1→L2 恰等、整桶查询。
func TestSpecExample(t *testing.T) {
	s, _ := rollup.New(120_000, 7_200_000, 86_400_000, 1000, nil)
	must(t, s.Write(10_000, 5), "w1")
	must(t, s.Write(50_000, 3), "w2")
	must(t, s.Write(70_000, 7), "w3")
	must(t, s.Advance(180_000), "advance")
	// 判定依据：分钟0 结束 60000，180000-60000=120000 恰等 A0 -> 折叠。
	b0, _ := s.L1Bucket(0)
	if !eqBucket(b0, tier.Bucket{Count: 2, Sum: 8, Min: 3, Max: 5}) {
		t.Fatalf("m0 = %+v", b0)
	}
	q, _ := s.Query(0, 180_000)
	t.Logf("Query(0,180000)=%+v", q)
	if q != (rollup.QueryResult{3, 15, 3, 7, 0}) {
		t.Fatalf("full %+v", q)
	}
	q, _ = s.Query(0, 30_000) // 分钟0 仅部分相交 -> 整桶跳过 count=2
	t.Logf("Query(0,30000)=%+v (部分桶跳过)", q)
	if q != (rollup.QueryResult{0, 0, 0, 0, 2}) {
		t.Fatalf("partial %+v", q)
	}
	must(t, s.Write(20_000, 1), "late merge L1")
	b0, _ = s.L1Bucket(0)
	if !eqBucket(b0, tier.Bucket{Count: 3, Sum: 9, Min: 1, Max: 5}) {
		t.Fatalf("m0 late = %+v", b0)
	}
	must(t, s.Advance(10_800_000), "advance hour age")
	h0, _ := s.L2Bucket(0)
	t.Logf("L2 hour0=%+v units=%d", h0, s.Units())
	if !eqBucket(h0, tier.Bucket{Count: 4, Sum: 16, Min: 1, Max: 7}) {
		t.Fatalf("h0 = %+v", h0)
	}
	must(t, s.Write(100, 2), "late into L2")
	h0, _ = s.L2Bucket(0)
	if !eqBucket(h0, tier.Bucket{Count: 5, Sum: 18, Min: 1, Max: 7}) {
		t.Fatalf("h0 late = %+v", h0)
	}
	q, _ = s.Query(0, 3_600_000)
	if q != (rollup.QueryResult{5, 18, 1, 7, 0}) {
		t.Fatalf("hour query %+v", q)
	}
}

// 恰等阈值触发折叠与删除；差 1 毫秒不触发。
func TestExactThresholdsAndDelete(t *testing.T) {
	s, _ := rollup.New(tier.MinuteMS, tier.HourMS, tier.HourMS, 100, nil)
	must(t, s.Write(1000, 10), "w")
	must(t, s.Advance(2*tier.MinuteMS), "fold exact A0")
	if _, ok := s.L1Bucket(0); !ok {
		t.Fatal("exact A0 did not fold")
	}
	must(t, s.Advance(2*tier.HourMS), "delete exact A2")
	if s.Units() != 0 {
		t.Fatalf("exact A2 did not delete: %d", s.Units())
	}
	wantErr(t, s.Write(1000, 1), tier.ErrExpired, "expired after delete")
	// 差 1 不删除。
	s2, _ := rollup.New(1, 1, tier.HourMS, 10, nil)
	must(t, s2.Write(0, 1), "w2")
	must(t, s2.Advance(2*tier.HourMS-1), "one ms before delete")
	if s2.Units() != 1 {
		t.Fatalf("should survive: units=%d", s2.Units())
	}
}

// 保全冻结相交分钟；非 owner 不能解除；解除后下次 Advance 折叠。
func TestFreezeThenRelease(t *testing.T) {
	reg := hold.NewRegistry()
	s, _ := rollup.New(tier.MinuteMS, 2*tier.HourMS, 100*tier.HourMS, 100, reg)
	must(t, s.Write(10_000, 1), "w")
	must(t, reg.Place("h", 40_000, 45_000, "u"), "hold half-open inside m0")
	must(t, s.Advance(2*tier.MinuteMS), "advance held")
	if len(s.L0Points()) != 1 {
		t.Fatalf("held minute must stay L0: %+v", s.L0Points())
	}
	wantErr(t, reg.Release("h", "x"), tier.ErrPermission, "non owner")
	must(t, reg.Release("h", "admin"), "admin release")
	must(t, s.Advance(2*tier.MinuteMS+1), "advance after release")
	b, ok := s.L1Bucket(0)
	if !ok || b.Count != 1 || len(s.L0Points()) != 0 {
		t.Fatalf("post-release fold b=%+v ok=%v l0=%+v", b, ok, s.L0Points())
	}
}

// 迟到写入三层落位；保全相交时即使够龄也落 L0。
func TestLateWriteTiersAndHoldPriority(t *testing.T) {
	s, _ := rollup.New(120_000, 7_200_000, 86_400_000, 1000, nil)
	must(t, s.Advance(10_000_000), "c=10m")
	u := s.Units()
	must(t, s.Write(10_000, 1), "to L1 new bucket")
	if s.Units() != u+1 {
		t.Fatal("new L1 bucket costs 1")
	}
	must(t, s.Write(20_000, 2), "merge L1 free")
	if s.Units() != u+1 {
		t.Fatalf("merge must not cost unit: %d", s.Units())
	}
	must(t, s.Advance(11_000_000), "c=11m")
	must(t, s.Write(3_599_000, 4), "direct L2: 11m-3.6m>=A1")
	if _, ok := s.L2Bucket(0); !ok {
		t.Fatal("expected L2 bucket")
	}
	s2, _ := rollup.New(60_000, 120_000, 3_600_000, 100, nil)
	must(t, s2.Advance(10_000_000), "c2")
	wantErr(t, s2.Write(1000, 1), tier.ErrExpired, "expired")
	reg := hold.NewRegistry()
	s3, _ := rollup.New(60_000, 120_000, 3_600_000, 100, reg)
	must(t, s3.Advance(10_000_000), "c3")
	must(t, reg.Place("k", 5000, 10_000, "o"), "hold m0")
	must(t, s3.Write(2000, 9), "held overrides expiry")
	if len(s3.L0Points()) != 1 {
		t.Fatalf("held write L0: %+v", s3.L0Points())
	}
}

// 容量满拒写新增；折叠腾位；并入已有桶即使满也成功。
func TestCapacity(t *testing.T) {
	s, _ := rollup.New(120_000, 7_200_000, 86_400_000, 2, nil)
	must(t, s.Write(0, 1), "w1")
	must(t, s.Write(30_000, 2), "w2")
	wantErr(t, s.Write(40_000, 3), tier.ErrCapacity, "over cap")
	must(t, s.Advance(200_000), "fold frees unit")
	if s.Units() != 1 {
		t.Fatalf("units=%d", s.Units())
	}
	must(t, s.Write(150_000, 5), "new point after free")
	must(t, s.Write(10_000, 7), "merge at cap")
	b, _ := s.L1Bucket(0)
	if b.Count != 3 {
		t.Fatalf("b=%+v", b)
	}
}

// 每阶段注入 panic：返回中止错误，时钟与三层状态逐位回滚。
func TestPanicRollback(t *testing.T) {
	for _, phase := range []string{"delete", "l0l1", "l1l2"} {
		s, _ := rollup.New(60_000, 2*tier.HourMS, 100*tier.HourMS, 100, nil)
		must(t, s.Write(10_000, 1), "w")
		before := s.L0Points()
		s.PanicHook = func(p string) {
			if p == phase {
				panic("boom")
			}
		}
		err := s.Advance(2 * tier.MinuteMS)
		t.Logf("phase=%s -> %v", phase, err)
		wantErr(t, err, tier.ErrAborted, "panic "+phase)
		if s.Now() != 0 || len(s.L0Points()) != len(before) {
			t.Fatalf("rollback failed phase=%s now=%d", phase, s.Now())
		}
		if _, ok := s.L1Bucket(0); ok {
			t.Fatalf("phase=%s L1 leaked", phase)
		}
	}
}

func TestClockRollbackAndNoop(t *testing.T) {
	s, _ := rollup.New(60_000, 120_000, 3_600_000, 10, nil)
	must(t, s.Advance(1000), "advance")
	wantErr(t, s.Advance(999), tier.ErrClockRollback, "back")
	must(t, s.Advance(1000), "equal is noop")
}
