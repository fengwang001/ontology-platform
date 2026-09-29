package doublewrite

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// 测试布局：页 = 4 扇区（2048B），数据区 4 页，双写区可容纳 2 页，
// 外加 1 个完成标记扇区 => 8+8+1 = 17 个扇区。
const (
	tPages    = 4
	tCap      = 2
	tPageSize = 4 * SectorSize
)

func testDisk() *SectorDisk {
	return NewSectorDisk(tPages*tPageSize/SectorSize + tCap*tPageSize/SectorSize + markerSectorCount)
}

func newTestManager(t *testing.T, disk *SectorDisk, log *bytes.Buffer) *Manager {
	t.Helper()
	m, err := NewManager(disk, Config{PageCount: tPages, CapacityPages: tCap, PageSize: tPageSize}, log)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// seedOldVersions 把全部数据页写成“旧版本”（每页版本 1，负载可区分）。
func seedOldVersions(t *testing.T, m *Manager) {
	t.Helper()
	for id := 0; id < tPages; id++ {
		raw := encodePage(tPageSize, uint32(id), 1, []byte(fmt.Sprintf("old-page-%d", id)))
		if err := m.disk.WriteSectors(m.pageOffset(uint32(id)), raw, "seed"); err != nil {
			t.Fatalf("seed page %d: %v", id, err)
		}
	}
}

func newBatch() []PendingPage {
	// 本批只覆盖页 1 和页 3，版本升到 2。
	return []PendingPage{
		{PageID: 3, Version: 2, Payload: []byte("new-page-3")},
		{PageID: 1, Version: 2, Payload: []byte("new-page-1")},
	}
}

// pageExpectation 描述恢复后一页应有的 (version, payload)。
type pageExpectation struct {
	version uint64
	payload string
}

// assertState 验证批内页要么全是新版本、要么全是旧版本，且没有撕裂页。
func assertAtomicState(t *testing.T, m *Manager, wantAllNew bool) {
	t.Helper()
	batchPages := map[uint32]bool{1: true, 3: true}
	gotNew := map[uint32]bool{}
	for id := uint32(0); id < tPages; id++ {
		raw := make([]byte, tPageSize)
		if err := m.disk.ReadSectors(m.pageOffset(id), raw); err != nil {
			t.Fatalf("read page %d: %v", id, err)
		}
		gotID, ver, p, ok := decodePage(tPageSize, raw)
		if !ok || gotID != id {
			t.Fatalf("page %d is torn/corrupted after recovery (ok=%v id=%d)", id, ok, gotID)
		}
		if batchPages[id] {
			gotNew[id] = ver == 2
			if wantAllNew {
				if ver != 2 || string(p) != fmt.Sprintf("new-page-%d", id) {
					t.Fatalf("page %d expected new v2, got v=%d payload=%q", id, ver, p)
				}
			} else {
				if ver != 1 || string(p) != fmt.Sprintf("old-page-%d", id) {
					t.Fatalf("page %d expected old v1, got v=%d payload=%q", id, ver, p)
				}
			}
		} else if ver != 1 {
			t.Fatalf("non-batch page %d unexpectedly changed to v=%d", id, ver)
		}
	}
	if gotNew[1] != gotNew[3] {
		t.Fatalf("batch split across versions: page1-new=%v page3-new=%v", gotNew[1], gotNew[3])
	}
}

// runCrashPoint 在 base 副本上、于第 crashAfter 个扇区以 partialBytes 前缀掉电，
// 然后恢复并断言批次原子。返回恢复报告与日志。
func runCrashPoint(t *testing.T, base *SectorDisk, crashAfter, partialBytes int, log *bytes.Buffer) RecoverReport {
	t.Helper()
	disk := base.Clone()
	disk.ArmCrash(crashAfter, partialBytes)
	m := newTestManager(t, disk, log)
	err := m.FlushBatch(100, newBatch())
	if crashAfter < totalFlushSectors() {
		if !errors.Is(err, ErrPowerLoss) {
			t.Fatalf("crashAfter=%d partial=%d: want ErrPowerLoss, got %v", crashAfter, partialBytes, err)
		}
		if !disk.Crashed() {
			t.Fatalf("crashAfter=%d partial=%d: disk did not crash", crashAfter, partialBytes)
		}
	} else if err != nil {
		t.Fatalf("crash beyond sequence should succeed, got %v", err)
	}

	rep, err := m.Recover()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}

	// 判定依据：完成标记是否有效，与期望的原子状态一致。
	assertAtomicState(t, m, rep.MarkerValid)

	// 恢复幂等：第二次恢复不得改变任何字节。
	afterFirst := disk.Bytes()
	rep2, err := m.Recover()
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if !bytes.Equal(afterFirst, disk.Bytes()) {
		t.Fatalf("crashAfter=%d partial=%d: second recover changed bytes", crashAfter, partialBytes)
	}
	if rep2.MarkerValid != rep.MarkerValid {
		t.Fatalf("second recover changed marker decision: %v -> %v", rep.MarkerValid, rep2.MarkerValid)
	}
	return rep
}

// totalFlushSectors 计算本次 Flush 的写序列扇区总数：
// 擦标记 1 + 双写 2页*4 + 标记 1 + 原位 2页*4 = 18。
func totalFlushSectors() int {
	pages := 2
	sectorsPerPage := tPageSize / SectorSize
	return markerSectorCount + pages*sectorsPerPage + markerSectorCount + pages*sectorsPerPage
}

// TestAllCrashPoints 遍历双写区每个扇区边界、完成标记前后、原位每页每个扇区
// 边界的每个掉电点（含扇区内字节前缀 0..511），断言批次原子与恢复幂等。
func TestAllCrashPoints(t *testing.T) {
	base := testDisk()
	mBase := newTestManager(t, base, nil)
	seedOldVersions(t, mBase)

	total := totalFlushSectors()
	var log bytes.Buffer
	seenValid, seenInvalid := false, false
	for crashAfter := 0; crashAfter <= total; crashAfter++ {
		// 每个扇区边界抽 0 / 1 / 255 / 511 四种前缀长度，覆盖撕裂扇区。
		for _, partial := range []int{0, 1, 255, SectorSize - 1} {
			log.Reset()
			log.WriteString(fmt.Sprintf("=== crashAfter=%d partial=%d ===\n", crashAfter, partial))
			rep := runCrashPoint(t, base, crashAfter, partial, &log)
			if rep.MarkerValid {
				seenValid = true
			} else {
				seenInvalid = true
			}
			if t.Failed() {
				t.Logf("decision log:\n%s", log.String())
				return
			}
		}
	}
	if !seenValid || !seenInvalid {
		t.Fatalf("expected both marker-valid and marker-invalid crash points, valid=%v invalid=%v",
			seenValid, seenInvalid)
	}
}

// TestRecoverIdempotentBytewise 专门断言第二次恢复逐字节不变。
func TestRecoverIdempotentBytewise(t *testing.T) {
	for _, crashAfter := range []int{0, 3, 8, 9, 10, 14, 100} {
		base := testDisk()
		mBase := newTestManager(t, base, nil)
		seedOldVersions(t, mBase)
		disk := base.Clone()
		disk.ArmCrash(crashAfter, 77)
		m := newTestManager(t, disk, nil)
		_ = m.FlushBatch(100, newBatch())
		if _, err := m.Recover(); err != nil {
			t.Fatalf("first recover: %v", err)
		}
		afterFirst := disk.Bytes()
		if _, err := m.Recover(); err != nil {
			t.Fatalf("second recover: %v", err)
		}
		if !bytes.Equal(afterFirst, disk.Bytes()) {
			t.Fatalf("crashAfter=%d: second recover changed bytes", crashAfter)
		}
	}
}

// TestDeterministicSameCrashPoint 同一刷写序列、同一掉电点必须逐字节一致。
func TestDeterministicSameCrashPoint(t *testing.T) {
	base := testDisk()
	mBase := newTestManager(t, base, nil)
	seedOldVersions(t, mBase)

	run := func() []byte {
		disk := base.Clone()
		disk.ArmCrash(11, 300)
		m := newTestManager(t, disk, nil)
		if err := m.FlushBatch(100, newBatch()); !errors.Is(err, ErrPowerLoss) {
			t.Fatalf("want power loss, got %v", err)
		}
		if _, err := m.Recover(); err != nil {
			t.Fatalf("recover: %v", err)
		}
		return disk.Bytes()
	}
	if !bytes.Equal(run(), run()) {
		t.Fatalf("same sequence + same crash point produced different bytes")
	}
}

// TestRejectionReasons 区分四类整批拒绝，且拒绝前不写任何扇区。
func TestRejectionReasons(t *testing.T) {
	cases := []struct {
		name  string
		batch []PendingPage
		want  error
	}{
		{
			name: "duplicate",
			batch: []PendingPage{
				{PageID: 1, Version: 2, Payload: []byte("a")},
				{PageID: 1, Version: 2, Payload: []byte("b")},
			},
			want: ErrDuplicatePageID,
		},
		{
			name:  "out-of-range",
			batch: []PendingPage{{PageID: uint32(tPages), Version: 2, Payload: []byte("x")}},
			want:  ErrPageIDOutOfRange,
		},
		{
			name: "too-large",
			batch: []PendingPage{
				{PageID: 0, Version: 2}, {PageID: 1, Version: 2}, {PageID: 2, Version: 2},
			},
			want: ErrBatchTooLarge,
		},
		{
			name:  "version-not-greater",
			batch: []PendingPage{{PageID: 1, Version: 1, Payload: []byte("same")}},
			want:  ErrVersionNotGreater,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disk := testDisk()
			m := newTestManager(t, disk, nil)
			seedOldVersions(t, m)
			before := disk.Bytes()
			err := m.FlushBatch(1, tc.batch)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if !bytes.Equal(before, disk.Bytes()) {
				t.Fatalf("rejection wrote sectors for %s", tc.name)
			}
			if !disk.Crashed() && len(disk.WriteRecords()) != 0 {
				t.Fatalf("rejection produced disk writes for %s: %v", tc.name, disk.WriteRecords())
			}
		})
	}
}

// TestStaleCopyNoRollback 标记有效但双写副本比原位旧时，只前滚确实更旧的页，
// 原位更新的页保持不动，绝不回滚。
func TestStaleCopyNoRollback(t *testing.T) {
	disk := testDisk()
	m := newTestManager(t, disk, nil)
	seedOldVersions(t, m) // 全部页 v1

	// 人工构造一个“已提交批次”的双写区与完成标记：页1=v2、页3=v2。
	dw1 := encodePage(tPageSize, 1, 2, []byte("dw-page-1"))
	dw3 := encodePage(tPageSize, 3, 2, []byte("dw-page-3"))
	if err := disk.WriteSectors(m.slotOffset(0), dw1, "setup-dw"); err != nil {
		t.Fatal(err)
	}
	if err := disk.WriteSectors(m.slotOffset(1), dw3, "setup-dw"); err != nil {
		t.Fatal(err)
	}
	if err := disk.WriteSectors(m.mrkOff, encodeMarker(100, 2), "setup-marker"); err != nil {
		t.Fatal(err)
	}
	// 原位：页1 已经被后续批次推进到 v3（残留双写副本不得回滚）；页3 仍是 v1（应前滚）。
	in1 := encodePage(tPageSize, 1, 3, []byte("newer-page-1"))
	if err := disk.WriteSectors(m.pageOffset(1), in1, "setup-inplace"); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	m2 := newTestManager(t, disk, &log)
	rep, err := m2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.MarkerValid {
		t.Fatalf("marker should be valid")
	}
	byPage := map[uint32]RecoveredPage{}
	for _, p := range rep.Pages {
		byPage[p.PageID] = p
	}
	if byPage[1].Outcome != OutcomeKeptNewer {
		t.Fatalf("page1 want kept-newer, got %s", byPage[1].Outcome)
	}
	if byPage[3].Outcome != OutcomeRolledForward {
		t.Fatalf("page3 want rolled-forward, got %s", byPage[3].Outcome)
	}
	if ver, p, ok, _ := m2.ReadPage(1); !ok || ver != 3 || string(p) != "newer-page-1" {
		t.Fatalf("page1 rolled back! v=%d p=%q ok=%v", ver, p, ok)
	}
	if ver, p, ok, _ := m2.ReadPage(3); !ok || ver != 2 || string(p) != "dw-page-3" {
		t.Fatalf("page3 not rolled forward: v=%d p=%q ok=%v", ver, p, ok)
	}
	if !strings.Contains(log.String(), "KEEP (no rollback)") || !strings.Contains(log.String(), "ROLL FORWARD") {
		t.Fatalf("log missing decision basis:\n%s", log.String())
	}
}

// TestSilentCorruptionUnrecoverable 原位页被静默损坏且完成标记无效（无副本），
// 必须报告不可修复、版本按 0 计，且不得改动该页。
func TestSilentCorruptionUnrecoverable(t *testing.T) {
	disk := testDisk()
	m := newTestManager(t, disk, nil)
	seedOldVersions(t, m)

	// 注入对单页的静默损坏：翻转页 2 负载区的一个字节（CRC 因此失效）。
	corruptOff := m.pageOffset(2) + pageHeaderSize + 10
	before := disk.Bytes()
	if err := disk.CorruptByte(corruptOff); err != nil {
		t.Fatal(err)
	}
	// 除该字节外不得有任何变化。
	for i := range before {
		want := before[i]
		if i == corruptOff {
			want ^= 0xFF
		}
		if disk.Bytes()[i] != want {
			t.Fatalf("CorruptByte changed unexpected byte %d", i)
		}
	}

	var log bytes.Buffer
	m2 := newTestManager(t, disk, &log)
	rep, err := m2.Recover() // 标记为全零 -> 无效 -> 忽略双写区
	if err != nil {
		t.Fatal(err)
	}
	if rep.MarkerValid {
		t.Fatalf("marker must be invalid on a clean disk")
	}
	found := false
	for _, p := range rep.Pages {
		if p.PageID == 2 {
			found = true
			if p.Outcome != OutcomeUnrecoverable || p.InPlaceVersion != 0 {
				t.Fatalf("page2 want unrecoverable v=0, got %s v=%d", p.Outcome, p.InPlaceVersion)
			}
		}
	}
	if !found {
		t.Fatalf("page2 missing from report")
	}
	if len(rep.Unrecoverable) != 1 || rep.Unrecoverable[0] != 2 {
		t.Fatalf("unrecoverable list wrong: %v", rep.Unrecoverable)
	}
	// 不可修复页不得被猜测/覆写：损坏字节保持原样。
	if disk.Bytes()[corruptOff] == before[corruptOff] {
		t.Fatalf("corrupted byte was modified during recovery")
	}
	if !strings.Contains(log.String(), "UNRECOVERABLE") {
		t.Fatalf("log missing UNRECOVERABLE basis:\n%s", log.String())
	}
}

// TestSilentCorruptionWithValidCopy 标记有效且双写副本完好时，
// 原位静默损坏的页应被前滚修复（而不是误报不可修复）。
func TestSilentCorruptionWithValidCopy(t *testing.T) {
	disk := testDisk()
	m := newTestManager(t, disk, nil)
	seedOldVersions(t, m)
	batch := []PendingPage{{PageID: 2, Version: 2, Payload: []byte("repair-page-2")}}
	disk.ArmCrash(10_000, 0) // 不掉电，完整提交
	if err := m.FlushBatch(100, batch); err != nil {
		t.Fatal(err)
	}
	// 提交后破坏原位页（模拟写回之后的介质腐坏）；双写副本仍然完好。
	if err := disk.CorruptByte(m.pageOffset(2) + pageHeaderSize + 3); err != nil {
		t.Fatal(err)
	}
	m2 := newTestManager(t, disk, nil)
	rep, err := m2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.MarkerValid {
		t.Fatalf("marker should still be valid")
	}
	if len(rep.Unrecoverable) != 0 {
		t.Fatalf("expected repair from copy, got unrecoverable: %v", rep.Unrecoverable)
	}
	if ver, p, ok, _ := m2.ReadPage(2); !ok || ver != 2 || string(p) != "repair-page-2" {
		t.Fatalf("page2 not repaired: v=%d p=%q ok=%v", ver, p, ok)
	}
}

// TestConcurrentReadersAndFlushes 刷写彼此串行；并发读者只看到完整版本。
func TestConcurrentReadersAndFlushes(t *testing.T) {
	disk := testDisk()
	m := newTestManager(t, disk, nil)
	seedOldVersions(t, m)

	var readerWg, flusherWg sync.WaitGroup
	readErr := make(chan error, 1)

	// 读者：任何时刻读到的页都必须 CRC 合法、页号正确，且版本严格为正。
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for round := 0; round < 2000; round++ {
			for id := uint32(0); id < tPages; id++ {
				ver, _, ok, err := m.ReadPage(id)
				if err != nil {
					readErr <- err
					return
				}
				if !ok || ver < 1 {
					readErr <- fmt.Errorf("reader saw torn page id=%d ok=%v ver=%d", id, ok, ver)
					return
				}
			}
		}
	}()

	// 两个刷写者提交互不相交的页集，避免跨者版本竞争；flushMu 保证磁盘上串行生效。
	flush := func(flusher int, pages []uint32) {
		defer flusherWg.Done()
		for round := uint64(0); round < 50; round++ {
			batch := make([]PendingPage, 0, len(pages))
			for _, pid := range pages {
				batch = append(batch, PendingPage{
					PageID:  pid,
					Version: 2 + uint64(flusher)*100 + round,
					Payload: []byte(fmt.Sprintf("f%d-p%d-r%d", flusher, pid, round)),
				})
			}
			if err := m.FlushBatch(uint64(flusher*1000+int(round)), batch); err != nil {
				readErr <- err
				return
			}
		}
	}
	flusherWg.Add(2)
	go flush(0, []uint32{0, 2})
	go flush(1, []uint32{1, 3})

	flusherWg.Wait()
	readerWg.Wait()
	close(readErr)
	for err := range readErr {
		t.Fatalf("concurrent access error: %v", err)
	}
}

// TestWriteOrderAndCommitPoint 断言严格写序，并确认提交点正好是完成标记：
// 标记前所有掉电点都应回滚到整批旧版本，标记起前滚到整批新版本。
func TestWriteOrderAndCommitPoint(t *testing.T) {
	base := testDisk()
	mBase := newTestManager(t, base, nil)
	seedOldVersions(t, mBase)
	dwStartSector := mBase.dwOff / SectorSize
	markSector := mBase.mrkOff / SectorSize

	// 不掉电执行一次，抓取完整写序列。
	disk := base.Clone()
	disk.ArmCrash(1_000_000, 0)
	m := newTestManager(t, disk, nil)
	if err := m.FlushBatch(100, newBatch()); err != nil {
		t.Fatal(err)
	}
	recs := disk.WriteRecords()
	var phases []string
	for _, r := range recs {
		phases = append(phases, r.Phase)
	}
	wantPhases := []string{
		PhaseEraseMarker,
		PhaseDoubleWrite, PhaseDoubleWrite,
		PhaseMarker,
		PhaseInPlace, PhaseInPlace,
	}
	if strings.Join(phases, ",") != strings.Join(wantPhases, ",") {
		t.Fatalf("write order wrong: got %v want %v", phases, wantPhases)
	}
	// 双写写必须落在双写区、标记落在标记扇区、原位写落在数据区。
	for _, r := range recs {
		switch r.Phase {
		case PhaseDoubleWrite:
			if r.SectorOffset < dwStartSector || r.SectorOffset >= markSector {
				t.Fatalf("doublewrite write outside dw region: %+v", r)
			}
		case PhaseMarker, PhaseEraseMarker:
			if r.SectorOffset != markSector {
				t.Fatalf("marker write at wrong sector: %+v", r)
			}
		case PhaseInPlace:
			if r.SectorOffset >= dwStartSector {
				t.Fatalf("in-place write outside data region: %+v", r)
			}
		}
	}

	// 写序列扇区编号（crashAfter=已完整落盘扇区数）：
	// 0=擦标记，1..8=双写两页，9=完成标记，10..17=原位两页。
	// crashAfter=10 表示含标记在内已落 10 扇区 -> 必须前滚；
	// crashAfter=9 时下一扇区是标记、标记撕裂 -> 必须忽略。
	var log bytes.Buffer
	rollRep := runCrashPoint(t, base, 10, 0, &log)
	if !rollRep.MarkerValid {
		t.Fatalf("after marker sector, batch must roll forward")
	}
	ignoreRep := runCrashPoint(t, base, 9, 0, &log)
	if ignoreRep.MarkerValid {
		t.Fatalf("before marker sector, doublewrite region must be ignored")
	}
}
