package logstore

import (
	"bytes"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
)

// blockSizeOf 镜像实现中的字节公式：17 字节定长头 + 键 + 值。
func blockSizeOf(key string, valLen int) uint64 {
	return blockHeaderSize + uint64(len(key)) + uint64(valLen)
}

func mustPut(t *testing.T, s *Store, key string, value []byte) {
	t.Helper()
	if err := s.Put(key, value); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func mustGet(t *testing.T, s *Store, key, want string) {
	t.Helper()
	got, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q)=%q, want %q", key, got, want)
	}
}

// TestRejectReasons 覆盖三类可区分拒绝：空键、块超段大小、删除不存在键；
// 被拒绝的写入不得追加任何块。
func TestRejectReasons(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{SegmentSize: 40, MaxSegments: 3, Logger: &buf})

	if err := s.Put("", []byte("x")); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key err=%v, want ErrEmptyKey", err)
	}
	if err := s.Put("k", make([]byte, 23)); !errors.Is(err, ErrValueTooLarge) {
		t.Fatalf("oversize err=%v, want ErrValueTooLarge", err)
	}
	if err := s.Delete("ghost"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("delete missing err=%v, want ErrKeyNotFound", err)
	}

	segs, clock, active := s.Snapshot()
	if len(segs) != 0 || clock != 0 || active != -1 {
		t.Fatalf("rejected ops must append nothing: segs=%+v clock=%d active=%d", segs, clock, active)
	}

	mustPut(t, s, "k", []byte("v"))
	if err := s.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	usedAfterDelete := s.segments[s.activeID].used
	if err := s.Delete("k"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("second delete err=%v", err)
	}
	if s.segments[s.activeID].used != usedAfterDelete {
		t.Fatalf("rejected delete appended bytes")
	}
	if !s.verifyAccounting() {
		t.Fatalf("accounting invariant broken")
	}
}

// TestZeroLiveTieLowestID 两个存活率均为 0 的已封存段并列，
// 直接回收段号最小者；日志须给出判定依据。
func TestZeroLiveTieLowestID(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{SegmentSize: 200, MaxSegments: 8, Logger: &buf})

	// 旧块 169 字节独占一段；seg0..seg3 各放一个旧版本。
	mustPut(t, s, "k0", make([]byte, 150)) // 169 seg0 ts1
	mustPut(t, s, "k1", make([]byte, 120)) // 139 seg1 ts2
	mustPut(t, s, "k2", make([]byte, 120)) // 139 seg2 ts3
	mustPut(t, s, "k3", make([]byte, 120)) // 139 seg3 ts4
	mustPut(t, s, "k0", make([]byte, 150)) // 169 seg4 ts5，覆盖 seg0 旧块
	mustPut(t, s, "k1", make([]byte, 20))  // 39 => seg5 ts6（seg4 剩31）
	mustPut(t, s, "k2", make([]byte, 20))  // 39 seg5 ts7
	mustPut(t, s, "k3", make([]byte, 20))  // 39 seg5 ts8，当前段

	if s.live[0] != 0 || s.live[1] != 0 {
		t.Fatalf("seg0/seg1 should be zero-live, live=%v", s.live)
	}
	buf.Reset()
	ok, err := s.CleanOnce()
	if err != nil || !ok {
		t.Fatalf("CleanOnce ok=%v err=%v", ok, err)
	}
	if _, exists := s.segments[0]; exists {
		t.Fatalf("lowest zero-live seg0 must be reclaimed")
	}
	if _, exists := s.segments[1]; !exists {
		t.Fatalf("seg1 must remain after single clean")
	}
	if !strings.Contains(buf.String(), "zero-live seg=0") {
		t.Fatalf("missing decision basis in log:\n%s", buf.String())
	}
	if ok, _ := s.CleanOnce(); !ok {
		t.Fatalf("second clean should reclaim seg1")
	}
	if _, exists := s.segments[1]; exists {
		t.Fatalf("seg1 should be reclaimed on second clean")
	}
	mustGet(t, s, "k0", string(make([]byte, 150)))
	mustGet(t, s, "k3", string(make([]byte, 20)))
	if !s.verifyAccounting() {
		t.Fatalf("accounting broken")
	}
}

// TestRationalScoreCompare 精确有理数比较：交叉相乘，无浮点误差。
func TestRationalScoreCompare(t *testing.T) {
	big1 := big.NewInt(1)
	huge := new(big.Int).Lsh(big1, 128)
	cases := []struct {
		a, b score
		want int
	}{
		{score{big.NewInt(2), big.NewInt(3)}, score{big.NewInt(4), big.NewInt(6)}, 0},
		{score{big.NewInt(3), big.NewInt(4)}, score{big.NewInt(2), big.NewInt(3)}, 1},
		{score{big.NewInt(1), big.NewInt(3)}, score{big.NewInt(1), big.NewInt(2)}, -1},
		{score{new(big.Int).Sub(new(big.Int).Set(huge), big1), new(big.Int).Set(huge)},
			score{big1, big1}, -1},
	}
	for i, c := range cases {
		if got := compareScore(c.a, c.b); got != c.want {
			t.Fatalf("case %d: compare=%d want %d", i, got, c.want)
		}
	}
}

// TestCostBenefitSelection 构造两个存活率不同、年龄不同的已封存段，
// 验证非零存活率下按 (1-r)*age/(1+r) 的精确值选出高代价收益段。
func TestCostBenefitSelection(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{SegmentSize: 100, MaxSegments: 5, Logger: &buf})
	mustPut(t, s, "keep0", make([]byte, 30)) // 49 seg0 ts1
	mustPut(t, s, "dead0", make([]byte, 20)) // 39 seg0 ts2
	mustPut(t, s, "keep1", make([]byte, 76)) // 98 seg1 ts3
	// 用一个新段覆盖 dead0（20 字节块 -> seg2 ts4），seg0 live 变为 49。
	mustPut(t, s, "dead0", []byte("x")) // 22 => seg2 ts4
	// 再写大块封存 seg2，进入 seg3，并让 seg0 年龄大于 seg1。
	mustPut(t, s, "pad", make([]byte, 70)) // 90 => seg3 ts5

	if s.live[0] != blockSizeOf("keep0", 30) {
		t.Fatalf("seg0 live=%d want %d", s.live[0], blockSizeOf("keep0", 30))
	}
	if s.live[1] != blockSizeOf("keep1", 76) {
		t.Fatalf("seg1 live=%d want %d", s.live[1], blockSizeOf("keep1", 76))
	}
	buf.Reset()
	ok, err := s.CleanOnce()
	if err != nil || !ok {
		t.Fatalf("CleanOnce %v %v", ok, err)
	}
	// seg0: r=52/100, age=5-2=3 => score=(48*3)/(152)=144/152
	// seg1: r=98/100, age=2     => score=4/198
	// seg2: r=23/100, age=1     => score=77/123
	if _, exists := s.segments[1]; !exists {
		t.Fatalf("seg1 must remain (low benefit)")
	}
	if !strings.Contains(buf.String(), "pickVictim score seg=0 value=144/152") {
		t.Fatalf("must log exact rational winner:\n%s", buf.String())
	}
	mustGet(t, s, "keep0", string(make([]byte, 30)))
	mustGet(t, s, "keep1", string(make([]byte, 76)))
	if !s.verifyAccounting() {
		t.Fatalf("accounting broken")
	}
}

// TestTombstoneRetention 墓碑在旧块仍于其他段时存活；
// 旧块所在段被回收后墓碑存活字节归零；删除不存在键拒绝且不追加。
func TestTombstoneRetention(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{SegmentSize: 60, MaxSegments: 6, Logger: &buf})

	mustPut(t, s, "key", make([]byte, 20))  // 40 独占段0 ts1
	mustPut(t, s, "bigA", make([]byte, 36)) // 57 => seg1 ts2
	if err := s.Delete("key"); err != nil { // 20 => seg2 ts3
		t.Fatalf("Delete: %v", err)
	}
	tombSeg := s.activeID
	if s.live[tombSeg] != blockSizeOf("key", 0) {
		t.Fatalf("tombstone must be live while older block lives in another seg, live=%d", s.live[tombSeg])
	}
	if _, err := s.Get("key"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get deleted key err=%v", err)
	}

	before := s.segments[s.activeID].used
	if err := s.Delete("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("delete missing err=%v", err)
	}
	if s.segments[s.activeID].used != before {
		t.Fatalf("rejected delete appended data")
	}

	mustPut(t, s, "bigB", make([]byte, 36)) // 封存墓碑段，进入新段
	if ok, err := s.CleanOnce(); err != nil || !ok {
		t.Fatalf("CleanOnce ok=%v err=%v", ok, err)
	}
	if _, exists := s.segments[0]; exists {
		t.Fatalf("seg0 holding dead old value must be reclaimed")
	}
	if s.live[tombSeg] != 0 {
		t.Fatalf("tombstone must be dead after old block reclaimed, live=%d", s.live[tombSeg])
	}
	if _, err := s.Get("key"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get after reclaim err=%v", err)
	}
	if !s.verifyAccounting() {
		t.Fatalf("accounting broken")
	}
	if !strings.Contains(buf.String(), "older-block-in-other-segment") ||
		!strings.Contains(buf.String(), "no-older-block-elsewhere") {
		t.Fatalf("log must explain both tombstone keep/drop decisions:\n%s", buf.String())
	}
}

// TestMigrationKeepsTimestamp 存活块按原写入顺序搬迁、保留原 ts；
// 搬迁不推进逻辑时钟，年龄（clock-maxTS）不重置。
func TestMigrationKeepsTimestamp(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{SegmentSize: 80, MaxSegments: 4, Logger: &buf})

	mustPut(t, s, "alpha", []byte("a"))     // 23 seg0 ts1
	mustPut(t, s, "beta", []byte("b"))      // 22 seg0 ts2
	mustPut(t, s, "big1", make([]byte, 31)) // 52 seg1 ts3
	mustPut(t, s, "big2", make([]byte, 31)) // seg2 ts4
	mustPut(t, s, "big3", make([]byte, 31)) // seg3 ts5

	if s.segments[0].maxTS != 2 {
		t.Fatalf("seg0 maxTS=%d want 2", s.segments[0].maxTS)
	}
	if ok, err := s.CleanOnce(); err != nil || !ok {
		t.Fatalf("CleanOnce %v %v", ok, err)
	}
	if s.clock != 5 {
		t.Fatalf("migration must not advance clock, got %d", s.clock)
	}
	if s.index["alpha"][0].ts != 1 || s.index["beta"][0].ts != 2 {
		t.Fatalf("migrated blocks must keep original ts")
	}
	var order []uint64
	type tb struct {
		ts  uint64
		off uint64
		id  int
	}
	var found []tb
	for _, seg := range s.segments {
		for _, b := range seg.blocks {
			if b.key == "alpha" || b.key == "beta" {
				found = append(found, tb{b.ts, 0, seg.id})
			}
		}
	}
	for i := 1; i < len(found); i++ {
		for j := i; j > 0 && (found[j-1].ts > found[j].ts); j-- {
			found[j-1], found[j] = found[j], found[j-1]
		}
	}
	for _, f := range found {
		order = append(order, f.ts)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("migration order not preserved: %v", order)
	}
	mustGet(t, s, "alpha", "a")
	mustGet(t, s, "beta", "b")
	if !s.verifyAccounting() {
		t.Fatalf("accounting broken")
	}
	if !strings.Contains(buf.String(), "ts preserved") {
		t.Fatalf("log must record ts preservation:\n%s", buf.String())
	}
}

// TestSpaceExhaustedAndTriggeredClean 覆盖写入触发清理的两种结果。
func TestSpaceExhaustedAndTriggeredClean(t *testing.T) {
	var buf bytes.Buffer

	t.Run("triggered-clean-succeeds", func(t *testing.T) {
		s := New(Options{SegmentSize: 40, MaxSegments: 2, Logger: &buf})
		mustPut(t, s, "key", make([]byte, 15))   // 35 seg0
		mustPut(t, s, "key", make([]byte, 15))   // 35 seg1，seg0 存活率0
		mustPut(t, s, "z", []byte("0123456789")) // 28，触发清理并复用段0
		if s.activeID != 0 {
			t.Fatalf("expected reuse of lowest free id 0, active=%d", s.activeID)
		}
		mustGet(t, s, "z", "0123456789")
		mustGet(t, s, "key", string(make([]byte, 15)))
		if !s.verifyAccounting() {
			t.Fatalf("accounting broken")
		}
	})

	t.Run("no-net-free-rejected", func(t *testing.T) {
		s := New(Options{SegmentSize: 40, MaxSegments: 2, Logger: &buf})
		mustPut(t, s, "aaa", make([]byte, 16)) // 36 seg0
		mustPut(t, s, "bbb", make([]byte, 16)) // 36 seg1
		beforeUsed := s.segments[s.activeID].used
		beforeClock := s.clock
		err := s.Put("ccc", make([]byte, 10)) // 31；活动段剩4放不下
		if !errors.Is(err, ErrSpaceExhausted) {
			t.Fatalf("want ErrSpaceExhausted, got %v", err)
		}
		if s.segments[s.activeID].used != beforeUsed || s.clock != beforeClock {
			t.Fatalf("rejected write mutated state: used %d->%d clock %d->%d",
				beforeUsed, s.segments[s.activeID].used, beforeClock, s.clock)
		}
		if len(s.segments) != 2 {
			t.Fatalf("rejected write must not trigger partial clean")
		}
		if !s.verifyAccounting() {
			t.Fatalf("accounting broken")
		}
		if !strings.Contains(buf.String(), "space-exhausted") {
			t.Fatalf("log must explain space exhaustion:\n%s", buf.String())
		}
	})
}

// TestConcurrentReadWriteClean 并发读、写、清理：任何时刻读到最新值，
// 且账目不变式始终成立；race 检测器负责捕获数据竞争。
func TestConcurrentReadWriteClean(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{SegmentSize: 128, MaxSegments: 8, Logger: &buf})

	const writers = 4
	const rounds = 60
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				key := "k" + itoa(w)
				val := itoa(i)
				if i%7 == 0 && i > 0 {
					if err := s.Delete(key); err != nil &&
						!errors.Is(err, ErrKeyNotFound) &&
						!errors.Is(err, ErrSpaceExhausted) {
						t.Errorf("unexpected delete err: %v", err)
						return
					}
					continue
				}
				if err := s.Put(key, []byte(val)); err != nil &&
					!errors.Is(err, ErrSpaceExhausted) {
					t.Errorf("unexpected put err: %v", err)
					return
				}
			}
		}(w)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if _, err := s.CleanOnce(); err != nil {
				t.Errorf("clean err: %v", err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 400; i++ {
			for w := 0; w < writers; w++ {
				v, err := s.Get("k" + itoa(w))
				if err != nil && !errors.Is(err, ErrKeyNotFound) {
					t.Errorf("reader err: %v", err)
					return
				}
				if err == nil && v == nil {
					t.Errorf("got nil value without error")
					return
				}
			}
		}
	}()

	wg.Wait()
	if !s.verifyAccounting() {
		t.Fatalf("accounting broken after concurrent run")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestDeterministicLayout 同一操作序列两次运行得到相同的清理顺序与段布局。
func TestDeterministicLayout(t *testing.T) {
	run := func() []SegmentInfo {
		var buf bytes.Buffer
		s := New(Options{SegmentSize: 96, MaxSegments: 5, Logger: &buf})
		ops := []struct {
			op     string
			key    string
			valLen int
		}{
			{"put", "a", 30}, {"put", "b", 30}, {"put", "a", 5},
			{"put", "c", 40}, {"del", "a", 0}, {"put", "d", 40},
			{"put", "e", 10}, {"clean", "", 0}, {"clean", "", 0},
			{"put", "f", 50}, {"clean", "", 0},
		}
		for _, op := range ops {
			switch op.op {
			case "put":
				if err := s.Put(op.key, make([]byte, op.valLen)); err != nil {
					t.Fatalf("put %s: %v", op.key, err)
				}
			case "del":
				if err := s.Delete(op.key); err != nil && !errors.Is(err, ErrKeyNotFound) {
					t.Fatalf("del %s: %v", op.key, err)
				}
			case "clean":
				if _, err := s.CleanOnce(); err != nil {
					t.Fatalf("clean: %v", err)
				}
			}
		}
		segs, _, _ := s.Snapshot()
		if !s.verifyAccounting() {
			t.Fatalf("accounting broken")
		}
		return segs
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("layout length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("nondeterministic layout at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}
