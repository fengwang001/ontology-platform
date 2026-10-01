package swingdoor

import (
	"bytes"
	"errors"
	"testing"
)

// 斜率恰等于 lo / 恰等于 hi 时继续。
func TestSlopeOnBoundsContinues(t *testing.T) {
	// 起点 (0,0), E=1：(3,2) 给出 lo=1/3、hi=1；
	// (6,2) 斜率 2/6 恰等于 lo=1/3（继续），并把 hi 收紧为 1/2；
	// (8,4) 斜率 4/8 恰等于 hi=1/2（继续）。等号不算超界。
	got := run(t, 1,
		Sample{0, 0}, Sample{3, 2}, Sample{6, 2}, Sample{8, 4},
	)
	want := []Sample{{0, 0}, {8, 4}}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// E=0：共线点被丢弃，折点被保留。
func TestZeroToleranceKeepsVertices(t *testing.T) {
	got := run(t, 0,
		Sample{0, 0}, Sample{1, 1}, Sample{2, 2}, Sample{3, 1}, Sample{4, 0},
	)
	want := []Sample{{0, 0}, {2, 2}, {4, 0}}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 重启起点是「上一个待定点」而不是触发超界的新采样。
func TestRestartOriginIsPreviousPending(t *testing.T) {
	// E=1：(2,3) 相对 (0,0) 的斜率 3/2 超出 hi=1，被存档的必须是
	// 「上一个待定点」(1,0)，而不是触发超界的 (2,3) 本身。
	got := run(t, 1,
		Sample{0, 0}, Sample{1, 0}, Sample{2, 3}, Sample{3, 0},
	)
	if len(got) < 2 || got[1] != (Sample{1, 0}) {
		t.Fatalf("restart anchor must be previous pending (1,0), got %v", got)
	}
}

// 规定反例：E=1, (0,0),(1,0),(2,3),(3,0)。
// 关键判定：(2,3) 到达时斜率 3/2 超出 hi=1，存档的是 (1,0)，
// 而不是错误地把 (2,3) 存档成一个相对首段偏差 1.5 的点。
// 随后 (3,0) 相对新起点 (2,3) 再次超界，故 (2,3) 也成为存档点；
// 所有存档点连线对被丢弃点的偏差均不超过 E（由朴素校验验证）。
func TestSpecCounterexample(t *testing.T) {
	got := run(t, 1,
		Sample{0, 0}, Sample{1, 0}, Sample{2, 3}, Sample{3, 0},
	)
	want := []Sample{{0, 0}, {1, 0}, {2, 3}, {3, 0}}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSingleSample(t *testing.T) {
	got := run(t, 5, Sample{7, 9})
	want := []Sample{{7, 9}}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTwoSamples(t *testing.T) {
	got := run(t, 5, Sample{0, 0}, Sample{10, 3})
	want := []Sample{{0, 0}, {10, 3}}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 关闭时末点已是存档点：单采样流中首点即存档点，关闭不重复追加。
func TestCloseLastAlreadyArchived(t *testing.T) {
	var log bytes.Buffer
	c, err := NewWithLogger(0, &log)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, c, Sample{0, 0})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	got := c.Archives()
	want := []Sample{{0, 0}}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 触发重启后，关闭仍追加末点。
func TestCloseAppendsAfterRestart(t *testing.T) {
	got := run(t, 0,
		Sample{0, 0}, Sample{1, 1}, Sample{2, 1}, Sample{3, 1},
	)
	// (2,1) 超界 -> 存档 (1,1)；(3,1) 相对它斜率 0，在 [0,0] 内；关闭追加 (3,1)。
	want := []Sample{{0, 0}, {1, 1}, {3, 1}}
	if !samplesEq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInvalidTolerance(t *testing.T) {
	for _, e := range []int64{-1, 4_000_000_001} {
		if _, err := New(e); !errors.Is(err, ErrInvalidTolerance) {
			t.Fatalf("New(%d): err=%v want ErrInvalidTolerance", e, err)
		}
	}
}

func TestRejectionCausesAndOrder(t *testing.T) {
	c, _ := New(1)

	// 数值检查先于时间次序（值和时间都非法时返回数值原因）。
	if err := c.Write(2_000_000_000, 2_000_000_000); !errors.Is(err, ErrValueOutOfRange) {
		t.Fatalf("value out of range: %v", err)
	}
	if err := c.Write(2_000_000_000, 0); !errors.Is(err, ErrTimeOutOfRange) {
		t.Fatalf("time out of range: %v", err)
	}

	if err := c.Write(1, 0); err != nil {
		t.Fatalf("valid write: %v", err)
	}
	// 时间戳相等与小于上一采样是两种不同原因。
	if err := c.Write(1, 5); !errors.Is(err, ErrDuplicateTime) {
		t.Fatalf("duplicate time: %v", err)
	}
	if err := c.Write(0, 5); !errors.Is(err, ErrTimeBeforePrev) {
		t.Fatalf("time before previous: %v", err)
	}

	// 被拒绝的操作不得改变状态：合法点仍可继续写入。
	if err := c.Write(2, 1); err != nil {
		t.Fatalf("write after rejections: %v", err)
	}

	// 已关闭优先于数值超限与时间次序。
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(9_999_999_999, 9_999_999_999); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := c.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("double close: %v", err)
	}

	got := c.Archives()
	if len(got) != 2 || got[0] != (Sample{1, 0}) || got[1] != (Sample{2, 1}) {
		t.Fatalf("rejected ops changed state: %v", got)
	}
}

func TestEmptyCloseRejected(t *testing.T) {
	c, _ := New(1)
	if err := c.Close(); !errors.Is(err, ErrEmptyClose) {
		t.Fatalf("empty close: %v", err)
	}
	// 拒绝空流关闭不改变关闭状态，仍可正常写入并再次关闭。
	if err := c.Write(0, 0); err != nil {
		t.Fatalf("write after rejected close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicReplay(t *testing.T) {
	pts := []Sample{{0, 0}, {1, 4}, {2, 0}, {3, 5}, {4, -3}, {5, 1}, {6, 6}}
	first := run(t, 2, pts...)
	second := run(t, 2, pts...)
	if !samplesEq(first, second) {
		t.Fatalf("replay not identical: %v vs %v", first, second)
	}
}
