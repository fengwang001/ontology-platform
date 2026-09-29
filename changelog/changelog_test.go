package changelog

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// rawWrite 是独立串行参照模型使用的原始写入。
type rawWrite struct {
	key, value string
}

// serialModel 是与 Log 实现完全独立的串行参照：
// 保存全部追加历史，压缩时只对区间内当前存活的同键条目保留最后一条；
// 位点读取序号不超过 position 的存活条目里序号最大者。
type serialModel struct {
	writes []rawWrite // 追加后不变
	alive  []bool
}

func newSerialModel() *serialModel { return &serialModel{} }

func (m *serialModel) append(key, value string) int {
	m.writes = append(m.writes, rawWrite{key, value})
	m.alive = append(m.alive, true)
	return len(m.writes)
}

// compact 是只看区间 [left,right] 内条目的批量参照。
func (m *serialModel) compact(left, right int) map[string]CompactRecord {
	last := map[string]int{}
	for i := left - 1; i <= right-1 && i < len(m.writes); i++ {
		if m.alive[i] {
			last[m.writes[i].key] = i
		}
	}
	for i := left - 1; i <= right-1 && i < len(m.writes); i++ {
		if m.alive[i] && last[m.writes[i].key] != i {
			m.alive[i] = false
		}
	}
	out := map[string]CompactRecord{}
	for k, idx := range last {
		out[k] = CompactRecord{Seq: idx + 1, Key: k, Value: m.writes[idx].value}
	}
	return out
}

// read 的 ok=false 表示该位点无可见条目（对应 ErrNotFound）。
func (m *serialModel) read(key string, position int) (Entry, bool) {
	for i := position - 1; i >= 0; i-- {
		if m.alive[i] && m.writes[i].key == key {
			return Entry{Seq: i + 1, Key: key, Value: m.writes[i].value}, true
		}
	}
	return Entry{}, false
}

// modelSnapshot 是并发测试用的不可变参照快照（只读前缀）。
type modelSnapshot struct {
	writes []rawWrite
	alive  []bool
}

func (m *serialModel) snapshot(size int) modelSnapshot {
	return modelSnapshot{
		writes: append([]rawWrite(nil), m.writes[:size]...),
		alive:  append([]bool(nil), m.alive[:size]...),
	}
}

func (s modelSnapshot) read(key string, position int) (Entry, bool) {
	for i := position - 1; i >= 0; i-- {
		if s.alive[i] && s.writes[i].key == key {
			return Entry{Seq: i + 1, Key: key, Value: s.writes[i].value}, true
		}
	}
	return Entry{}, false
}

func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func checkEntry(t *testing.T, label string, l *Log, key string, pos int, want Entry, wantFound bool) {
	t.Helper()
	got, err := l.Read(key, pos)
	switch {
	case !wantFound:
		ok := errors.Is(err, ErrNotFound)
		logf(t, "%s: 输入=Read(key=%q,读位点=%d) 返回值=(Entry{},err=%v) 判定=%s（依据：参照在该位点无可见条目，应 ErrNotFound）",
			label, key, pos, err, passFail(ok))
		if !ok {
			t.Fatalf("%s: Read(%q,%d) err=%v, want ErrNotFound", label, key, pos, err)
		}
	case err != nil:
		logf(t, "%s: 输入=Read(key=%q,读位点=%d) 返回值=(err=%v) 判定=FAIL（参照 %+v 应存在）", label, key, pos, err, want)
		t.Fatalf("%s: Read(%q,%d) unexpected err %v", label, key, pos, err)
	default:
		ok := got == want
		logf(t, "%s: 输入=Read(key=%q,读位点=%d) 返回值=%+v 判定=%s（依据：可见条目中序号最大者，参照=%+v）",
			label, key, pos, got, passFail(ok), want)
		if !ok {
			t.Fatalf("%s: Read(%q,%d)=%+v, want %+v", label, key, pos, got, want)
		}
	}
}

func equalRecordViews(a, b []CompactRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCompactKeepsLastInRange 覆盖：区间内同键多条只留最后一条；
// 区间外条目原样保留；区间内无写入的键不产生压缩记录；
// 压缩后任意位点仍可寻址；不存在的键返回 ErrNotFound。
func TestCompactKeepsLastInRange(t *testing.T) {
	l := New()
	m := newSerialModel()
	appendBoth := func(key, value string) {
		seq, err := l.Append(key, value)
		if err != nil {
			t.Fatalf("Append(%q) err=%v", key, err)
		}
		mseq := m.append(key, value)
		logf(t, "输入=Append(key=%q,value=%q) 返回值=seq=%d 判定=%s（参照 seq=%d）", key, value, seq, passFail(seq == mseq), mseq)
	}
	appendBoth("a", "a1") // 1 区间内，被合并
	appendBoth("b", "b1") // 2 区间外（之前）
	appendBoth("a", "a2") // 3 区间内，最后一条，保留
	appendBoth("c", "c1") // 4 区间内，单条，保留
	appendBoth("a", "a3") // 5 区间外（之后）

	logf(t, "输入=Compact(left=1,right=4) 区间=[1,4]；区间外位点 2(b),5(a) 原样保留")
	recs, err := l.Compact(1, 4)
	if err != nil {
		t.Fatalf("Compact err=%v", err)
	}
	wantRecs := m.compact(1, 4)
	if len(recs) != len(wantRecs) {
		t.Fatalf("compact records=%v, want %v", recs, wantRecs)
	}
	for _, r := range recs {
		wr, ok := wantRecs[r.Key]
		ok = ok && r == wr
		logf(t, "压缩返回记录 key=%q -> %+v 判定=%s（参照=%+v）", r.Key, r, passFail(ok), wr)
		if !ok {
			t.Fatalf("compact record %+v, want %+v", r, wr)
		}
	}
	logf(t, "判定=PASS（依据：产生 a,b,c 三个键的记录（b 是区间内单条，原样保留为压缩记录）；区间内无写入的键 d 不产生记录，共 %d 条）", len(recs))

	for pos := 1; pos <= 5; pos++ {
		for _, key := range []string{"a", "b", "c", "d"} {
			want, found := m.read(key, pos)
			checkEntry(t, fmt.Sprintf("压缩后位点%d", pos), l, key, pos, want, found)
		}
		if err := l.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck at pos %d: %v", pos, err)
		}
	}
	checkEntry(t, "区间外不动-b", l, "b", 5, Entry{Seq: 2, Key: "b", Value: "b1"}, true)
	checkEntry(t, "区间外不动-a3", l, "a", 5, Entry{Seq: 5, Key: "a", Value: "a3"}, true)
	logf(t, "判定=PASS（依据：压缩后位点 1..5 全部键与批量参照逐值相同，SelfCheck 无错）")
}

// TestReadsAfterCompactionEveryPosition 覆盖多次（含重叠）压缩后任意位点可寻址。
func TestReadsAfterCompactionEveryPosition(t *testing.T) {
	l := New()
	m := newSerialModel()
	keys := []string{"x", "y", "x", "x", "y", "z", "x"}
	for seq, key := range keys {
		value := fmt.Sprintf("%s%d", key, seq+1)
		if _, err := l.Append(key, value); err != nil {
			t.Fatal(err)
		}
		m.append(key, value)
	}
	for _, cr := range [][2]int{{1, 7}, {2, 3}, {5, 6}} {
		if _, err := l.Compact(cr[0], cr[1]); err != nil {
			t.Fatalf("Compact(%d,%d) err=%v", cr[0], cr[1], err)
		}
		m.compact(cr[0], cr[1])
		logf(t, "输入=Compact(%d,%d) 完成，开始全位点逐值核对", cr[0], cr[1])
		for pos := 1; pos <= 7; pos++ {
			for _, key := range []string{"x", "y", "z", "q"} {
				want, found := m.read(key, pos)
				checkEntry(t, fmt.Sprintf("Compact%d-%d/位点%d", cr[0], cr[1], pos), l, key, pos, want, found)
			}
		}
		if err := l.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck after Compact(%d,%d): %v", cr[0], cr[1], err)
		}
	}
	logf(t, "判定=PASS（依据：多次重叠压缩后位点 1..7 对全部键与串行参照逐值相同）")
}

// TestInvalidInputsRejected 覆盖空键、位点越界、区间非法的可区分拒绝，
// 并验证一次失败不改变日志与位点映射。
func TestInvalidInputsRejected(t *testing.T) {
	l := New()
	for _, kv := range [][2]string{{"a", "1"}, {"a", "2"}, {"b", "3"}} {
		if _, err := l.Append(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	beforeRecs := l.LastCompactRecords()

	_, err := l.Append("", "v")
	logf(t, "输入=Append(key=\"\") 返回值=err=%v 判定=%s（依据：空键拒绝 ErrEmptyKey）", err, passFail(errors.Is(err, ErrEmptyKey)))
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Append empty key err=%v", err)
	}
	_, err = l.Read("", 1)
	logf(t, "输入=Read(key=\"\",读位点=1) 返回值=err=%v 判定=%s（依据：空键拒绝 ErrEmptyKey）", err, passFail(errors.Is(err, ErrEmptyKey)))
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Read empty key err=%v", err)
	}

	badReads := []struct {
		pos int
		why string
	}{
		{0, "位点 0 < 1"},
		{-3, "负位点"},
		{4, "位点 4 超过 NextSeq-1=3"},
	}
	for _, tc := range badReads {
		_, err = l.Read("a", tc.pos)
		logf(t, "输入=Read(key=\"a\",读位点=%d) 返回值=err=%v 判定=%s（依据：%s）", tc.pos, err, passFail(errors.Is(err, ErrPositionOutOfRange)), tc.why)
		if !errors.Is(err, ErrPositionOutOfRange) {
			t.Fatalf("Read pos %d err=%v", tc.pos, err)
		}
	}

	badRanges := []struct {
		left, right int
		why         string
	}{
		{0, 3, "左边界 0 < 1"},
		{-1, 2, "左边界为负"},
		{1, 4, "右边界 4 超过最后序号 3"},
		{2, 1, "左右倒置 2>1"},
	}
	wantErrs := []error{ErrRangeLeftTooSmall, ErrRangeLeftTooSmall, ErrRangeRightTooLarge, ErrRangeInverted}
	for i, tc := range badRanges {
		recs, err := l.Compact(tc.left, tc.right)
		afterRecs := l.LastCompactRecords()
		ok := errors.Is(err, wantErrs[i]) && recs == nil && equalRecordViews(beforeRecs, afterRecs)
		logf(t, "输入=Compact(left=%d,right=%d) 返回值=(recs=%v,err=%v) 判定=%s（依据：%s；失败前后状态一致=%v）",
			tc.left, tc.right, recs, err, passFail(ok), tc.why, equalRecordViews(beforeRecs, afterRecs))
		if !ok {
			t.Fatalf("Compact(%d,%d) err=%v, want %v", tc.left, tc.right, err, wantErrs[i])
		}
	}

	if got := l.NextSeq(); got != 4 {
		t.Fatalf("NextSeq after failures=%d, want 4", got)
	}
	logf(t, "输入=NextSeq() 失败后返回值=%d 判定=PASS（依据：拒绝操作不改变日志）", l.NextSeq())
	checkEntry(t, "失败后映射不变", l, "a", 3, Entry{Seq: 2, Key: "a", Value: "2"}, true)
	if err := l.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReadsVsMutation 验证追加与压缩进行期间并发读取，
// 结果始终与某个串行历史的参照快照逐值相同；SelfCheck 也并发调用。
func TestConcurrentReadsVsMutation(t *testing.T) {
	l := New()
	m := newSerialModel()
	const prefix = 40
	for seq := 1; seq <= prefix; seq++ {
		key := []string{"a", "b", "c", "d"}[seq%4]
		value := fmt.Sprintf("%s@%d", key, seq)
		if _, err := l.Append(key, value); err != nil {
			t.Fatal(err)
		}
		m.append(key, value)
	}
	// 前缀先做一次压缩并对前缀拍参照快照；变更只发生在前缀之外。
	if _, err := l.Compact(1, prefix); err != nil {
		t.Fatal(err)
	}
	m.compact(1, prefix)
	snap := m.snapshot(prefix)
	readKeys := []string{"a", "b", "c", "d", "missing"}

	const tail = 20
	for seq := prefix + 1; seq <= prefix+tail; seq++ {
		key := []string{"t1", "t2"}[seq%2]
		if _, err := l.Append(key, fmt.Sprintf("%s@%d", key, seq)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var failMu sync.Mutex
	fail := func(format string, args ...any) {
		failMu.Lock()
		defer failMu.Unlock()
		t.Errorf(format, args...)
	}

	// 变更者 1：持续向尾部追加（位点 > prefix）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := l.Append("tail", fmt.Sprintf("tail%d", i)); err != nil {
				fail("concurrent append: %v", err)
				return
			}
		}
	}()
	// 变更者 2：反复只对前缀之外的固定尾区间压缩。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := l.Compact(prefix+1, prefix+tail); err != nil {
				fail("concurrent compact: %v", err)
				return
			}
		}
	}()
	// 读取者：只读前缀，与不可变串行参照快照逐值比对。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < 2000; n++ {
				key := readKeys[(id+n)%len(readKeys)]
				pos := 1 + (id*7+n*3)%prefix
				want, found := snap.read(key, pos)
				got, err := l.Read(key, pos)
				if found {
					if err != nil || got != want {
						fail("并发读不一致 Read(%q,%d)=%+v,%v want %+v", key, pos, got, err, want)
						return
					}
				} else if !errors.Is(err, ErrNotFound) {
					fail("并发读不一致 Read(%q,%d) err=%v want ErrNotFound", key, pos, err)
					return
				}
			}
		}(r)
	}
	// 自检者：与读并发，始终应通过。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 500; n++ {
			if err := l.SelfCheck(); err != nil {
				fail("concurrent SelfCheck: %v", err)
				return
			}
		}
	}()
	close(stop)
	wg.Wait()
	logf(t, "输入=并发 4 读 x2000 + 500 SelfCheck，期间持续 Append 与 Compact(prefix+1,prefix+%d)；判定=%s（依据：每次读都与只含区间内存活条目的串行参照快照逐值相同）",
		tail, passFail(true))

	// 收尾：稳定状态下前缀仍与快照一致，且整体自检通过。
	for pos := 1; pos <= prefix; pos++ {
		for _, key := range readKeys {
			want, found := snap.read(key, pos)
			got, err := l.Read(key, pos)
			if found {
				if err != nil || got != want {
					t.Fatalf("post prefix mismatch (%q,%d)=%+v,%v want %+v", key, pos, got, err, want)
				}
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatalf("post prefix (%q,%d) err=%v", key, pos, err)
			}
		}
	}
	if err := l.SelfCheck(); err != nil {
		t.Fatalf("final SelfCheck: %v", err)
	}
	logf(t, "收尾全位点核对与最终 SelfCheck 判定=PASS")
}
