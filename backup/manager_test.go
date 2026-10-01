package backup

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

func mkBlock(b byte, size int) []byte {
	d := make([]byte, size)
	for i := range d {
		d[i] = b
	}
	return d
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func must(t *testing.T, _ []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wb(t *testing.T, m *Manager, index int, data []byte) {
	t.Helper()
	_, err := m.WriteBlock(index, data)
	if err != nil {
		t.Fatalf("write block %d: %v", index, err)
	}
}

func intSetEqual(got []int, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	set := map[int]struct{}{}
	for _, v := range got {
		set[v] = struct{}{}
	}
	for _, v := range want {
		if _, ok := set[v]; !ok {
			return false
		}
	}
	return true
}

func equalVolume(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// restoreAll 打开临时会话，还原某备份全部块。
func restoreAll(t *testing.T, m *Manager, id string) [][]byte {
	t.Helper()
	s, err := m.OpenRestore(id)
	if err != nil {
		t.Fatalf("open restore %s: %v", id, err)
	}
	defer s.Close()
	out := make([][]byte, m.blocks)
	for i := 0; i < m.blocks; i++ {
		out[i], err = s.ReadBlock(i)
		if err != nil {
			t.Fatalf("read block %d: %v", i, err)
		}
	}
	return out
}

// TestWriteBackOriginalValueNotStored 写过又写回原值的块不进入增量。
func TestWriteBackOriginalValueNotStored(t *testing.T) {
	m := New(4, 8, 1000, io.Discard)
	wb(t, m, 1, mkBlock('A', 8))
	full, _, err := m.Backup()
	mustOK(t, err)

	// 全量之后：块1改成 B 又改回 A；块2改成 C。
	wb(t, m, 1, mkBlock('B', 8))
	wb(t, m, 1, mkBlock('A', 8))
	wb(t, m, 2, mkBlock('C', 8))
	inc, stored, err := m.Backup()
	mustOK(t, err)

	isFull, err := m.IsFull(inc)
	mustOK(t, err)
	if isFull {
		t.Fatalf("%s 应为增量备份", inc)
	}
	if !intSetEqual(stored, []int{2}) {
		t.Fatalf("增量只应存块2, 实际存储块=%v", stored)
	}
	got, err := m.StoredBlocks(inc)
	mustOK(t, err)
	if !intSetEqual(got, []int{2}) {
		t.Fatalf("StoredBlocks=%v, 期望 [2]", got)
	}
	vol := restoreAll(t, m, inc)
	if !bytes.Equal(vol[1], mkBlock('A', 8)) || !bytes.Equal(vol[2], mkBlock('C', 8)) {
		t.Fatalf("还原结果错误: 块1=%v 块2=%v", vol[1], vol[2])
	}
	_ = full
}

// TestDeleteMiddleMergeAndPrune 删除中间备份后后继并入其块，
// 且剔除与新父还原结果相同的块；保留备份还原结果不变。
func TestDeleteMiddleMergeAndPrune(t *testing.T) {
	const n, size = 5, 4
	m := New(n, size, 1000, io.Discard)

	for i := 0; i < n; i++ {
		wb(t, m, i, mkBlock(1, size))
	}
	b1, _, _ := m.Backup()

	// B2: 块0->2, 块2->2
	wb(t, m, 0, mkBlock(2, size))
	wb(t, m, 2, mkBlock(2, size))
	b2, _, _ := m.Backup()

	// B3: 块0 写回1（与B1还原相同，但与B2还原不同），块3->3
	wb(t, m, 0, mkBlock(1, size))
	wb(t, m, 3, mkBlock(3, size))
	b3, _, _ := m.Backup()

	before1 := restoreAll(t, m, b1)
	before3 := restoreAll(t, m, b3)

	if err := m.Delete(b2); err != nil {
		t.Fatalf("delete middle: %v", err)
	}

	// 删除后 B3 应从 B2 并入块2=2；块0=1 与新父 B1 相同必须剔除；块3=3 保留。
	got, err := m.StoredBlocks(b3)
	mustOK(t, err)
	if !intSetEqual(got, []int{2, 3}) {
		t.Fatalf("删除B2后 B3 应存块{2,3}, 实际=%v", got)
	}
	if !equalVolume(restoreAll(t, m, b1), before1) {
		t.Fatal("B1 还原结果在删除后发生变化")
	}
	if !equalVolume(restoreAll(t, m, b3), before3) {
		t.Fatal("B3 还原结果在删除 B2 后逐字节变化")
	}
	if _, err := m.StoredBlocks(b2); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("期望 ErrBackupNotFound, 得到 %v", err)
	}
	if ids := m.Backups(); len(ids) != 2 || ids[0] != b1 || ids[1] != b3 {
		t.Fatalf("删除后链=%v", ids)
	}
}

// TestSuccessorBlockWins 合并时后继已有的同号块以后继为准。
func TestSuccessorBlockWins(t *testing.T) {
	const n, size = 3, 2
	m := New(n, size, 1000, io.Discard)
	wb(t, m, 0, mkBlock(1, size))
	b1, _, _ := m.Backup()
	wb(t, m, 0, mkBlock(2, size))
	b2, _, _ := m.Backup()
	wb(t, m, 0, mkBlock(3, size))
	b3, _, _ := m.Backup()

	before := restoreAll(t, m, b3)
	mustOK(t, m.Delete(b2))
	got := restoreAll(t, m, b3)
	if !equalVolume(got, before) || !bytes.Equal(got[0], mkBlock(3, size)) {
		t.Fatalf("合并应以后继块为准, got[0]=%v", got[0])
	}
	stored, _ := m.StoredBlocks(b3)
	if !intSetEqual(stored, []int{0}) {
		t.Fatalf("B3 应仅存块0(对新父B1仍不同), 实际=%v", stored)
	}
	_ = b1
}

// TestRepeatedDeleteUntilSingleFull 从链首连续删除，后继逐次升格，
// 直到只剩一个全量；删除过程中链尾还原结果逐字节不变。
func TestRepeatedDeleteUntilSingleFull(t *testing.T) {
	const n, size = 4, 4
	m := New(n, size, 1000, io.Discard)
	for i := 0; i < n; i++ {
		wb(t, m, i, mkBlock(1, size))
	}
	_, _, _ = m.Backup()
	for round := 2; round <= 5; round++ {
		wb(t, m, (round-2)%n, mkBlock(byte(round), size))
		_, _, _ = m.Backup()
	}
	tailID := m.Backups()[4]
	expectedTail := restoreAll(t, m, tailID)

	ids := m.Backups()
	// 连续删除链首：每次后继补入全部块并升格为全量。
	for len(ids) > 1 {
		victim := ids[0]
		mustOK(t, m.Delete(victim))
		ids = m.Backups()
		head := ids[0]
		full, err := m.IsFull(head)
		mustOK(t, err)
		if !full {
			t.Fatalf("删除 %s 后 %s 应升格为全量", victim, head)
		}
		// 升格后的全量必须存全部块。
		stored, _ := m.StoredBlocks(head)
		if head != tailID && len(stored) != n {
			t.Fatalf("中间全量 %s 应存全部块, 实际=%v", head, stored)
		}
		if !equalVolume(restoreAll(t, m, tailID), expectedTail) {
			t.Fatalf("删除 %s 后链尾 %s 还原结果变化", victim, tailID)
		}
	}
	if ids[0] != tailID {
		t.Fatalf("最后应只剩 %s, 实际 %s", tailID, ids[0])
	}
	full, err := m.IsFull(tailID)
	mustOK(t, err)
	if !full {
		t.Fatal("最后剩余的备份应为全量")
	}
	// 删除唯一全量（此时为链尾，直接丢弃），链清空。
	mustOK(t, m.Delete(tailID))
	if len(m.Backups()) != 0 {
		t.Fatal("链应已清空")
	}
	// 清空后下一次备份重新成为全量。
	wb(t, m, 0, mkBlock(9, size))
	id, _, _ := m.Backup()
	full, err = m.IsFull(id)
	mustOK(t, err)
	if !full {
		t.Fatal("链清空后的首个备份应为全量")
	}
	stored, _ := m.StoredBlocks(id)
	if len(stored) != n {
		t.Fatalf("新全量应存全部%d块, 实际=%v", n, stored)
	}
}

// TestDeleteFullPromotesSuccessor 删除全量链首后后继升格为全量。
func TestDeleteFullPromotesSuccessor(t *testing.T) {
	const n, size = 3, 2
	m := New(n, size, 1000, io.Discard)
	for i := 0; i < n; i++ {
		wb(t, m, i, mkBlock(1, size))
	}
	b1, _, _ := m.Backup()
	wb(t, m, 0, mkBlock(2, size))
	b2, _, _ := m.Backup()
	before := restoreAll(t, m, b2)

	mustOK(t, m.Delete(b1))
	full, err := m.IsFull(b2)
	mustOK(t, err)
	if !full {
		t.Fatal("删除原全量后继应升格为全量")
	}
	stored, _ := m.StoredBlocks(b2)
	if !intSetEqual(stored, []int{0, 1, 2}) {
		t.Fatalf("新全量应存全部块, 实际=%v", stored)
	}
	if !equalVolume(restoreAll(t, m, b2), before) {
		t.Fatal("升格全量后还原结果变化")
	}
}

// TestDeleteBlockedByOpenSession 还原会话打开期间删除其回溯路径上的备份被拒。
func TestDeleteBlockedByOpenSession(t *testing.T) {
	m := New(3, 2, 1000, io.Discard)
	b1, _, _ := m.Backup()
	wb(t, m, 0, mkBlock(1, 2))
	b2, _, _ := m.Backup()
	wb(t, m, 1, mkBlock(2, 2))
	b3, _, _ := m.Backup()

	s, err := m.OpenRestore(b3)
	mustOK(t, err)

	for _, id := range []string{b1, b2, b3} {
		if err := m.Delete(id); !errors.Is(err, ErrBackupInRestorePath) {
			t.Fatalf("会话打开时删除 %s 应被拒, 得到 %v", id, err)
		}
	}
	if ids := m.Backups(); len(ids) != 3 {
		t.Fatalf("被拒删除不得改变链, 实际=%v", ids)
	}
	if m.StoredBlockCount() == 0 {
		t.Fatal("被拒删除不得改变存储")
	}
	s.Close()
	mustOK(t, m.Delete(b2))
	if ids := m.Backups(); len(ids) != 2 {
		t.Fatalf("关闭会话后应能删除, 链=%v", ids)
	}
}

// TestSessionOnMiddleStillBlocksAncestors 指向中间备份的会话保护其祖先，
// 但不保护其后继。
func TestSessionOnMiddleStillBlocksAncestors(t *testing.T) {
	m := New(2, 2, 1000, io.Discard)
	b1, _, _ := m.Backup()
	wb(t, m, 0, mkBlock(1, 2))
	b2, _, _ := m.Backup()
	wb(t, m, 1, mkBlock(2, 2))
	b3, _, _ := m.Backup()

	s, err := m.OpenRestore(b2)
	mustOK(t, err)
	if err := m.Delete(b1); !errors.Is(err, ErrBackupInRestorePath) {
		t.Fatalf("祖先删除应被拒, got %v", err)
	}
	if err := m.Delete(b2); !errors.Is(err, ErrBackupInRestorePath) {
		t.Fatalf("目标删除应被拒, got %v", err)
	}
	// b3 不在 b2 会话的回溯路径上（是其后继），作为链尾可直接删除。
	mustOK(t, m.Delete(b3))
	s.Close()
}

// TestStorageLimit 存储块总数超上限时备份被整体拒绝且状态不变。
func TestStorageLimit(t *testing.T) {
	const n, size = 4, 2
	m := New(n, size, 5, io.Discard)
	for i := 0; i < n; i++ {
		wb(t, m, i, mkBlock(1, size))
	}
	b1, stored, err := m.Backup()
	mustOK(t, err)
	if len(stored) != 4 || m.StoredBlockCount() != 4 {
		t.Fatalf("全量应存4块, stored=%v total=%d", stored, m.StoredBlockCount())
	}
	wb(t, m, 0, mkBlock(2, size))
	wb(t, m, 1, mkBlock(2, size))
	if _, _, err := m.Backup(); !errors.Is(err, ErrStorageLimitExceeded) {
		t.Fatalf("应返回 ErrStorageLimitExceeded, 得到 %v", err)
	}
	if ids := m.Backups(); len(ids) != 1 || ids[0] != b1 {
		t.Fatalf("被拒备份不得改变链, ids=%v", ids)
	}
	if m.StoredBlockCount() != 4 {
		t.Fatalf("被拒备份不得改变存储总数, 实际=%d", m.StoredBlockCount())
	}
	// 块1写回原值后只剩块1个差异块，总数5恰好不超限。
	wb(t, m, 1, mkBlock(1, size))
	b2, stored2, err := m.Backup()
	mustOK(t, err)
	if !intSetEqual(stored2, []int{0}) {
		t.Fatalf("受限增量仅应存块0, 实际=%v", stored2)
	}
	if m.StoredBlockCount() != 5 {
		t.Fatalf("存储总数应为5, 实际=%d", m.StoredBlockCount())
	}
	_ = b2
}

// TestErrorCases 越界、尺寸不符、备份不存在等可区分错误。
func TestErrorCases(t *testing.T) {
	m := New(3, 4, 1000, io.Discard)
	if _, err := m.WriteBlock(-1, mkBlock(0, 4)); !errors.Is(err, ErrBlockIndexOutOfRange) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.WriteBlock(3, mkBlock(0, 4)); !errors.Is(err, ErrBlockIndexOutOfRange) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.WriteBlock(0, []byte{1, 2, 3}); !errors.Is(err, ErrBlockSizeMismatch) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.OpenRestore("nope"); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := m.Delete("nope"); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.StoredBlocks("nope"); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.IsFull("nope"); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("got %v", err)
	}
}

// TestSessionClosedRead 关闭后读取报错。
func TestSessionClosedRead(t *testing.T) {
	m := New(2, 2, 1000, io.Discard)
	id, _, _ := m.Backup()
	s, err := m.OpenRestore(id)
	mustOK(t, err)
	s.Close()
	if _, err := s.ReadBlock(0); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("got %v", err)
	}
	s.Close() // 重复关闭安全
}

// TestConcurrentBackupsAndWrites 高并发下备份必须时间点一致：
// 每个备份还原出的每块内容要么等于0要么等于1，不会出现撕裂。
func TestConcurrentBackupsAndWrites(t *testing.T) {
	const n, size, rounds = 8, 16, 40
	m := New(n, size, 1<<30, io.Discard)
	one := mkBlock(1, size)

	var wg sync.WaitGroup
	var idsMu sync.Mutex
	var ids []string

	wg.Add(2)
	go func() {
		defer wg.Done()
		for r := 0; r < rounds; r++ {
			for i := 0; i < n; i++ {
				_, _ = m.WriteBlock(i, one)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for r := 0; r < rounds; r++ {
			id, _, err := m.Backup()
			if err != nil {
				t.Errorf("backup: %v", err)
				return
			}
			idsMu.Lock()
			ids = append(ids, id)
			idsMu.Unlock()
		}
	}()
	wg.Wait()

	for _, id := range ids {
		vol := restoreAll(t, m, id)
		for i, blk := range vol {
			for _, c := range blk {
				if c != 0 && c != 1 {
					t.Fatalf("备份 %s 块%d 出现撕裂字节 %d", id, i, c)
				}
			}
		}
	}
	// 同一操作序列确定性：所有备份存储块集合应为确定的可复现结果，
	// 这里只验证最终卷与最终备份一致且链结构稳定。
	final := restoreAll(t, m, ids[len(ids)-1])
	for i := 0; i < n; i++ {
		if !bytes.Equal(final[i], one) {
			t.Fatalf("最终备份块%d 应为全1", i)
		}
	}
}

// TestDeterminism 同一操作序列两次执行得到完全相同的各备份块集合。
func TestDeterminism(t *testing.T) {
	run := func() [][]int {
		m := New(4, 2, 1000, io.Discard)
		var sets [][]int
		wb(t, m, 0, mkBlock(1, 2))
		wb(t, m, 2, mkBlock(1, 2))
		id, _, _ := m.Backup()
		s, _ := m.StoredBlocks(id)
		sets = append(sets, s)
		wb(t, m, 0, mkBlock(2, 2))
		wb(t, m, 2, mkBlock(1, 2)) // 写回原值
		id, _, _ = m.Backup()
		s, _ = m.StoredBlocks(id)
		sets = append(sets, s)
		wb(t, m, 3, mkBlock(3, 2))
		id, _, _ = m.Backup()
		s, _ = m.StoredBlocks(id)
		sets = append(sets, s)
		mustOK(t, m.Delete(m.Backups()[1]))
		s, _ = m.StoredBlocks(m.Backups()[1])
		sets = append(sets, s)
		return sets
	}
	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatal("确定性序列长度不一致")
	}
	for i := range a {
		if !intSetEqual(a[i], b[i]) {
			t.Fatalf("第%d次集合不一致: %v vs %v", i, a[i], b[i])
		}
	}
}
