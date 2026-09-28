package timelog

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naiveScan 是逐条扫描的朴素参考实现，用于与索引查询对拍。
func naiveScan(msgs []Message, ts int64) SeekResult {
	for i, msg := range msgs {
		if msg.Timestamp >= ts {
			return SeekResult{Offset: int64(i), Found: true}
		}
	}
	return SeekResult{Offset: int64(len(msgs)), Found: false}
}

// checkAgainstScan 校验一次查询与朴素扫描一致，并打印输入、位点与判定依据。
func checkAgainstScan(t *testing.T, l *Log, msgs []Message, ts int64) SeekResult {
	t.Helper()
	got, err := l.SeekByTimestamp(ts)
	if err != nil {
		t.Fatalf("SeekByTimestamp(%d) 返回错误: %v", ts, err)
	}
	want := naiveScan(msgs, ts)
	basis := "未命中：所有消息时间戳均小于目标，返回结束位点"
	if want.Found {
		basis = fmt.Sprintf("命中：位点 %d 处时间戳 %d >= %d，且此前无更早满足者",
			want.Offset, msgs[want.Offset].Timestamp, ts)
	}
	t.Logf("输入 ts=%d => 位点=%d found=%v（结束位点=%d，索引项=%v）；判定依据：%s",
		ts, got.Offset, got.Found, l.EndOffset(), l.index, basis)
	if got != want {
		t.Fatalf("与朴素扫描不一致: got={%d %v}, want={%d %v}", got.Offset, got.Found, want.Offset, want.Found)
	}
	return got
}

func TestNonMonotonicSeek(t *testing.T) {
	l, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 非单调时间戳：新高 / 相等 / 回退 / 再次新高 / 重复。
	batch := []Message{
		{Timestamp: 10},
		{Timestamp: 5},
		{Timestamp: 10},
		{Timestamp: 20},
		{Timestamp: 7},
		{Timestamp: 20},
	}
	if err := l.Append(batch); err != nil {
		t.Fatalf("Append: %v", err)
	}
	t.Logf("追加后结束位点=%d，索引（仅严格新高）=%v", l.EndOffset(), l.index)

	wantIndex := []indexEntry{
		{timestamp: 10, offset: 0},
		{timestamp: 20, offset: 3},
	}
	if fmt.Sprint(l.index) != fmt.Sprint(wantIndex) {
		t.Fatalf("索引不符: got=%v want=%v（相等/回退不应入索引）", l.index, wantIndex)
	}

	for _, ts := range []int64{0, 1, 5, 6, 10, 11, 19, 20, 21, 100} {
		checkAgainstScan(t, l, batch, ts)
	}
}

func TestEmptyLogAndBoundaries(t *testing.T) {
	l, _ := New(Options{})
	// 空日志边界：任何查询都未命中，位点为 0。
	checkAgainstScan(t, l, nil, 0)

	msgs := []Message{{Timestamp: 0}, {Timestamp: 0}, {Timestamp: 1}}
	if err := l.Append(msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// ts=0 命中最小位点；相等时间戳只记首个新高。
	checkAgainstScan(t, l, msgs, 0)
	// ts=1 命中位点 2；ts=2 超过全局最大值，未命中并返回结束位点 3。
	checkAgainstScan(t, l, msgs, 1)
	checkAgainstScan(t, l, msgs, 2)

	if len(l.index) != 2 {
		t.Fatalf("索引长度=%d, want 2（0 与 1 两个严格新高）", len(l.index))
	}

	// 多批追加后继续对拍：回退时间戳不改变索引。
	more := []Message{{Timestamp: 1}, {Timestamp: 0}, {Timestamp: 50}}
	if err := l.Append(more); err != nil {
		t.Fatalf("Append: %v", err)
	}
	all := append(append([]Message{}, msgs...), more...)
	for _, ts := range []int64{0, 1, 2, 49, 50, 51} {
		checkAgainstScan(t, l, all, ts)
	}
}

func TestRejectedCallsLeaveNoTrace(t *testing.T) {
	l, err := New(Options{MaxEntries: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	good := []Message{{Timestamp: 1}, {Timestamp: 2}}
	if err := l.Append(good); err != nil {
		t.Fatalf("Append: %v", err)
	}
	beforeEnd := l.EndOffset()
	beforeIndex := fmt.Sprint(l.index)
	beforeMsgs := fmt.Sprint(l.messages)
	t.Logf("拒绝前快照: 结束位点=%d 索引=%v 消息=%v", beforeEnd, beforeIndex, beforeMsgs)

	cases := []struct {
		name     string
		op       func() error
		kind     ErrorKind
		sentinel error
	}{
		{"空批次", func() error { return l.Append(nil) }, KindInvalidArgument, ErrInvalidArgument},
		{"负时间戳", func() error { return l.Append([]Message{{Timestamp: 3}, {Timestamp: -1}}) }, KindNegativeTimestamp, ErrNegativeTimestamp},
		{"容量超限", func() error { return l.Append([]Message{{Timestamp: 3}, {Timestamp: 4}}) }, KindCapacityExceeded, ErrCapacityExceeded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.op()
			if err == nil {
				t.Fatal("期望被拒绝，实际成功")
			}
			var e *Error
			if !errors.As(err, &e) || e.Kind != c.kind {
				t.Fatalf("错误类别不符: got=%v want kind=%d", err, c.kind)
			}
			if !errors.Is(err, c.sentinel) {
				t.Fatalf("errors.Is 判定失败: %v vs %v", err, c.sentinel)
			}
			t.Logf("输入 %s 被拒绝: %v（类别=%d）", c.name, err, c.kind)
			if l.EndOffset() != beforeEnd || fmt.Sprint(l.index) != beforeIndex ||
				fmt.Sprint(l.messages) != beforeMsgs {
				t.Fatalf("拒绝后状态发生变化: end=%d index=%v msgs=%v",
					l.EndOffset(), l.index, l.messages)
			}
		})
	}

	// 非法配置与负查询时间同样被整体拒绝且互不混淆。
	if _, err := New(Options{MaxEntries: -1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("负容量应判为非法参数, got=%v", err)
	}
	if _, err := l.SeekByTimestamp(-1); !errors.Is(err, ErrNegativeTimestamp) {
		t.Fatalf("负查询时间应判为负时间戳错误, got=%v", err)
	}
	if l.EndOffset() != beforeEnd {
		t.Fatal("被拒绝的查询改变了结束位点")
	}
}

func TestErrorKindsDistinct(t *testing.T) {
	kinds := map[ErrorKind]string{
		KindInvalidArgument:   "invalid",
		KindNegativeTimestamp: "negative",
		KindCapacityExceeded:  "capacity",
	}
	seen := map[ErrorKind]bool{}
	for k, name := range kinds {
		if seen[k] {
			t.Fatalf("错误类别 %d 重复", k)
		}
		seen[k] = true
		t.Logf("错误类别 %d => %q", k, name)
	}
	// 跨类别 errors.Is 必须为 false。
	err := newError(KindCapacityExceeded, "Append", "x")
	if errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrNegativeTimestamp) {
		t.Fatal("不同错误类别被错误地视为相等")
	}
}

func TestRandomizedMatchesNaiveScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	l, _ := New(Options{})
	var msgs []Message
	for round := 0; round < 40; round++ {
		n := rng.Intn(8) + 1
		batch := make([]Message, n)
		for i := range batch {
			// 0..20 的时间戳刻意制造大量相等与回退。
			batch[i] = Message{Timestamp: int64(rng.Intn(21))}
		}
		if err := l.Append(batch); err != nil {
			t.Fatalf("Append: %v", err)
		}
		msgs = append(msgs, batch...)
		for q := 0; q < 10; q++ {
			ts := int64(rng.Intn(23))
			checkAgainstScan(t, l, msgs, ts)
		}
	}
}

func TestMonotonicSeekResults(t *testing.T) {
	l, _ := New(Options{})
	msgs := []Message{{Timestamp: 5}, {Timestamp: 2}, {Timestamp: 9}, {Timestamp: 7}}
	if err := l.Append(msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
	var prev int64
	for _, ts := range []int64{0, 1, 2, 3, 5, 6, 7, 8, 9, 10} {
		r := checkAgainstScan(t, l, msgs, ts)
		if ts > 0 {
			if r.Offset < prev {
				t.Fatalf("查询位点对时间非单调: ts=%d -> %d, 上一位点=%d",
					ts, r.Offset, prev)
			}
		}
		prev = r.Offset
	}
}

func TestExistingFirstHitStableUnderAppend(t *testing.T) {
	l, _ := New(Options{})
	base := []Message{{Timestamp: 5}, {Timestamp: 8}}
	if err := l.Append(base); err != nil {
		t.Fatalf("Append: %v", err)
	}
	snapshot := map[int64]SeekResult{}
	for _, ts := range []int64{0, 5, 6, 8, 9} {
		r, _ := l.SeekByTimestamp(ts)
		snapshot[ts] = r
	}

	// 并发追加与查询；追加更大、更小与相等的时间戳。
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				err := l.Append([]Message{{Timestamp: int64(1 + (w*7+i*3)%12)}})
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				for ts, want := range snapshot {
					got, err := l.SeekByTimestamp(ts)
					if err != nil {
						t.Errorf("Seek: %v", err)
						return
					}
					// 已存在的首个命中位点只能保持不变。
					if want.Found && got != want {
						t.Errorf("已存在的首个命中发生变化: ts=%d old={%d true} new=%v",
							ts, want.Offset, got)
					}
					// 原本未命中时，后续若命中，位点只能位于当时结束位点之后，且不回移。
					if !want.Found && got.Found && got.Offset < want.Offset {
						t.Errorf("新命中位点早于原结束位点: ts=%d new=%v oldEnd=%d",
							ts, got, want.Offset)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	t.Logf("并发追加结束: 结束位点=%d 索引项数=%d", l.EndOffset(), len(l.index))

	for ts, want := range snapshot {
		got, _ := l.SeekByTimestamp(ts)
		if want.Found && got != want {
			t.Fatalf("最终首个命中与初始不一致: ts=%d old=%v new=%v", ts, want, got)
		}
	}
}
