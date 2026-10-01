package backup

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

const (
	testNumBlocks = 4
	testBlockSize = 8
)

// blk 生成内容可辨识的块数据。
func blk(tag string) []byte {
	b := make([]byte, testBlockSize)
	copy(b, tag)
	return b
}

// chainDesc 描述链上每个备份的 (ID, Full, 块号集合)，用于日志与确定性判定。
func chainDesc(m *Manager) string {
	var buf bytes.Buffer
	for i, b := range m.Backups() {
		if i > 0 {
			buf.WriteString(" | ")
		}
		var nos []int
		for n := 0; n < testNumBlocks; n++ {
			if b.HasBlock(n) {
				nos = append(nos, n)
			}
		}
		fmt.Fprintf(&buf, "#%d(full=%v,blocks=%v)", b.ID, b.Full, nos)
	}
	return buf.String()
}

func mustWrite(t *testing.T, m *Manager, blockNo int, tag string) {
	t.Helper()
	if err := m.Write(blockNo, blk(tag)); err != nil {
		t.Fatalf("Write(%d, %q) 出错: %v", blockNo, tag, err)
	}
	t.Logf("输入: Write(%d, %q)", blockNo, tag)
}

func mustBackup(t *testing.T, m *Manager) int {
	t.Helper()
	id, err := m.Backup()
	if err != nil {
		t.Fatalf("Backup() 出错: %v", err)
	}
	t.Logf("输入: Backup() -> 输出: id=%d; 链状态: %s", id, chainDesc(m))
	return id
}

func mustRestore(t *testing.T, m *Manager, id int) []byte {
	t.Helper()
	data, err := m.Restore(id)
	if err != nil {
		t.Fatalf("Restore(%d) 出错: %v", id, err)
	}
	t.Logf("输入: Restore(%d) -> 输出: %q", id, data)
	return data
}

func findBackup(t *testing.T, m *Manager, id int) *Backup {
	t.Helper()
	for _, b := range m.Backups() {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("备份 %d 不存在", id)
	return nil
}

// volumeImage 按块 tag 拼出期望的整卷内容，空 tag 表示全零块。
func volumeImage(tags ...string) []byte {
	var out []byte
	for _, tag := range tags {
		out = append(out, blk(tag)...)
	}
	return out
}

// TestWriteBackToOriginalNotStored 验证写过又写回原值的块不进入增量备份。
func TestWriteBackToOriginalNotStored(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 0)
	mustWrite(t, m, 0, "AAAAAAAA")
	mustWrite(t, m, 1, "BBBBBBBB")
	mustBackup(t, m) // #1 全量

	mustWrite(t, m, 0, "CCCCCCCC")
	mustWrite(t, m, 0, "AAAAAAAA") // 写回原值
	mustWrite(t, m, 1, "DDDDDDDD")
	incID := mustBackup(t, m) // #2 增量

	inc := findBackup(t, m, incID)
	t.Logf("输出: 增量备份 #%d 块数=%d; 链状态: %s", incID, inc.BlockCount(), chainDesc(m))
	t.Logf("判定依据: 块0 写过又写回原值，与父备份还原结果相同，不应存入增量；块1 值不同，应存入")
	if inc.Full {
		t.Errorf("增量备份不应为全量")
	}
	if inc.HasBlock(0) {
		t.Errorf("块0 写回了原值，不应存入增量备份")
	}
	if !inc.HasBlock(1) {
		t.Errorf("块1 与父备份不同，应存入增量备份")
	}
	if inc.BlockCount() != 1 {
		t.Errorf("增量备份应只存 1 个块，实际 %d", inc.BlockCount())
	}

	got := mustRestore(t, m, incID)
	if want := volumeImage("AAAAAAAA", "DDDDDDDD", "", ""); !bytes.Equal(got, want) {
		t.Errorf("还原结果不符:\n got %q\nwant %q", got, want)
	}
}

// TestDeleteMiddleMergesAndPrunes 验证删除中间备份时并入后继、
// 后继已有块以后继为准、且与新父还原结果相同的块被剔除。
func TestDeleteMiddleMergesAndPrunes(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 0)
	mustWrite(t, m, 0, "AAAAAAAA")
	mustWrite(t, m, 1, "BBBBBBBB")
	fullID := mustBackup(t, m) // #1 全量 {0:A,1:B}

	mustWrite(t, m, 0, "CCCCCCCC")
	midID := mustBackup(t, m) // #2 增量 {0:C}

	mustWrite(t, m, 0, "AAAAAAAA") // 块0 写回与 #1 相同的值
	mustWrite(t, m, 1, "DDDDDDDD")
	succID := mustBackup(t, m) // #3 增量 {0:A,1:D}（相对 #2 的还原结果）

	r1 := mustRestore(t, m, fullID)
	r3 := mustRestore(t, m, succID)

	t.Logf("输入: Delete(%d)（中间备份）", midID)
	if err := m.Delete(midID); err != nil {
		t.Fatalf("Delete(%d) 出错: %v", midID, err)
	}
	succ := findBackup(t, m, succID)
	t.Logf("输出: 后继 #%d 块数=%d; 链状态: %s", succID, succ.BlockCount(), chainDesc(m))
	t.Logf("判定依据: 并入时后继已有块以后继为准；并入后块0(A)与新父(#1)还原结果相同应剔除，块1(D)不同应保留")
	if succ.HasBlock(0) {
		t.Errorf("块0 与新父还原结果相同，并入后应被剔除")
	}
	if !succ.HasBlock(1) {
		t.Errorf("块1 与新父还原结果不同，应保留")
	}
	if succ.BlockCount() != 1 {
		t.Errorf("剔除后后继应只存 1 个块，实际 %d", succ.BlockCount())
	}

	t.Logf("判定依据: 删除前后每个保留备份的还原结果必须逐字节不变")
	if got := mustRestore(t, m, fullID); !bytes.Equal(got, r1) {
		t.Errorf("备份 #%d 还原结果在删除后改变:\n前 %q\n后 %q", fullID, r1, got)
	}
	if got := mustRestore(t, m, succID); !bytes.Equal(got, r3) {
		t.Errorf("备份 #%d 还原结果在删除后改变:\n前 %q\n后 %q", succID, r3, got)
	}
}

// TestDeleteUntilSingleFull 验证连续删除直到只剩一个全量备份。
func TestDeleteUntilSingleFull(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 0)
	mustWrite(t, m, 0, "AAAAAAAA")
	id1 := mustBackup(t, m) // 全量
	mustWrite(t, m, 1, "BBBBBBBB")
	id2 := mustBackup(t, m)
	mustWrite(t, m, 2, "CCCCCCCC")
	id3 := mustBackup(t, m)
	mustWrite(t, m, 3, "DDDDDDDD")
	id4 := mustBackup(t, m)

	before := map[int][]byte{}
	for _, id := range []int{id1, id2, id3, id4} {
		before[id] = mustRestore(t, m, id)
	}

	// 连续从链首删除，直到只剩一个备份。
	for _, id := range []int{id1, id2, id3} {
		t.Logf("输入: Delete(%d)", id)
		if err := m.Delete(id); err != nil {
			t.Fatalf("Delete(%d) 出错: %v", id, err)
		}
		t.Logf("输出: 链状态: %s", chainDesc(m))
		// 每个保留备份的还原结果必须不变。
		for remainID, want := range before {
			if remainID <= id {
				continue // 已删除
			}
			if got := mustRestore(t, m, remainID); !bytes.Equal(got, want) {
				t.Fatalf("删除 #%d 后备份 #%d 还原结果改变:\n前 %q\n后 %q", id, remainID, want, got)
			}
		}
	}

	last := findBackup(t, m, id4)
	t.Logf("输出: 最后备份 #%d full=%v 块数=%d", last.ID, last.Full, last.BlockCount())
	t.Logf("判定依据: 删除全量时后继变全量；只剩一个备份时它必须是存全部块的全量备份")
	if !last.Full {
		t.Errorf("唯一保留的备份应为全量")
	}
	if last.BlockCount() != testNumBlocks {
		t.Errorf("全量备份应存 %d 个块，实际 %d", testNumBlocks, last.BlockCount())
	}
	if got := m.StoredBlockCount(); got != testNumBlocks {
		t.Errorf("全链存储块数应为 %d，实际 %d", testNumBlocks, got)
	}
}

// TestDeleteBlockedByOpenRestoreSession 验证还原会话打开期间，
// 删除其回溯路径上的备份被拒，且拒绝后状态不变。
func TestDeleteBlockedByOpenRestoreSession(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 0)
	mustWrite(t, m, 0, "AAAAAAAA")
	id1 := mustBackup(t, m)
	mustWrite(t, m, 1, "BBBBBBBB")
	id2 := mustBackup(t, m)
	mustWrite(t, m, 2, "CCCCCCCC")
	id3 := mustBackup(t, m)

	sess, err := m.OpenRestore(id3)
	if err != nil {
		t.Fatalf("OpenRestore(%d) 出错: %v", id3, err)
	}
	t.Logf("输入: OpenRestore(%d) -> 会话已打开", id3)

	chainBefore := chainDesc(m)
	r2 := mustRestore(t, m, id2)

	// 会话目标是 #3，回溯路径为 #3 -> #2 -> #1，三者都应拒绝删除。
	for _, id := range []int{id1, id2, id3} {
		err := m.Delete(id)
		t.Logf("输入: Delete(%d) -> 输出: err=%v", id, err)
		if !errors.Is(err, ErrDeleteBlockedByRestore) {
			t.Errorf("Delete(%d) 应返回 ErrDeleteBlockedByRestore，实际 %v", id, err)
		}
	}
	t.Logf("判定依据: 目标处于未关闭还原会话的回溯路径上时，删除必须整体拒绝且状态不变")
	if got := chainDesc(m); got != chainBefore {
		t.Errorf("被拒绝的删除改变了链:\n前 %s\n后 %s", chainBefore, got)
	}
	if got := mustRestore(t, m, id2); !bytes.Equal(got, r2) {
		t.Errorf("被拒绝的删除改变了备份 #%d 的还原结果", id2)
	}

	// 会话内可正常读取。
	data, err := sess.ReadAll()
	if err != nil {
		t.Fatalf("会话内 ReadAll 出错: %v", err)
	}
	if want := volumeImage("AAAAAAAA", "BBBBBBBB", "CCCCCCCC", ""); !bytes.Equal(data, want) {
		t.Errorf("会话还原结果不符:\n got %q\nwant %q", data, want)
	}

	if err := sess.Close(); err != nil {
		t.Fatalf("Close 出错: %v", err)
	}
	t.Logf("输入: Close() -> 会话已关闭，路径上的删除应放行")
	if err := m.Delete(id2); err != nil {
		t.Errorf("会话关闭后 Delete(%d) 应成功，实际 %v", id2, err)
	}
	if _, err := sess.ReadAll(); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("关闭后读取应返回 ErrSessionClosed，实际 %v", err)
	}

	// 会话只保护其回溯路径：针对 #1 的会话不阻塞删除链尾 #3。
	s2, err := m.OpenRestore(id1)
	if err != nil {
		t.Fatalf("OpenRestore(%d) 出错: %v", id1, err)
	}
	if err := m.Delete(id3); err != nil {
		t.Errorf("删除不在回溯路径上的链尾 #%d 应成功，实际 %v", id3, err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close 出错: %v", err)
	}
}

// TestStorageLimit 验证存储块总数将超上限时备份被整体拒绝且状态不变。
func TestStorageLimit(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 5)
	mustWrite(t, m, 0, "AAAAAAAA")
	mustWrite(t, m, 1, "BBBBBBBB")
	id1 := mustBackup(t, m) // 全量 4 块，累计 4 <= 5

	mustWrite(t, m, 0, "CCCCCCCC")
	id2 := mustBackup(t, m) // 增量 1 块，累计 5 <= 5
	mustRestore(t, m, id2)

	mustWrite(t, m, 1, "DDDDDDDD")
	chainBefore := chainDesc(m)
	storedBefore := m.StoredBlockCount()
	_, err := m.Backup()
	t.Logf("输入: Backup()（新增 1 块，累计将达 %d > 上限 5）-> 输出: err=%v", storedBefore+1, err)
	if !errors.Is(err, ErrStorageLimitExceeded) {
		t.Fatalf("应返回 ErrStorageLimitExceeded，实际 %v", err)
	}
	t.Logf("判定依据: 存储块总数将超上限时整体拒绝，不得改变任何备份")
	if got := chainDesc(m); got != chainBefore {
		t.Errorf("被拒绝的备份改变了链:\n前 %s\n后 %s", chainBefore, got)
	}
	if got := m.StoredBlockCount(); got != storedBefore {
		t.Errorf("存储块数应保持 %d，实际 %d", storedBefore, got)
	}
	if got := mustRestore(t, m, id1); !bytes.Equal(got, volumeImage("AAAAAAAA", "BBBBBBBB", "", "")) {
		t.Errorf("被拒绝的备份改变了备份 #%d 的还原结果: %q", id1, got)
	}

	// 上限小于卷块数时，连全量备份也应被拒绝。
	m2 := NewManager(testNumBlocks, testBlockSize, 3)
	if _, err := m2.Backup(); !errors.Is(err, ErrStorageLimitExceeded) {
		t.Errorf("全量备份超上限应返回 ErrStorageLimitExceeded，实际 %v", err)
	}
	if got := len(m2.Backups()); got != 0 {
		t.Errorf("被拒绝的全量备份不应入链，链长 %d", got)
	}
}

// TestRejects 验证块号越界与备份不存在两类拒绝，且状态不变。
func TestRejects(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 0)
	mustWrite(t, m, 0, "AAAAAAAA")
	id1 := mustBackup(t, m)

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"Write(-1)", m.Write(-1, blk("X"))},
		{"Write(numBlocks)", m.Write(testNumBlocks, blk("X"))},
		{"Write(长度不符)", m.Write(0, []byte("short"))},
	} {
		t.Logf("输入: %s -> 输出: err=%v", tc.name, tc.err)
		if !errors.Is(tc.err, ErrBlockOutOfRange) {
			t.Errorf("%s 应返回 ErrBlockOutOfRange，实际 %v", tc.name, tc.err)
		}
	}
	t.Logf("判定依据: 块号越界或数据长度与块长不符必须拒绝")

	if err := m.Delete(999); !errors.Is(err, ErrBackupNotFound) {
		t.Errorf("Delete(999) 应返回 ErrBackupNotFound，实际 %v", err)
	}
	if _, err := m.OpenRestore(999); !errors.Is(err, ErrBackupNotFound) {
		t.Errorf("OpenRestore(999) 应返回 ErrBackupNotFound，实际 %v", err)
	}
	t.Logf("判定依据: 备份不存在必须拒绝")

	if got := mustRestore(t, m, id1); !bytes.Equal(got, volumeImage("AAAAAAAA", "", "", "")) {
		t.Errorf("被拒绝的操作改变了备份 #%d 的还原结果: %q", id1, got)
	}
	vol, err := m.ReadBlock(0)
	if err != nil || !bytes.Equal(vol, blk("AAAAAAAA")) {
		t.Errorf("被拒绝的写入改变了卷: %q", vol)
	}
}

// TestDeleteTailAndHead 验证删除链尾直接丢弃、删除全量后继变全量。
func TestDeleteTailAndHead(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 0)
	mustWrite(t, m, 0, "AAAAAAAA")
	id1 := mustBackup(t, m)
	mustWrite(t, m, 1, "BBBBBBBB")
	id2 := mustBackup(t, m)
	mustWrite(t, m, 2, "CCCCCCCC")
	id3 := mustBackup(t, m)

	r2 := mustRestore(t, m, id2)

	t.Logf("输入: Delete(%d)（链尾）", id3)
	if err := m.Delete(id3); err != nil {
		t.Fatalf("Delete(%d) 出错: %v", id3, err)
	}
	t.Logf("输出: 链状态: %s", chainDesc(m))
	t.Logf("判定依据: 删除链尾直接丢弃，不影响其他备份")
	if got := mustRestore(t, m, id2); !bytes.Equal(got, r2) {
		t.Errorf("删除链尾后备份 #%d 还原结果改变", id2)
	}
	if _, err := m.OpenRestore(id3); !errors.Is(err, ErrBackupNotFound) {
		t.Errorf("已删除的链尾应不存在，实际 err=%v", err)
	}

	t.Logf("输入: Delete(%d)（全量链首）", id1)
	if err := m.Delete(id1); err != nil {
		t.Fatalf("Delete(%d) 出错: %v", id1, err)
	}
	b2 := findBackup(t, m, id2)
	t.Logf("输出: 链状态: %s", chainDesc(m))
	t.Logf("判定依据: 删除全量备份时其后继变为全量，且还原结果不变")
	if !b2.Full {
		t.Errorf("后继 #%d 应变全量", id2)
	}
	if b2.BlockCount() != testNumBlocks {
		t.Errorf("变全量后应存全部 %d 块，实际 %d", testNumBlocks, b2.BlockCount())
	}
	if got := mustRestore(t, m, id2); !bytes.Equal(got, r2) {
		t.Errorf("删除全量后备份 #%d 还原结果改变:\n前 %q\n后 %q", id2, r2, got)
	}
}

// TestDeterministicBlockSets 验证同一操作序列得到相同的各备份块集合。
func TestDeterministicBlockSets(t *testing.T) {
	run := func() string {
		m := NewManager(testNumBlocks, testBlockSize, 0)
		// op: write(arg=块号, tag) / backup / delete(arg=备份ID)
		seq := []struct {
			op  string
			arg int
			tag string
		}{
			{"write", 0, "AAAAAAAA"},
			{"backup", 0, ""},
			{"write", 1, "BBBBBBBB"},
			{"write", 0, "CCCCCCCC"},
			{"backup", 0, ""},
			{"write", 0, "AAAAAAAA"},
			{"backup", 0, ""},
			{"delete", 2, ""},
			{"write", 2, "DDDDDDDD"},
			{"backup", 0, ""},
			{"delete", 1, ""},
		}
		for _, s := range seq {
			switch s.op {
			case "write":
				if err := m.Write(s.arg, blk(s.tag)); err != nil {
					t.Fatalf("Write 出错: %v", err)
				}
			case "backup":
				if _, err := m.Backup(); err != nil {
					t.Fatalf("Backup 出错: %v", err)
				}
			case "delete":
				if err := m.Delete(s.arg); err != nil {
					t.Fatalf("Delete 出错: %v", err)
				}
			}
		}
		return chainDesc(m)
	}
	first, second := run(), run()
	t.Logf("输出: 第一次 %s", first)
	t.Logf("输出: 第二次 %s", second)
	t.Logf("判定依据: 同一操作序列必须得到相同的各备份块集合")
	if first != second {
		t.Errorf("两次运行的块集合不一致")
	}
}

// TestConcurrent 验证写、备份、还原会话与删除可并发调用，
// 且备份是时间点一致的。
func TestConcurrent(t *testing.T) {
	m := NewManager(testNumBlocks, testBlockSize, 0)
	if _, err := m.Backup(); err != nil { // #1 全量（全零卷）
		t.Fatalf("Backup 出错: %v", err)
	}

	const writesPerWorker = 200
	var wg sync.WaitGroup
	for w := 0; w < testNumBlocks; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < writesPerWorker; i++ {
				tag := fmt.Sprintf("w%d-%04d", n, i)
				if err := m.Write(n, blk(tag)); err != nil {
					t.Errorf("Write 出错: %v", err)
					return
				}
			}
		}(w)
	}

	var bg sync.WaitGroup
	// 并发备份（有界次数）。
	bg.Add(1)
	go func() {
		defer bg.Done()
		for i := 0; i < 50; i++ {
			if _, err := m.Backup(); err != nil {
				t.Errorf("Backup 出错: %v", err)
				return
			}
		}
	}()
	// 并发还原会话（有界次数）。
	bg.Add(1)
	go func() {
		defer bg.Done()
		for i := 0; i < 100; i++ {
			s, err := m.OpenRestore(1)
			if err != nil {
				continue
			}
			if _, err := s.ReadAll(); err != nil {
				t.Errorf("ReadAll 出错: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Errorf("Close 出错: %v", err)
			}
		}
	}()

	wg.Wait()
	bg.Wait()

	// 写全部落盘后做最终备份：其还原结果必须与卷当前内容逐字节一致，
	// 以此验证备份的时间点一致性。
	finalID, err := m.Backup()
	if err != nil {
		t.Fatalf("最终 Backup 出错: %v", err)
	}
	got, err := m.Restore(finalID)
	if err != nil {
		t.Fatalf("Restore(%d) 出错: %v", finalID, err)
	}
	var want []byte
	for n := 0; n < testNumBlocks; n++ {
		want = append(want, blk(fmt.Sprintf("w%d-%04d", n, writesPerWorker-1))...)
	}
	t.Logf("判定依据: 无并发写时创建的备份，其还原结果必须与卷当前内容逐字节一致")
	if !bytes.Equal(got, want) {
		t.Errorf("最终备份还原结果与卷不一致:\n got %q\nwant %q", got, want)
	}

	// 并发删除中间备份：删除后保留备份的还原结果必须不变。
	keep := map[int]bool{1: true, finalID: true}
	wantRestore := mustRestore(t, m, finalID)
	var delWg sync.WaitGroup
	for _, b := range m.Backups() {
		if keep[b.ID] {
			continue
		}
		delWg.Add(1)
		go func(id int) {
			defer delWg.Done()
			if err := m.Delete(id); err != nil {
				t.Errorf("Delete(%d) 出错: %v", id, err)
			}
		}(b.ID)
	}
	delWg.Wait()
	if got := mustRestore(t, m, finalID); !bytes.Equal(got, wantRestore) {
		t.Errorf("并发删除后最终备份还原结果改变")
	}
	t.Logf("输出: 并发删除后链状态: %s", chainDesc(m))
}
