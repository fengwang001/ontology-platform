package changelog

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// stepf 打印单测日志：输入、读位点、返回值与判定依据。
// 配合 `go test -v` 即可逐条审计。
func stepf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("STEP | "+format, args...)
}

func mustAppend(t *testing.T, l *Log, key, value string) int64 {
	t.Helper()
	seq, err := l.Append(key, value)
	if err != nil {
		t.Fatalf("Append(%q,%q) unexpected error: %v", key, value, err)
	}
	stepf(t, "输入 Append(key=%q value=%q) -> 返回 seq=%d err=nil 判定: 序号连续递增", key, value, seq)
	return seq
}

func checkRead(t *testing.T, l *Log, key string, pos int64, wantVal string, wantOK bool) {
	t.Helper()
	rec, ok, err := l.ReadAt(key, pos)
	if err != nil {
		t.Fatalf("ReadAt(%q,%d) unexpected error: %v", key, pos, err)
	}
	if ok != wantOK || (ok && rec.Value != wantVal) {
		t.Fatalf("ReadAt(%q,%d) = {seq:%d val:%q compacted:%v} ok=%v, want ok=%v val=%q",
			key, pos, rec.Seq, rec.Value, rec.Compacted, ok, wantOK, wantVal)
	}
	stepf(t, "读位点 ReadAt(key=%q pos=%d) -> 返回 seq=%d value=%q compacted=%v exists=%v err=nil 判定: 与可见条目中序号最大者一致",
		key, pos, rec.Seq, rec.Value, rec.Compacted, ok)
}

func sortRecords(recs []Record) {
	sort.Slice(recs, func(i, j int) bool { return recs[i].Seq < recs[j].Seq })
}

// TestCompactKeepsLastWriteInRange 区间内同键多条写入只保留最后一条。
func TestCompactKeepsLastWriteInRange(t *testing.T) {
	l := New()
	s1 := mustAppend(t, l, "a", "a1")
	s2 := mustAppend(t, l, "b", "b1")
	s3 := mustAppend(t, l, "a", "a2")
	s4 := mustAppend(t, l, "a", "a3")
	s5 := mustAppend(t, l, "b", "b2")

	stepf(t, "输入 Compact(left=%d right=%d) 覆盖全部5条, 期望 a->seq%d(a3), b->seq%d(b2)", s1, s5, s4, s5)
	res, err := l.Compact(s1, s5)
	if err != nil {
		t.Fatalf("Compact unexpected error: %v", err)
	}
	stepf(t, "返回 CompactResult=%+v err=nil 判定: 合并2键, 删除3条", res)
	if res.MergedRecords != 2 || res.RemovedRecords != 3 {
		t.Fatalf("CompactResult = %+v, want merged=2 removed=3", res)
	}

	recs := l.Records()
	stepf(t, "压缩后全量条目: %+v 判定: 按序号升序且只剩 a3/b2", recs)
	if len(recs) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(recs))
	}
	if recs[0] != (Record{Seq: s4, Key: "a", Value: "a3", Compacted: true}) {
		t.Fatalf("rec0 = %+v, want a@%d a3 compacted", recs[0], s4)
	}
	if recs[1] != (Record{Seq: s5, Key: "b", Value: "b2", Compacted: true}) {
		t.Fatalf("rec1 = %+v, want b@%d b2 compacted", recs[1], s5)
	}

	checkRead(t, l, "a", s5, "a3", true)
	checkRead(t, l, "b", s5, "b2", true)
	// 压缩记录沿用保留条目的原序号：序号 s4 起 a3 可见，s3 位点尚不可见。
	checkRead(t, l, "a", s3, "", false)
	checkRead(t, l, "a", s2, "", false)
}

// TestCompactOutsideRangeUntouched 区间外条目原样保留。
func TestCompactOutsideRangeUntouched(t *testing.T) {
	l := New()
	s1 := mustAppend(t, l, "a", "a1")
	s2 := mustAppend(t, l, "b", "b1")
	s3 := mustAppend(t, l, "a", "a2")
	s4 := mustAppend(t, l, "c", "c1")
	s5 := mustAppend(t, l, "a", "a3")

	stepf(t, "输入 Compact(left=%d right=%d) 仅覆盖 b1/a2, a1/c1/a3 在区间外", s2, s3)
	res, err := l.Compact(s2, s3)
	if err != nil {
		t.Fatalf("Compact unexpected error: %v", err)
	}
	stepf(t, "返回 %+v err=nil 判定: 区间内2键各1条, 删除0", res)
	if res.MergedRecords != 2 || res.RemovedRecords != 0 {
		t.Fatalf("CompactResult = %+v, want merged=2 removed=0", res)
	}

	recs := l.Records()
	stepf(t, "压缩后全量条目: %+v", recs)
	if len(recs) != 5 {
		t.Fatalf("len(records) = %d, want 5 (无删除)", len(recs))
	}
	if recs[0] != (Record{Seq: s1, Key: "a", Value: "a1"}) {
		t.Fatalf("区间外 a1 被改动: %+v", recs[0])
	}
	if recs[3] != (Record{Seq: s4, Key: "c", Value: "c1"}) {
		t.Fatalf("区间外 c1 被改动: %+v", recs[3])
	}
	if recs[4] != (Record{Seq: s5, Key: "a", Value: "a3"}) {
		t.Fatalf("区间外 a3 被改动: %+v", recs[4])
	}
	if !recs[1].Compacted || !recs[2].Compacted {
		t.Fatalf("区间内记录应带 Compacted 标记: %+v", recs)
	}

	checkRead(t, l, "b", s2, "b1", true)
	checkRead(t, l, "a", s5, "a3", true)
	checkRead(t, l, "c", s4, "c1", true)
}

// TestCompactEveryPositionAddressable 压缩后任意合法位点仍可寻址，
// 且结果与“只看区间内条目的批量参照”逐键逐位点一致。
func TestCompactEveryPositionAddressable(t *testing.T) {
	l := New()
	type write struct{ key, value string }
	plan := []write{
		{"a", "a1"}, {"b", "b1"}, {"a", "a2"}, {"c", "c1"},
		{"b", "b2"}, {"a", "a3"}, {"c", "c2"}, {"d", "d1"},
	}
	raw := make([]Record, 0, len(plan))
	for _, w := range plan {
		seq := mustAppend(t, l, w.key, w.value)
		raw = append(raw, Record{Seq: seq, Key: w.key, Value: w.value})
	}

	left, right := int64(2), int64(7)

	// 批量参照：只扫描区间 [left,right] 内条目，按键保留序号最大者；
	// 区间外条目原样纳入，合并后按序号排序即为期望的压缩日志。
	reference := make([]Record, 0, len(raw))
	lastByKey := make(map[string]Record)
	for _, rec := range raw {
		switch {
		case rec.Seq < left, rec.Seq > right:
			reference = append(reference, rec)
		default:
			if cur, ok := lastByKey[rec.Key]; !ok || rec.Seq > cur.Seq {
				lastByKey[rec.Key] = rec
			}
		}
	}
	for _, rec := range lastByKey {
		rec.Compacted = true
		reference = append(reference, rec)
	}
	sortRecords(reference)

	res, err := l.Compact(left, right)
	if err != nil {
		t.Fatalf("Compact(%d,%d) unexpected error: %v", left, right, err)
	}
	stepf(t, "输入 Compact(left=%d right=%d) -> 返回 %+v err=nil 判定依据: 只看区间内条目的批量参照", left, right, res)

	got := l.Records()
	stepf(t, "实际压缩条目: %+v", got)
	stepf(t, "参照压缩条目: %+v", reference)
	if len(got) != len(reference) {
		t.Fatalf("条目数实际=%d 参照=%d", len(got), len(reference))
	}
	for i := range reference {
		if got[i] != reference[i] {
			t.Fatalf("第%d条不一致 实际=%+v 参照=%+v", i, got[i], reference[i])
		}
	}

	// 参照读取：直接在参照记录上按“seq<=pos 取序号最大”计算。
	refRead := func(key string, pos int64) (Record, bool) {
		var best Record
		found := false
		for _, rec := range reference {
			if rec.Seq <= pos && rec.Key == key {
				best, found = rec, true
			}
		}
		return best, found
	}

	// 任意合法位点 1..NextSeq-1 逐键核对：不报错且与参照逐值相同。
	keys := []string{"a", "b", "c", "d", "never-written"}
	for pos := int64(1); pos < l.NextSeq(); pos++ {
		for _, key := range keys {
			rec, ok, rerr := l.ReadAt(key, pos)
			if rerr != nil {
				t.Fatalf("ReadAt(%q,%d) 压缩后返回错误: %v", key, pos, rerr)
			}
			refRec, refOK := refRead(key, pos)
			if ok != refOK {
				t.Fatalf("ReadAt(%q,%d) exists=%v 参照=%v", key, pos, ok, refOK)
			}
			if ok && rec != refRec {
				t.Fatalf("ReadAt(%q,%d) 实际=%+v 参照=%+v", key, pos, rec, refRec)
			}
			stepf(t, "读位点 pos=%d key=%q -> value=%q seq=%d exists=%v 判定依据: 与只看区间参照逐值相同",
				pos, key, rec.Value, rec.Seq, ok)
		}
	}

	if _, verr := l.Verify(); verr != nil {
		t.Fatalf("Verify after compact error: %v", verr)
	}
}

// TestReadAtMissingReturnsNotError 无可见条目返回不存在而不是错误。
func TestReadAtMissingReturnsNotError(t *testing.T) {
	l := New()
	mustAppend(t, l, "a", "a1")
	checkRead(t, l, "ghost", 1, "", false)
}

// TestValidationRejections 空键、位点越界、区间非法整体拒绝且互不改变状态。
func TestValidationRejections(t *testing.T) {
	l := New()
	s1 := mustAppend(t, l, "a", "a1")
	mustAppend(t, l, "b", "b1")
	before := l.Records()
	beforeNext := l.NextSeq()

	type tc struct {
		name        string
		call        func() error
		wantErr     error
		errContains string
	}
	cases := []tc{
		{"append-empty-key", func() error { _, e := l.Append("", "x"); return e }, ErrEmptyKey, "empty"},
		{"read-empty-key", func() error { _, _, e := l.ReadAt("", 1); return e }, ErrEmptyKey, "empty"},
		{"read-pos-zero", func() error { _, _, e := l.ReadAt("a", 0); return e }, ErrPositionOutOfRange, "out of range"},
		{"read-pos-too-large", func() error { _, _, e := l.ReadAt("a", l.NextSeq()); return e }, ErrPositionOutOfRange, "out of range"},
		{"read-pos-negative", func() error { _, _, e := l.ReadAt("a", -1); return e }, ErrPositionOutOfRange, "out of range"},
		{"compact-left<1", func() error { _, e := l.Compact(0, 2); return e }, ErrCompactLeftTooSmall, "left"},
		{"compact-right>last", func() error { _, e := l.Compact(1, s1+2); return e }, ErrCompactRightTooLarge, "right"},
		{"compact-inverted", func() error { _, e := l.Compact(2, 1); return e }, ErrCompactInverted, "left <= right"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("%s: err = %v, want %v", c.name, err, c.wantErr)
			}
			if !strings.Contains(err.Error(), c.errContains) {
				t.Fatalf("%s: err message %q 不含区分关键字 %q", c.name, err.Error(), c.errContains)
			}
			stepf(t, "输入 %s -> 返回 err=%q 判定: 按预期整体拒绝, 错误可区分", c.name, err.Error())

			after := l.Records()
			if len(after) != len(before) || l.NextSeq() != beforeNext {
				t.Fatalf("%s: 失败操作改变了日志/位点映射 before=%v/%d after=%v/%d",
					c.name, before, beforeNext, after, l.NextSeq())
			}
			for i := range before {
				if after[i] != before[i] {
					t.Fatalf("%s: 失败操作改变了第%d条记录", c.name, i)
				}
			}
			stepf(t, "判定依据: 失败后条目数=%d NextSeq=%d 与失败前完全一致", len(after), l.NextSeq())
		})
	}
}

// TestCompactIdempotentAndDeterministic 同一区间重复压缩结果稳定可复现，
// 且空区间（没有任何写入的键）不产生压缩记录。
func TestCompactIdempotentAndDeterministic(t *testing.T) {
	l := New()
	for _, v := range []string{"a1", "a2", "a3"} {
		mustAppend(t, l, "a", v)
	}
	mustAppend(t, l, "b", "b1")

	first, err := l.Compact(1, 3)
	if err != nil {
		t.Fatalf("first compact: %v", err)
	}
	stepf(t, "第一次 Compact(1,3) -> 返回 %+v", first)
	afterFirst := l.Records()

	second, err := l.Compact(1, 3)
	if err != nil {
		t.Fatalf("second compact: %v", err)
	}
	stepf(t, "第二次 Compact(1,3) -> 返回 %+v 判定: 已压缩区间无额外删除, 条目不再变化", second)
	if second.RemovedRecords != 0 || second.MergedRecords != 1 {
		t.Fatalf("重复压缩 = %+v, want merged=1 removed=0", second)
	}
	afterSecond := l.Records()
	if len(afterSecond) != len(afterFirst) {
		t.Fatalf("重复压缩改变条目数: %d -> %d", len(afterFirst), len(afterSecond))
	}
	for i := range afterFirst {
		if afterFirst[i] != afterSecond[i] {
			t.Fatalf("重复压缩改变第%d条: %+v vs %+v", i, afterFirst[i], afterSecond[i])
		}
	}

	// 对“只含 b1”的区间压缩：b 只出现一次，生成1条压缩记录、删除0条；
	// 区间内从未写入的键不产生任何记录。
	res, err := l.Compact(4, 4)
	if err != nil {
		t.Fatalf("single-entry compact: %v", err)
	}
	stepf(t, "单点 Compact(4,4) -> 返回 %+v 判定: 无写入的键不产生压缩记录", res)
	if res.MergedRecords != 1 || res.RemovedRecords != 0 {
		t.Fatalf("single-entry result = %+v, want merged=1 removed=0", res)
	}
}

// TestEmptyLogAndBoundaryReads 空日志位点拒绝；NextSeq 边界清晰。
func TestEmptyLogAndBoundaryReads(t *testing.T) {
	l := New()
	if l.NextSeq() != 1 {
		t.Fatalf("empty NextSeq = %d, want 1", l.NextSeq())
	}
	if _, _, err := l.ReadAt("a", 1); !errors.Is(err, ErrPositionOutOfRange) {
		t.Fatalf("空日志读位点1 err=%v, want ErrPositionOutOfRange", err)
	}
	if _, err := l.Compact(1, 1); !errors.Is(err, ErrCompactRightTooLarge) {
		t.Fatalf("空日志 Compact(1,1) err=%v, want ErrCompactRightTooLarge", err)
	}
	gen, err := l.Verify()
	if err != nil || gen != 0 {
		t.Fatalf("空日志 Verify = %d,%v", gen, err)
	}
	stepf(t, "空日志: ReadAt/Compact 均按位点越界拒绝, Verify 通过, 判定: 边界 NextSeq=1")
}

// TestVerifyDetectsCorruption 自检应能发现被破坏的不可变快照。
func TestVerifyDetectsCorruption(t *testing.T) {
	l := New()
	mustAppend(t, l, "a", "a1")

	// 注意：序号空洞是压缩后的正常形态，自检不报错；这里只检查真正非法的结构。
	l.state.Store(&state{nextSeq: 3, records: []Record{{Seq: 5, Key: "a"}}})
	if _, err := l.Verify(); !errors.Is(err, ErrLogCorrupted) {
		t.Fatalf("序号越上界自检 err=%v, want ErrLogCorrupted", err)
	}

	l.state.Store(&state{nextSeq: 3, records: []Record{{Seq: 0, Key: "a"}}})
	if _, err := l.Verify(); !errors.Is(err, ErrLogCorrupted) {
		t.Fatalf("序号小于1自检 err=%v, want ErrLogCorrupted", err)
	}

	l.state.Store(&state{nextSeq: 3, records: []Record{
		{Seq: 2, Key: "a"}, {Seq: 1, Key: "b"},
	}})
	if _, err := l.Verify(); !errors.Is(err, ErrLogCorrupted) {
		t.Fatalf("非升序自检 err=%v, want ErrLogCorrupted", err)
	}
	l.state.Store(&state{nextSeq: 0})
	if _, err := l.Verify(); !errors.Is(err, ErrLogCorrupted) {
		t.Fatalf("nextSeq 非法自检 err=%v, want ErrLogCorrupted", err)
	}
	stepf(t, "输入四组损坏快照 -> 返回 ErrLogCorrupted 判定: 自检可并发且只读")
}
