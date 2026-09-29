package doublewrite

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"sync"
)

// 磁盘布局（均以扇区为边界）：
//
//	[ dataRegion )                 原位数据区，pageCount 个定长页
//	[ dwRegion   )                 双写区，capacityPages 个定长页槽
//	[ marker     ) markerSectorCount 个扇区，批次完成标记
//
// 一次批次刷写的严格写序为：
//  1. PhaseEraseMarker：用零扇区覆盖旧完成标记（使其无效）；
//  2. PhaseDoubleWrite：按页号升序把整批新页写入双写区；
//  3. PhaseMarker：写入完成标记（提交点）；
//  4. PhaseInPlace：按页号升序逐页写回原位。
//
// 只有标记有效才表示“整批已安全落在双写区”，恢复时才允许前滚。

// Phase 名常量，写入记录与日志中使用。
const (
	PhaseEraseMarker = "erase-marker"
	PhaseDoubleWrite = "doublewrite"
	PhaseMarker      = "marker"
	PhaseInPlace     = "inplace"
)

var (
	// ErrDuplicatePageID：同批页号重复。
	ErrDuplicatePageID = errors.New("doublewrite: batch contains duplicate page ids")
	// ErrPageIDOutOfRange：页号越界。
	ErrPageIDOutOfRange = errors.New("doublewrite: page id out of range")
	// ErrBatchTooLarge：批大小超过双写区容量。
	ErrBatchTooLarge = errors.New("doublewrite: batch size exceeds doublewrite capacity")
	// ErrVersionNotGreater：新版本不大于原位版本（原位损坏按版本 0 计）。
	ErrVersionNotGreater = errors.New("doublewrite: new version must be greater than in-place version")
	// ErrPayloadTooLarge：页有效负载超过页体容量。
	ErrPayloadTooLarge = errors.New("doublewrite: payload larger than page body")
	// ErrEmptyBatch：空批次不产生任何写入。
	ErrEmptyBatch = errors.New("doublewrite: empty batch")
	// ErrBadConfig：布局参数非法。
	ErrBadConfig = errors.New("doublewrite: invalid config")
)

// Config 描述磁盘布局。PageSize 必须是 SectorSize 的整数倍。
type Config struct {
	PageCount     int // 原位数据区页数
	CapacityPages int // 双写区页槽数（单批最大页数）
	PageSize      int // 页大小（字节）
}

// PendingPage 是待刷写的一页。
type PendingPage struct {
	PageID  uint32
	Version uint64
	Payload []byte
}

// PageOutcome 是单页恢复判定。
type PageOutcome string

const (
	// OutcomeIntactValid：原位校验通过且不属于提交批次，未被改动。
	OutcomeIntactValid PageOutcome = "intact-valid"
	// OutcomeRolledForward：原位被双写区新版本覆盖（含原位损坏/版本较低）。
	OutcomeRolledForward PageOutcome = "rolled-forward"
	// OutcomeKeptNewer：原位版本不低于双写版本，保持不动，拒绝回滚。
	OutcomeKeptNewer PageOutcome = "kept-newer"
	// OutcomeUnrecoverable：原位损坏且无可用副本，不可修复，版本按 0 计。
	OutcomeUnrecoverable PageOutcome = "unrecoverable"
	// OutcomeMarkerIgnored：完成标记无效，双写区整体忽略，原位不动。
	OutcomeMarkerIgnored PageOutcome = "marker-ignored"
)

// RecoveredPage 报告单页恢复判定。
type RecoveredPage struct {
	PageID  uint32
	Outcome PageOutcome
	// InPlaceVersion 是恢复前原位版本；原位损坏时为 0。
	InPlaceVersion uint64
	// CopyVersion 是双写副本版本；无有效副本或标记无效时为 0。
	CopyVersion uint64
}

// RecoverReport 汇总一次恢复结果。
type RecoverReport struct {
	// MarkerValid 为 false 时表示双写区被整体忽略、原位一字节未改。
	MarkerValid bool
	BatchSeq    uint64
	Pages       []RecoveredPage
	// Unrecoverable 列出不可修复页号（校验失败且无可用副本）。
	Unrecoverable []uint32
}

// Manager 在一块 SectorDisk 上提供串行批次刷写、并发读取与崩溃恢复。
type Manager struct {
	disk    *SectorDisk
	cfg     Config
	log     io.Writer
	dataOff int
	dwOff   int
	mrkOff  int

	flushMu sync.Mutex // 批次彼此串行生效；读也在同一把锁下取完整版本
}

// NewManager 校验布局并创建管理器。日志传 nil 表示丢弃。
func NewManager(disk *SectorDisk, cfg Config, log io.Writer) (*Manager, error) {
	if cfg.PageCount <= 0 || cfg.CapacityPages <= 0 ||
		cfg.PageSize <= 0 || cfg.PageSize%SectorSize != 0 {
		return nil, ErrBadConfig
	}
	dataBytes := cfg.PageCount * cfg.PageSize
	dwBytes := cfg.CapacityPages * cfg.PageSize
	total := dataBytes + dwBytes + markerSize
	if disk.TotalBytes() < total {
		return nil, fmt.Errorf("%w: disk too small: need %d bytes have %d",
			ErrBadConfig, total, disk.TotalBytes())
	}
	if isNilWriter(log) {
		log = io.Discard
	}
	return &Manager{
		disk:    disk,
		cfg:     cfg,
		log:     log,
		dataOff: 0,
		dwOff:   dataBytes,
		mrkOff:  dataBytes + dwBytes,
	}, nil
}

func (m *Manager) pageOffset(id uint32) int { return m.dataOff + int(id)*m.cfg.PageSize }

func (m *Manager) slotOffset(slot int) int { return m.dwOff + slot*m.cfg.PageSize }

// ReadPage 读取原位页。返回的 Payload 为拷贝；ok=false 表示页损坏/撕裂，
// 此时 version 按 0 计。读与刷写可并发调用，且只会读到完整版本。
func (m *Manager) ReadPage(pageID uint32) (version uint64, payload []byte, ok bool, err error) {
	if int(pageID) >= m.cfg.PageCount {
		return 0, nil, false, ErrPageIDOutOfRange
	}
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	raw := make([]byte, m.cfg.PageSize)
	if err = m.disk.ReadSectors(m.pageOffset(pageID), raw); err != nil {
		return 0, nil, false, err
	}
	id, ver, p, valid := decodePage(m.cfg.PageSize, raw)
	if !valid || id != pageID {
		fmt.Fprintf(m.log, "READ  page=%d -> corrupted (stored-id=%d crc-ok=%v) version=0\n",
			pageID, id, valid)
		return 0, nil, false, nil
	}
	fmt.Fprintf(m.log, "READ  page=%d version=%d bytes=%d -> ok\n", pageID, ver, len(p))
	return ver, p, true, nil
}

// inPlaceVersion 返回原位版本：损坏/页号不符按 0 计。
func (m *Manager) inPlaceVersion(pageID uint32) (uint64, error) {
	raw := make([]byte, m.cfg.PageSize)
	if err := m.disk.ReadSectors(m.pageOffset(pageID), raw); err != nil {
		return 0, err
	}
	id, ver, _, ok := decodePage(m.cfg.PageSize, raw)
	if !ok || id != pageID {
		return 0, nil
	}
	return ver, nil
}

// validateBatch 做整批拒绝校验；全部通过前不写任何扇区。
func (m *Manager) validateBatch(batch []PendingPage) ([]PendingPage, error) {
	if len(batch) == 0 {
		return nil, ErrEmptyBatch
	}
	if len(batch) > m.cfg.CapacityPages {
		return nil, fmt.Errorf("%w: batch=%d capacity=%d",
			ErrBatchTooLarge, len(batch), m.cfg.CapacityPages)
	}
	pages := make([]PendingPage, len(batch))
	copy(pages, batch)
	seen := make(map[uint32]struct{}, len(pages))
	for _, p := range pages {
		if int(p.PageID) >= m.cfg.PageCount {
			return nil, fmt.Errorf("%w: page=%d pageCount=%d",
				ErrPageIDOutOfRange, p.PageID, m.cfg.PageCount)
		}
		if _, dup := seen[p.PageID]; dup {
			return nil, fmt.Errorf("%w: page=%d", ErrDuplicatePageID, p.PageID)
		}
		seen[p.PageID] = struct{}{}
		if len(p.Payload) > m.cfg.PageSize-pageHeaderSize {
			return nil, fmt.Errorf("%w: page=%d payload=%d body=%d",
				ErrPayloadTooLarge, p.PageID, len(p.Payload),
				m.cfg.PageSize-pageHeaderSize)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageID < pages[j].PageID })
	for _, p := range pages {
		cur, err := m.inPlaceVersion(p.PageID)
		if err != nil {
			return nil, err
		}
		if p.Version <= cur {
			return nil, fmt.Errorf("%w: page=%d new=%d current=%d",
				ErrVersionNotGreater, p.PageID, p.Version, cur)
		}
	}
	return pages, nil
}

// FlushBatch 原子刷写一批页。批次彼此串行；校验不通过时不写任何扇区。
// 写序固定为：擦标记 → 双写区整批 → 完成标记 → 原位逐页。
func (m *Manager) FlushBatch(batchSeq uint64, batch []PendingPage) error {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()

	pages, err := m.validateBatch(batch)
	if err != nil {
		fmt.Fprintf(m.log, "FLUSH seq=%d pages=%d REJECTED reason=%v (no sector written)\n",
			batchSeq, len(batch), err)
		return err
	}

	ids := make([]uint32, len(pages))
	for i, p := range pages {
		ids[i] = p.PageID
	}
	fmt.Fprintf(m.log, "FLUSH seq=%d pages=%v ACCEPTED begin ordered write\n", batchSeq, ids)

	// 阶段 1：使旧完成标记失效，避免上一批残留把本批半成品误判为已提交。
	zero := make([]byte, markerSize)
	if err = m.disk.WriteSectors(m.mrkOff, zero, PhaseEraseMarker); err != nil {
		return err
	}

	// 阶段 2：整批写入双写区（按页号升序）。
	for slot, p := range pages {
		raw := encodePage(m.cfg.PageSize, p.PageID, p.Version, p.Payload)
		if err = m.disk.WriteSectors(m.slotOffset(slot), raw, PhaseDoubleWrite); err != nil {
			return err
		}
	}

	// 阶段 3：写入完成标记（提交点）。
	if err = m.disk.WriteSectors(m.mrkOff, encodeMarker(batchSeq, uint32(len(pages))), PhaseMarker); err != nil {
		return err
	}

	// 阶段 4：逐页写回原位（按页号升序）。
	for _, p := range pages {
		raw := encodePage(m.cfg.PageSize, p.PageID, p.Version, p.Payload)
		if err = m.disk.WriteSectors(m.pageOffset(p.PageID), raw, PhaseInPlace); err != nil {
			return err
		}
	}

	fmt.Fprintf(m.log, "FLUSH seq=%d committed and written back pages=%v\n", batchSeq, ids)
	return nil
}

// Recover 在任意掉电状态后执行恢复，可重复执行；第二次执行不改变任何字节。
func (m *Manager) Recover() (RecoverReport, error) {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()

	report := RecoverReport{Pages: make([]RecoveredPage, 0, m.cfg.PageCount)}

	rawMarker := make([]byte, markerSize)
	if err := m.disk.ReadSectors(m.mrkOff, rawMarker); err != nil {
		return report, err
	}
	seq, count, markerOK := decodeMarker(rawMarker)

	// 标记无效（含“完成标记前”掉电）：双写区整体忽略，原位一字节不动。
	if !markerOK {
		fmt.Fprintf(m.log, "RECOVER marker INVALID -> doublewrite region ignored, in-place untouched\n")
		for id := 0; id < m.cfg.PageCount; id++ {
			ver, _ := m.inPlaceVersion(uint32(id))
			rp := RecoveredPage{PageID: uint32(id), Outcome: OutcomeMarkerIgnored, InPlaceVersion: ver}
			if ver == 0 {
				// 没有任何可用副本：原位损坏即不可修复，版本按 0 计，绝不猜测内容。
				rp.Outcome = OutcomeUnrecoverable
				report.Unrecoverable = append(report.Unrecoverable, uint32(id))
				fmt.Fprintf(m.log, "RECOVER page=%d in-place CORRUPTED and marker invalid -> UNRECOVERABLE v=0\n", id)
			}
			report.Pages = append(report.Pages, rp)
		}
		return report, nil
	}

	// 数量越界属于标记与双写区内容不一致，按标记无效处理（绝不猜测）。
	if int(count) == 0 || int(count) > m.cfg.CapacityPages {
		fmt.Fprintf(m.log, "RECOVER marker VALID seq=%d but count=%d out of range -> ignored\n", seq, count)
		return report, nil
	}

	report.MarkerValid = true
	report.BatchSeq = seq
	fmt.Fprintf(m.log, "RECOVER marker VALID seq=%d count=%d -> roll forward from doublewrite\n", seq, count)

	// 读取批内双写副本。任一副本损坏都不能猜测：该副本视为不可用。
	type copySlot struct {
		pageID  uint32
		version uint64
		raw     []byte
		ok      bool
	}
	slots := make([]copySlot, count)
	for i := range slots {
		raw := make([]byte, m.cfg.PageSize)
		if err := m.disk.ReadSectors(m.slotOffset(i), raw); err != nil {
			return report, err
		}
		id, ver, _, valid := decodePage(m.cfg.PageSize, raw)
		// 页号越界同样视为该副本不可用。
		if valid && int(id) < m.cfg.PageCount {
			slots[i] = copySlot{pageID: id, version: ver, raw: raw, ok: true}
		} else {
			fmt.Fprintf(m.log, "RECOVER doublewrite slot=%d corrupted (stored-id=%d crc-ok=%v) -> copy unusable\n",
				i, id, valid)
		}
	}
	copyByPage := make(map[uint32]copySlot, count)
	for _, s := range slots {
		if s.ok {
			copyByPage[s.pageID] = s
		}
	}

	for id := 0; id < m.cfg.PageCount; id++ {
		pageID := uint32(id)
		inRaw := make([]byte, m.cfg.PageSize)
		if err := m.disk.ReadSectors(m.pageOffset(pageID), inRaw); err != nil {
			return report, err
		}
		inID, inVer, _, inOK := decodePage(m.cfg.PageSize, inRaw)
		if !inOK || inID != pageID {
			inVer = 0 // 原位校验失败，版本按零计
		}

		cp, hasCopy := copyByPage[pageID]
		rp := RecoveredPage{PageID: pageID, InPlaceVersion: inVer}
		if hasCopy {
			rp.CopyVersion = cp.version
		}

		switch {
		case hasCopy && cp.version > inVer:
			// 原位版本较低（含损坏按 0）：前滚为双写区版本。
			if err := m.disk.WriteSectors(m.pageOffset(pageID), cp.raw, PhaseInPlace); err != nil {
				return report, err
			}
			rp.Outcome = OutcomeRolledForward
			fmt.Fprintf(m.log, "RECOVER page=%d in-place-v=%d copy-v=%d -> ROLL FORWARD\n",
				pageID, inVer, cp.version)
		case hasCopy && inVer >= cp.version:
			// 原位版本不低于双写副本（残留旧副本）：不动，绝不回滚。
			rp.Outcome = OutcomeKeptNewer
			fmt.Fprintf(m.log, "RECOVER page=%d in-place-v=%d copy-v=%d -> KEEP (no rollback)\n",
				pageID, inVer, cp.version)
		case !inOK || inID != pageID:
			// 原位损坏且无可用副本：不可修复，不猜测任何内容。
			rp.Outcome = OutcomeUnrecoverable
			report.Unrecoverable = append(report.Unrecoverable, pageID)
			fmt.Fprintf(m.log, "RECOVER page=%d in-place CORRUPTED and no usable copy -> UNRECOVERABLE v=0\n",
				pageID)
		default:
			// 原位有效且不在本批中：保持完整版本不动。
			rp.Outcome = OutcomeIntactValid
			fmt.Fprintf(m.log, "RECOVER page=%d in-place-v=%d intact, not in batch -> untouched\n",
				pageID, inVer)
		}
		report.Pages = append(report.Pages, rp)
	}

	fmt.Fprintf(m.log, "RECOVER done seq=%d unrecoverable=%v\n", seq, report.Unrecoverable)
	return report, nil
}

// isNilWriter 报告接口是否为 nil 或持有 nil 指针（避免对 io.Discard 这类
// 非指针值做反射判空而 panic）。
func isNilWriter(w io.Writer) bool {
	if w == nil {
		return true
	}
	v := reflect.ValueOf(w)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Chan, reflect.Func, reflect.Interface, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
