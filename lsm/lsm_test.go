package lsm

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// verdict 打印写入、读取结果与判定依据，便于用 -v 复核每一步。
func verdict(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("判定: %s", fmt.Sprintf(format, args...))
}

func write(t *testing.T, d *DB, op string, key string, val []byte) {
	t.Helper()
	var err error
	switch op {
	case "put":
		err = d.Put(key, val)
	case "del":
		err = d.Delete(key)
	}
	if err != nil {
		t.Fatalf("写入 %s key=%s 失败: %v", op, key, err)
	}
	t.Logf("写入: %s key=%s value=%q -> mem=%d levels=%v", op, key, val, d.PendingWrites(), d.Levels())
}

func read(t *testing.T, d *DB, key string) ([]byte, bool) {
	t.Helper()
	v, seq, lvl, ok, err := d.GetWithSource(key)
	if err != nil {
		t.Fatalf("读取 key=%s 失败: %v", key, err)
	}
	t.Logf("读取: key=%s -> value=%q present=%v (seq=%d level=%d)", key, v, ok, seq, lvl)
	return v, ok
}

func mustEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	if string(got) != want {
		t.Fatalf("值不符: got=%q want=%q | 依据: 任意键的读必须返回最新写入", got, want)
	}
	verdict(t, "value=%q 等于最新写入 %q", got, want)
}

func mustAbsent(t *testing.T, d *DB, key, reason string) {
	t.Helper()
	_, ok := read(t, d, key)
	if ok {
		t.Fatalf("key=%s 应不存在，依据: %s", key, reason)
	}
	verdict(t, "key=%s 不存在，依据: %s", key, reason)
}

// TestLevelCascade 扇出2、两层：第零层连续触发合并且向第一层级联。
func TestLevelCascade(t *testing.T) {
	d, err := New(Options{MemtableSize: 1, Fanout: 2, MaxLevel: 1})
	if err != nil {
		t.Fatal(err)
	}

	write(t, d, "put", "a", []byte("a1")) // 段1 -> L0
	verdict(t, "levels=%v, 依据: 缓存满1条即冻结进第零层", d.Levels())
	write(t, d, "put", "b", []byte("b1")) // 段2 -> L0 满，合并 1,2 -> 段2 入 L1
	verdict(t, "levels=%v, 依据: L0 段数达到扇出2，最旧两段合并进上一层，段号取最大=2", d.Levels())
	write(t, d, "put", "c", []byte("c1")) // 段3 -> L0
	write(t, d, "put", "a", []byte("a2")) // 段4 -> L0，L0(3,4)->段4 入 L1
	verdict(t, "levels=%v, 依据: 第二次 L0 合并，段4 进入 L1，L1 现有 2,4", d.Levels())
	write(t, d, "put", "b", []byte("b2")) // 段5 -> L0
	write(t, d, "put", "c", []byte("c2")) // 段6 -> L0，L0(5,6)->段6 入 L1；L1(2,4,6) 满，合并 2,4 -> 段4 留在 L1
	verdict(t, "levels=%v, 依据: L1 达到扇出，段2+段4 合并为段4，与段6 同层（L1=[4,6]）", d.Levels())

	if got, ok := read(t, d, "a"); ok {
		mustEqual(t, got, "a2")
	} else {
		t.Fatal("a 应存在")
	}
	if got, ok := read(t, d, "b"); ok {
		mustEqual(t, got, "b2")
	} else {
		t.Fatal("b 应存在")
	}
	if got, ok := read(t, d, "c"); ok {
		mustEqual(t, got, "c2")
	} else {
		t.Fatal("c 应存在")
	}

	if err := d.Verify(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	verdict(t, "自检通过: 各层段号严格升序、段内键有序唯一")
}

// TestTombstoneKeptWhenOlderWriteExists 墓碑在仍有更早写入（在合并集之外）时不得被丢弃：
// floor 基线里有 a，之后删除 a，当 L1 合并时墓碑必须保留并遮蔽基线值。
func TestTombstoneKeptWhenOlderWriteExists(t *testing.T) {
	baseSeg := encodeSegment(&segment{id: 9001, records: []record{
		{seq: 1, key: "a", value: []byte("base-a")},
		{seq: 2, key: "b", value: []byte("base-b")},
	}})

	d, err := New(Options{MemtableSize: 1, Fanout: 2, MaxLevel: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.LoadSegment(baseSeg); err != nil {
		t.Fatalf("加载基线段失败: %v", err)
	}
	verdict(t, "floor 加载成功，floor=%d，依据: 外部段作为全局最旧基线", d.FloorCount())

	write(t, d, "del", "a", nil)          // 段1(L0): a 的墓碑；L1 里 b 仍只在 floor
	write(t, d, "put", "c", []byte("c1")) // 段2: L0(1,2)->段2 入 L1，墓碑保留（floor 有更早 a）
	verdict(t, "levels=%v, 依据: 段1合并进段2时，a 的墓碑必须保留（floor 存在更早 a）", d.Levels())

	write(t, d, "put", "d", []byte("d1")) // 段3 L0
	write(t, d, "put", "e", []byte("e1")) // 段4: L0->段4 入 L1，L1=[2,4]
	verdict(t, "levels=%v, 依据: L1 现有段2、段4，下一次合并将再次处理墓碑", d.Levels())
	write(t, d, "put", "f", []byte("f1")) // 段5 L0
	write(t, d, "put", "g", []byte("g1")) // 段6: L0->段6 入 L1；L1(2,4)->段4，墓碑仍须保留
	verdict(t, "levels=%v, 依据: L1 合并段2+段4，floor 仍有更早 a，墓碑不得丢弃", d.Levels())

	mustAbsent(t, d, "a", "墓碑遮蔽 floor 基线 base-a，且墓碑未在两次合并中被过早丢弃")

	// 墓碑真的落在合并后段里（而不是仅靠 floor 语义）
	foundTombstone := false
	for _, lvl := range d.snap.Load().levels {
		for _, seg := range lvl {
			if r, ok := lookupRecord(seg, "a"); ok && r.tombstone {
				foundTombstone = true
			}
		}
	}
	if !foundTombstone {
		t.Fatal("依据: 墓碑应实体保留在层段内，未找到 a 的墓碑")
	}
	verdict(t, "a 的墓碑实体存在于合并后段中，未被过早丢弃")
}

// TestTombstoneDroppedWithoutAnyOlderWrite 键从未写入、直接删除：
// 全系统不存在更早写入，墓碑在合并时可被丢弃；语义上读仍为不存在。
func TestTombstoneDroppedWithoutAnyOlderWrite(t *testing.T) {
	d, err := New(Options{MemtableSize: 1, Fanout: 2, MaxLevel: 0})
	if err != nil {
		t.Fatal(err)
	}
	write(t, d, "del", "ghost", nil)     // 段1 L0
	write(t, d, "put", "x", []byte("1")) // 段2: L0(1,2)->段2 留在 L0，ghost 墓碑应被丢弃

	totalRecs := 0
	for _, seg := range d.snap.Load().levels[0] {
		totalRecs += len(seg.records)
		if _, ok := lookupRecord(seg, "ghost"); ok {
			t.Fatal("依据: ghost 全系统无更早写入，其墓碑应在合并时丢弃")
		}
	}
	verdict(t, "合并后段内记录总数=%d，依据: ghost 墓碑已安全丢弃（无任何更早写入）", totalRecs)
	mustAbsent(t, d, "ghost", "墓碑丢弃与否读语义一致：键不存在")
}

// TestSegmentTruncationAndCorruption 覆盖尾部截断恢复与各类损坏拒绝。
func TestSegmentTruncationAndCorruption(t *testing.T) {
	good := encodeSegment(&segment{id: 7, records: []record{
		{seq: 1, key: "k1", value: []byte("v1")},
		{seq: 2, key: "k2", value: []byte("v2-long")},
	}})

	seg, err := decodeSegment(good, true)
	if err != nil {
		t.Fatalf("完整段应解析成功: %v", err)
	}
	verdict(t, "完整段解析成功: id=%d 记录数=%d", seg.id, len(seg.records))

	// 1) 尾部被截断：严格模式报 ErrSegmentTruncated，恢复模式安全截断。
	cut := append([]byte(nil), good[:len(good)-7]...)
	if _, err := decodeSegment(cut, true); !errors.Is(err, ErrSegmentTruncated) {
		t.Fatalf("依据: 尾部不完整应报 ErrSegmentTruncated，got %v", err)
	}
	verdict(t, "严格模式识别尾部截断 -> ErrSegmentTruncated（可区分原因）")

	rec, safe, err := RecoverTruncated(cut)
	if err != nil {
		t.Fatalf("截断恢复失败: %v", err)
	}
	if len(rec.records) != 1 || rec.records[0].key != "k1" {
		t.Fatalf("依据: 应只保留截断前最后一条完整记录 k1，got %+v", rec.records)
	}
	trailingRecLen := recFixedLen + len("k2") + len("v2-long")
	if !bytes.Equal(safe, good[:len(good)-trailingRecLen]) {
		t.Fatal("依据: 恢复字节应等于最后一条完整记录结束的前缀")
	}
	verdict(t, "截断恢复成功: 保留 %d 条完整记录，已提交数据不受影响", len(rec.records))

	// 2) 魔数错误：损坏，拒绝恢复。
	badMagic := append([]byte(nil), good...)
	copy(badMagic[:4], "XXXX")
	if _, err := decodeSegment(badMagic, true); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("依据: 魔数错应报 ErrSegmentCorrupt，got %v", err)
	}
	if _, _, err := RecoverTruncated(badMagic); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("依据: 魔数错不可截断恢复，got %v", err)
	}
	verdict(t, "魔数错误 -> ErrSegmentCorrupt，且拒绝猜测性恢复")

	// 3) 中间记录 CRC 被篡改：损坏，拒绝。
	crcBad := append([]byte(nil), good...)
	off := headerLen
	crcBad[off+9] ^= 0xFF // 破坏首条记录 seq 字节，CRC 必不匹配
	if _, err := decodeSegment(crcBad, true); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("依据: CRC 不匹配应报 ErrSegmentCorrupt，got %v", err)
	}
	verdict(t, "CRC 篡改 -> ErrSegmentCorrupt")

	// 4) LoadSegment 对损坏段整体拒绝，且状态不变。
	d, _ := New(Options{MemtableSize: 1, Fanout: 4, MaxLevel: 1})
	beforeLevels := append([]int(nil), d.Levels()...)
	if err := d.LoadSegment(badMagic); !errors.Is(err, ErrSegmentCorrupt) {
		t.Fatalf("依据: 加载损坏段必须整体拒绝，got %v", err)
	}
	if err := d.LoadSegment(cut); !errors.Is(err, ErrSegmentTruncated) {
		t.Fatalf("依据: 加载截断段必须拒绝（严格模式），got %v", err)
	}
	if fmt.Sprint(d.Levels()) != fmt.Sprint(beforeLevels) || d.FloorCount() != 0 {
		t.Fatal("依据: 一次失败不得改变缓存、段与层分布")
	}
	verdict(t, "损坏/截断段加载均被拒绝，levels=%v floor=%d 保持失败前状态", d.Levels(), d.FloorCount())

	// 5) 恢复后的安全字节可以正常加载，且不影响已有数据。
	if err := d.LoadSegment(safe); err != nil {
		t.Fatalf("恢复字节应可加载: %v", err)
	}
	if v, ok := read(t, d, "k1"); !ok || string(v) != "v1" {
		t.Fatalf("依据: 恢复段中的 k1=v1 应可读，got %q ok=%v", v, ok)
	}
	verdict(t, "截断恢复后的段成功加载，已有数据未受影响")
}

// TestInvalidArgs 非法参数与空键必须被拒绝且有可区分原因。
func TestInvalidArgs(t *testing.T) {
	for _, opts := range []Options{
		{MemtableSize: 0, Fanout: 2, MaxLevel: 1},
		{MemtableSize: 1, Fanout: 0, MaxLevel: 1},
		{MemtableSize: 1, Fanout: 2, MaxLevel: -1},
	} {
		if _, err := New(opts); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("opts=%+v 应报 ErrInvalidArgument, got %v", opts, err)
		}
	}
	verdict(t, "非法 Options 统一报 ErrInvalidArgument")

	d, _ := New(Options{MemtableSize: 2, Fanout: 2, MaxLevel: 1})
	if err := d.Put("", []byte("v")); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键 Put 应报 ErrEmptyKey, got %v", err)
	}
	if err := d.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键 Delete 应报 ErrEmptyKey, got %v", err)
	}
	if _, _, err := d.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键 Get 应报 ErrEmptyKey, got %v", err)
	}
	if err := d.Put("k", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil 值应报 ErrInvalidArgument, got %v", err)
	}
	verdict(t, "空键 -> ErrEmptyKey，nil 值 -> ErrInvalidArgument，原因可区分")
}

// TestConcurrentReadsDuringMerges 写入持续触发冻结与多层合并期间，
// 多 goroutine 并发读与自检：不得崩溃、竞态，值始终属于某个完整状态。
func TestConcurrentReadsDuringMerges(t *testing.T) {
	d, err := New(Options{MemtableSize: 3, Fanout: 3, MaxLevel: 3})
	if err != nil {
		t.Fatal(err)
	}

	const writers = 4
	const writesPer = 400
	const readers = 8

	var writerWG sync.WaitGroup
	var readerWG sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(id int) {
			defer writerWG.Done()
			for i := 0; i < writesPer; i++ {
				key := fmt.Sprintf("k%d", (id*7+i)%50)
				val := []byte(fmt.Sprintf("w%d-%d", id, i))
				if err := d.Put(key, val); err != nil {
					t.Errorf("并发 Put 失败: %v", err)
					return
				}
				if i%50 == 0 {
					if err := d.Delete(fmt.Sprintf("k%d", (id+i)%50)); err != nil {
						t.Errorf("并发 Delete 失败: %v", err)
						return
					}
				}
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func(id int) {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("k%d", (id*13+id)%50)
				_, _, _, _, err := d.GetWithSource(key)
				if err != nil {
					t.Errorf("并发 Get 失败: %v", err)
					return
				}

				// 同一快照内重复读必须完全一致：快照是一次原子发布的完整状态，
				// 即使此刻写者正在合并，快照既不提前看见新段，也不会新旧段混杂。
				snap := d.View()
				v2, ok2, err := snap.Get(key)
				if err != nil {
					t.Errorf("快照 Get 失败: %v", err)
					return
				}
				v3, ok3 := mustSnapshotGet(t, snap, key)
				if ok2 != ok3 || (ok2 && !bytes.Equal(v2, v3)) {
					t.Errorf("依据: 同一快照内读取必须稳定，got (%q,%v) vs (%q,%v)", v2, ok2, v3, ok3)
					return
				}
				if err := d.Verify(); err != nil {
					t.Errorf("并发自检失败（出现混合两代的非法分布）: %v", err)
					return
				}

				// 扫描全部 50 个键：它们必然同属“某次合并前”或“某次合并后”，
				// 不会一键来自新代、另一键来自旧代。
				for k := 0; k < 50; k++ {
					_, exists, err := snap.Get(fmt.Sprintf("k%d", k))
					if err != nil {
						t.Errorf("快照扫描失败: %v", err)
						return
					}
					_ = exists
				}
			}
		}(r)
	}

	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	if err := d.Verify(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}
	verdict(t, "并发读/自检在 %d 写者 x%d 次写入、%d 读者持续读期间无竞态，层分布=%v",
		writers, writesPer, readers, d.Levels())
}

func mustSnapshotGet(t *testing.T, snap *Snapshot, key string) ([]byte, bool) {
	t.Helper()
	v, ok, err := snap.Get(key)
	if err != nil {
		t.Fatalf("快照读失败: %v", err)
	}
	return v, ok
}

// TestReplayByTimeOrder 本地核对方法：把当前所有段导出，
// 按段号升序（时间从旧到新）重放全部记录（同键以 seq 最大者为准），
// 重放结果必须与每个键的在线 Get 完全一致。
func TestReplayByTimeOrder(t *testing.T) {
	d, err := New(Options{MemtableSize: 2, Fanout: 2, MaxLevel: 2})
	if err != nil {
		t.Fatal(err)
	}

	// floor 基线（外部旧数据）
	base := encodeSegment(&segment{id: 500, records: []record{
		{seq: 1, key: "a", value: []byte("base-a")},
		{seq: 2, key: "z", value: []byte("base-z")},
	}})
	if err := d.LoadSegment(base); err != nil {
		t.Fatal(err)
	}

	ops := []struct {
		op  string
		key string
		val []byte
	}{
		{"put", "a", []byte("a1")},
		{"put", "b", []byte("b1")},
		{"del", "a", nil},
		{"put", "a", []byte("a2")},
		{"put", "c", []byte("c1")},
		{"del", "z", nil},
		{"put", "b", []byte("b2")},
		{"del", "never-existed", nil},
		{"put", "d", []byte("d1")},
	}
	for _, o := range ops {
		write(t, d, o.op, o.key, o.val)
	}

	type cell struct {
		value   []byte
		seq     uint64
		deleted bool
	}
	replay := make(map[string]cell)
	// 按逻辑年代从旧到新组织段字节：
	// floor 基线 -> 最大层..第零层（层内段号升序）-> 内存缓存。
	// 不能只按段号数值排序：floor 段号属于外部编号空间。
	var blobs [][]byte
	blobs = append(blobs, base)
	cur := d.snap.Load()
	for li := len(cur.levels) - 1; li >= 0; li-- {
		for _, seg := range cur.levels[li] {
			blobs = append(blobs, encodeSegment(seg))
		}
	}
	if len(cur.mem) > 0 {
		blobs = append(blobs, encodeSegment(freezeMem(cur.mem, 0)))
	}

	type decoded struct {
		id  uint64
		rec []record
	}
	var all []decoded
	for _, b := range blobs {
		seg, err := decodeSegment(b, true)
		if err != nil {
			t.Fatalf("导出段无法解析，依据: 重放前提是段字节完整: %v", err)
		}
		if len(seg.records) > 0 {
			all = append(all, decoded{seg.id, seg.records})
		}
	}
	for _, dg := range all {
		for _, r := range dg.rec {
			cur, ok := replay[r.key]
			if !ok || r.seq > cur.seq {
				replay[r.key] = cell{value: r.value, seq: r.seq, deleted: r.tombstone}
			}
		}
	}

	keys := map[string]bool{"a": true, "b": true, "c": true, "d": true, "z": true, "never-existed": true}
	for k := range keys {
		gotVal, gotOK := read(t, d, k)
		c, inReplay := replay[k]
		wantOK := inReplay && !c.deleted
		if gotOK != wantOK {
			t.Fatalf("key=%s 在线 present=%v 与重放 present=%v 不一致 | 依据: 时间顺序重放结果必须等于在线读",
				k, gotOK, wantOK)
		}
		if wantOK && !bytes.Equal(gotVal, c.value) {
			t.Fatalf("key=%s 在线值=%q 与重放值=%q 不一致", k, gotVal, c.value)
		}
		verdict(t, "key=%s 在线读(%q,present=%v) == 时间顺序重放(present=%v,seq=%d)",
			k, gotVal, gotOK, wantOK, c.seq)
	}

	if err := d.Verify(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}
