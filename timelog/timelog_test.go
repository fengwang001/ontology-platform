package timelog

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naiveScan 朴素逐条扫描，作为索引查询的对照基准。
func naiveScan(l *Log, t int64) (Position, bool) {
	end := l.End()
	for p := Position(0); p < end; p++ {
		m, _ := l.Message(p)
		if m.Timestamp >= t {
			return p, true
		}
	}
	return end, false
}

// appendOK 追加一条消息，失败则终止测试。
func appendOK(t *testing.T, l *Log, ts int64, payload string) Position {
	t.Helper()
	pos, err := l.Append(ts, []byte(payload))
	if err != nil {
		t.Fatalf("Append(ts=%d) 意外失败: %v", ts, err)
	}
	t.Logf("追加 ts=%d payload=%q -> 位点=%d", ts, payload, pos)
	return pos
}

// queryOK 查询并与朴素扫描对照，打印判定依据。
func queryOK(t *testing.T, l *Log, target int64) (Position, bool) {
	t.Helper()
	pos, found, err := l.Query(target)
	if err != nil {
		t.Fatalf("Query(%d) 意外失败: %v", target, err)
	}
	wantPos, wantFound := naiveScan(l, target)
	basis := "索引二分命中"
	if !found {
		basis = "索引中无 ts >= 目标，返回结束位点"
	}
	t.Logf("查询 t=%d -> 位点=%d found=%v（依据: %s；朴素扫描: 位点=%d found=%v）",
		target, pos, found, basis, wantPos, wantFound)
	if pos != wantPos || found != wantFound {
		t.Fatalf("Query(%d) = (%d, %v)，与朴素扫描 (%d, %v) 不一致",
			target, pos, found, wantPos, wantFound)
	}
	return pos, found
}

// TestNonMonotonicTimestamps 覆盖乱序时间戳下的命中与未命中。
func TestNonMonotonicTimestamps(t *testing.T) {
	l := New(0)
	// 乱序：索引应只记录 5、9、8 之后的严格新高，即位点 0(5)、1(9)。
	seq := []int64{5, 9, 3, 9, 1, 8, 9, 2}
	for i, ts := range seq {
		appendOK(t, l, ts, fmt.Sprintf("m%d", i))
	}
	if got, want := l.IndexLen(), 2; got != want {
		t.Fatalf("索引项数 = %d，期望 %d（相等与回落均不记录）", got, want)
	}

	cases := []struct {
		target    int64
		wantPos   Position
		wantFound bool
	}{
		{0, 0, true},   // 小于所有时间戳，命中首条
		{5, 0, true},   // 恰好等于首个索引项
		{6, 1, true},   // 落在两个索引项之间
		{9, 1, true},   // 等于最大时间戳，命中位点 1 而非后续的 3、6
		{10, 8, false}, // 超过所有时间戳，返回结束位点
	}
	for _, c := range cases {
		pos, found := queryOK(t, l, c.target)
		if pos != c.wantPos || found != c.wantFound {
			t.Fatalf("Query(%d) = (%d, %v)，期望 (%d, %v)",
				c.target, pos, found, c.wantPos, c.wantFound)
		}
	}
}

// TestBoundaries 覆盖空日志、单条、首位与末位等边界。
func TestBoundaries(t *testing.T) {
	l := New(0)
	t.Logf("空日志: 结束位点=%d 索引项=%d", l.End(), l.IndexLen())
	if pos, found := queryOK(t, l, 0); found || pos != 0 {
		t.Fatalf("空日志 Query(0) = (%d, %v)，期望 (0, false)", pos, found)
	}

	appendOK(t, l, 0, "zero") // 边界时间戳 0
	if pos, found := queryOK(t, l, 0); !found || pos != 0 {
		t.Fatalf("Query(0) = (%d, %v)，期望 (0, true)", pos, found)
	}
	if pos, found := queryOK(t, l, 1); found || pos != 1 {
		t.Fatalf("Query(1) = (%d, %v)，期望结束位点 (1, false)", pos, found)
	}
}

// TestRejections 覆盖非法输入：原因互不相同且拒绝后状态不变。
func TestRejections(t *testing.T) {
	l := New(3)
	appendOK(t, l, 4, "a")
	appendOK(t, l, 2, "b")
	appendOK(t, l, 7, "c") // 已满

	snapshot := func() (Position, int, []Message) {
		end, idxLen := l.End(), l.IndexLen()
		msgs := make([]Message, 0, end)
		for p := Position(0); p < end; p++ {
			m, _ := l.Message(p)
			msgs = append(msgs, m)
		}
		return end, idxLen, msgs
	}
	assertUnchanged := func(step string, end Position, idxLen int, msgs []Message) {
		t.Helper()
		end2, idxLen2, msgs2 := snapshot()
		if end2 != end || idxLen2 != idxLen || len(msgs2) != len(msgs) {
			t.Fatalf("%s 后状态被改变: end %d->%d, 索引 %d->%d",
				step, end, end2, idxLen, idxLen2)
		}
		for i := range msgs {
			if msgs2[i].Timestamp != msgs[i].Timestamp ||
				string(msgs2[i].Payload) != string(msgs[i].Payload) {
				t.Fatalf("%s 后消息 %d 被改变", step, i)
			}
		}
		t.Logf("%s 被拒绝后状态不变: end=%d 索引项=%d 消息数=%d",
			step, end2, idxLen2, len(msgs2))
	}

	end, idxLen, msgs := snapshot()

	// 负时间戳追加
	if _, err := l.Append(-1, []byte("x")); !errors.Is(err, ErrNegativeTimestamp) {
		t.Fatalf("Append(-1) err = %v，期望 ErrNegativeTimestamp", err)
	}
	t.Logf("追加 ts=-1 -> 拒绝: %v", ErrNegativeTimestamp)
	assertUnchanged("负时间戳追加", end, idxLen, msgs)

	// 空载荷
	if _, err := l.Append(10, nil); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("Append(10, nil) err = %v，期望 ErrEmptyPayload", err)
	}
	t.Logf("追加空载荷 -> 拒绝: %v", ErrEmptyPayload)
	assertUnchanged("空载荷追加", end, idxLen, msgs)

	// 容量超限
	if _, err := l.Append(10, []byte("x")); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("容量满时 Append err = %v，期望 ErrCapacityExceeded", err)
	}
	t.Logf("容量满时追加 -> 拒绝: %v", ErrCapacityExceeded)
	assertUnchanged("容量超限追加", end, idxLen, msgs)

	// 负时间戳查询
	if _, _, err := l.Query(-5); !errors.Is(err, ErrNegativeTimestamp) {
		t.Fatalf("Query(-5) err = %v，期望 ErrNegativeTimestamp", err)
	}
	t.Logf("查询 t=-5 -> 拒绝: %v", ErrNegativeTimestamp)
	assertUnchanged("负时间戳查询", end, idxLen, msgs)

	// 三种拒绝原因互不相同、可区分
	if errors.Is(ErrNegativeTimestamp, ErrEmptyPayload) ||
		errors.Is(ErrNegativeTimestamp, ErrCapacityExceeded) ||
		errors.Is(ErrEmptyPayload, ErrCapacityExceeded) {
		t.Fatal("拒绝原因之间必须互不相同、可区分")
	}
}

// TestRejectedAppendLeavesNoTrace 验证失败追加不留痕：
// 后续成功追加的位点紧接既有结束位点。
func TestRejectedAppendLeavesNoTrace(t *testing.T) {
	l := New(0)
	appendOK(t, l, 1, "a")
	if _, err := l.Append(-1, []byte("x")); err == nil {
		t.Fatal("负时间戳应被拒绝")
	}
	if _, err := l.Append(2, nil); err == nil {
		t.Fatal("空载荷应被拒绝")
	}
	pos := appendOK(t, l, 2, "b")
	if pos != 1 {
		t.Fatalf("失败追加留下痕迹: 后续位点 = %d，期望 1", pos)
	}
}

// TestMatchesNaiveScan 随机乱序序列下与朴素扫描逐点对照。
func TestMatchesNaiveScan(t *testing.T) {
	rng := rand.New(rand.NewSource(314))
	for trial := 0; trial < 20; trial++ {
		l := New(0)
		n := 1 + rng.Intn(200)
		for i := 0; i < n; i++ {
			ts := rng.Int63n(50) // 小值域制造大量重复与回落
			if _, err := l.Append(ts, []byte{byte(i)}); err != nil {
				t.Fatalf("trial %d 追加失败: %v", trial, err)
			}
		}
		for target := int64(0); target <= 51; target++ {
			pos, found, err := l.Query(target)
			if err != nil {
				t.Fatalf("trial %d Query(%d) 失败: %v", trial, target, err)
			}
			wantPos, wantFound := naiveScan(l, target)
			if pos != wantPos || found != wantFound {
				t.Fatalf("trial %d Query(%d) = (%d, %v)，朴素扫描 = (%d, %v)",
					trial, target, pos, found, wantPos, wantFound)
			}
		}
		t.Logf("trial %d: n=%d 索引项=%d，目标 0..51 全部与朴素扫描一致",
			trial, n, l.IndexLen())
	}
}

// TestQueryMonotonicity 验证查询结果对时间单调不减。
func TestQueryMonotonicity(t *testing.T) {
	l := New(0)
	for i, ts := range []int64{7, 2, 9, 9, 0, 4, 12, 12, 3} {
		appendOK(t, l, ts, fmt.Sprintf("m%d", i))
	}
	prev := Position(0)
	for target := int64(0); target <= 13; target++ {
		pos, _ := queryOK(t, l, target)
		if pos < prev {
			t.Fatalf("查询结果非单调: Query(%d)=%d < Query(%d)=%d",
				target, pos, target-1, prev)
		}
		prev = pos
	}
	t.Logf("目标 0..13 的查询位点单调不减，末位点=%d", prev)
}

// TestConcurrentAppendAndQuery 并发追加与查询：
// 已存在的首个命中不因追加而改变，且查询永不回退到扫描。
func TestConcurrentAppendAndQuery(t *testing.T) {
	l := New(0)
	const writers = 4
	const perWriter = 250

	// 先写入基准数据并记录各目标的首个命中。
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		appendOK(t, l, rng.Int63n(1000), fmt.Sprintf("base%d", i))
	}
	targets := []int64{0, 1, 250, 500, 999, 1000}
	baseline := make(map[int64]Position)
	for _, target := range targets {
		pos, _ := queryOK(t, l, target)
		baseline[target] = pos
	}

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < perWriter; i++ {
				if _, err := l.Append(r.Int63n(2000), []byte{byte(id), byte(i)}); err != nil {
					errCh <- fmt.Errorf("writer %d: %w", id, err)
					return
				}
			}
		}(w)
	}
	for q := 0; q < 8; q++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(100 + id)))
			for i := 0; i < 500; i++ {
				target := targets[r.Intn(len(targets))]
				pos, found, err := l.Query(target)
				if err != nil {
					errCh <- fmt.Errorf("querier %d: %w", id, err)
					return
				}
				// 追加只可能让更小的命中位点出现吗？不会：
				// 只追加日志的既有前缀不变，首个命中位点不可变。
				if found && pos != baseline[target] {
					errCh <- fmt.Errorf("querier %d: Query(%d)=%d，基准=%d（首个命中被追加改变）",
						id, target, pos, baseline[target])
					return
				}
			}
		}(q)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	t.Logf("并发完成: %d 个写者 x %d 条，8 个读者 x 500 次；结束位点=%d 索引项=%d，首个命中均未改变",
		writers, perWriter, l.End(), l.IndexLen())
}
