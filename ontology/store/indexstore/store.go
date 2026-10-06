package indexstore

// Options 控制崩溃注入；零值即正常运行。
type Options struct {
	// CrashHook 在每个持久化提交步骤前被调用；返回 true 模拟进程崩溃。
	CrashHook func(step string) bool
}

// Store 编排主表、WAL 与二级索引。一把互斥锁串行化所有对外操作，
// 使写入与追赶并发时结果等价于某个串行顺序。
type Store struct {
	disk *Disk
	mu   chan struct{} // 令牌锁（便于无第三方依赖）

	wal   *wal
	tbl   *table
	index *index

	entries []LogEntry

	log []OpRecord
}

// Open 打开（或创建）存储；崩溃后调用即完成重启加载（骨架）。
func Open(disk *Disk, opts Options) (*Store, error) {
	if disk == nil {
		disk = NewDisk()
	}
	disk.SetCrashHook(opts.CrashHook)
	s := &Store{
		disk:  disk,
		mu:    make(chan struct{}, 1),
		wal:   newWAL(disk),
		tbl:   newTable(disk),
		index: newIndex(disk),
	}
	s.mu <- struct{}{}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Put 写入一行：hasSec=false 表示置空二级键（骨架）。
func (s *Store) Put(pk, sec string, hasSec bool) (lsn int, err error) {
	s.lock()
	defer s.unlock()
	if pk == "" {
		s.record(OpRecord{Kind: OpPut, PK: pk, Sec: sec, HasSec: hasSec,
			Reason: "pk is empty", ErrCode: codeName(ErrInvalidArgument)})
		return 0, errf(ErrInvalidArgument, "pk must not be empty")
	}
	old, existed := s.tbl.get(pk)
	if hasSec && sec != "" {
		if owner, taken := s.tbl.ownerOf(sec); taken && owner != pk {
			// 判定依据来自主表最新状态（secOwners 与主表同步提交），
			// 不读可能落后的索引，因此开销与落后条数无关：一次 map 查询。
			s.record(OpRecord{Kind: OpPut, PK: pk, Sec: sec, HasSec: hasSec,
				LSN: s.tail(), Reason: "sec=" + sec + " owned by pk=" + owner + " in table",
				ErrCode: codeName(ErrUniqueConflict)})
			return 0, errf(ErrUniqueConflict,
				"secondary key %q already used by primary key %q", sec, owner)
		}
	}
	e := LogEntry{
		LSN:    s.tail() + 1,
		Op:     LogUpsert,
		PK:     pk,
		OldSec: old.Sec, HasOld: existed && old.HasSec,
		NewSec: sec, HasNew: hasSec,
	}
	// 主表与 WAL 在同一个崩溃原子步骤提交；被拒绝的写入到不了这里，
	// 因此不占 LSN、不留任何痕迹。
	if !s.tbl.commitWrite(s.wal, "put", e) {
		s.disk.SetCrashHook(nil)
		panic(crashPanic)
	}
	s.entries = append(s.entries, e)
	s.record(OpRecord{Kind: OpPut, PK: pk, Sec: sec, HasSec: hasSec, OK: true,
		LSN:    e.LSN,
		Reason: "applied to table+wal; index will absorb via catch-up lsn=" + itoa(e.LSN)})
	return e.LSN, nil
}

// Delete 删除一行（骨架）。
func (s *Store) Delete(pk string) (lsn int, err error) {
	s.lock()
	defer s.unlock()
	if pk == "" {
		s.record(OpRecord{Kind: OpDelete, PK: pk,
			Reason: "pk is empty", ErrCode: codeName(ErrInvalidArgument)})
		return 0, errf(ErrInvalidArgument, "pk must not be empty")
	}
	old, existed := s.tbl.get(pk)
	if !existed {
		s.record(OpRecord{Kind: OpDelete, PK: pk, LSN: s.tail(),
			Reason: "pk absent in table", ErrCode: codeName(ErrPrimaryNotFound)})
		return 0, errf(ErrPrimaryNotFound, "primary key %q not found", pk)
	}
	e := LogEntry{
		LSN: s.tail() + 1, Op: LogDelete, PK: pk,
		OldSec: old.Sec, HasOld: old.HasSec,
	}
	if !s.tbl.commitWrite(s.wal, "delete", e) {
		s.disk.SetCrashHook(nil)
		panic(crashPanic)
	}
	s.entries = append(s.entries, e)
	s.record(OpRecord{Kind: OpDelete, PK: pk, OK: true, LSN: e.LSN,
		Reason: "applied to table+wal"})
	return e.LSN, nil
}

// Get 按主键读主表，不受索引追赶影响（骨架）。
func (s *Store) Get(pk string) (Row, bool, error) {
	s.lock()
	defer s.unlock()
	if pk == "" {
		return Row{}, false, errf(ErrInvalidArgument, "pk must not be empty")
	}
	r, ok := s.tbl.get(pk)
	return r, ok, nil
}

// Lookup 按非空二级键查主键；追赶未完成报 ErrIndexStale（骨架）。
func (s *Store) Lookup(sec string) (pk string, found bool, err error) {
	s.lock()
	defer s.unlock()
	if sec == "" {
		return "", false, errf(ErrInvalidArgument, "sec must not be empty")
	}
	if s.Stale() {
		s.record(OpRecord{Kind: OpLookup, Sec: sec, LSN: s.tail(),
			Reason: "watermark<tail", ErrCode: codeName(ErrIndexStale)})
		return "", false, errf(ErrIndexStale,
			"index watermark %d behind log tail %d", s.index.watermark, s.tail())
	}
	owner, ok := s.index.entries[sec]
	s.record(OpRecord{Kind: OpLookup, Sec: sec, OK: true,
		Result: owner, Found: ok, LSN: s.tail(),
		Reason: "index caught up; direct map lookup"})
	return owner, ok, nil
}

// CatchUp 最多重放 maxBatch 条日志，返回已追赶到的 LSN（骨架）。
func (s *Store) CatchUp(maxBatch int) (reached int, done bool, err error) {
	s.lock()
	defer s.unlock()
	if maxBatch <= 0 {
		s.record(OpRecord{Kind: OpCatchUp, Batch: maxBatch,
			Reason:  "maxBatch must be positive",
			ErrCode: codeName(ErrInvalidArgument)})
		return s.index.watermark, false,
			errf(ErrInvalidArgument, "maxBatch must be positive, got %d", maxBatch)
	}
	from := s.index.applied
	tail := s.tail()
	end := from + maxBatch
	if end > tail {
		end = tail
	}
	// 先扫描本批窗口，序号必须恰好为 from+1..end；发现缺口则不改动
	// 索引与水位（任何 apply 都尚未发生）。
	for lsn := from + 1; lsn <= end; lsn++ {
		if s.entries[lsn-1].LSN != lsn {
			s.record(OpRecord{Kind: OpCatchUp, Batch: maxBatch,
				Reached: s.index.watermark, LSN: tail,
				Reason: "expected lsn=" + itoa(lsn) +
					" got=" + itoa(s.entries[lsn-1].LSN),
				ErrCode: codeName(ErrLogGap)})
			return s.index.watermark, false, errf(ErrLogGap,
				"log gap: expected lsn %d, found %d", lsn, s.entries[lsn-1].LSN)
		}
	}
	// 逐条幂等归并。每条只触及该日志涉及的至多两个键，O(1)/条。
	for lsn := from + 1; lsn <= end; lsn++ {
		s.index.applyOne(s.entries[lsn-1])
		if s.index.applied != lsn {
			// 崩溃注入在该条的提交步骤前触发：进程模型要求整体中止，
			// 已提交的前缀保留，重启后从 applied 继续。
			s.disk.SetCrashHook(nil)
			panic(crashPanic)
		}
	}
	// 批次边界：仅当已经触到当前日志末尾才推进水位。追赶期间并发写入
	// （由同一把锁串行）若把 tail 推得更远，本批就不推进水位——已吸收
	// 的条目安全地记在 applied 中，下一批继续，绝不遗漏或重复应用。
	advancedWM := false
	if s.index.applied == s.tail() {
		if !s.index.commitWatermark() {
			s.disk.SetCrashHook(nil)
			panic(crashPanic)
		}
		advancedWM = true
	}
	done = s.index.watermark == s.tail()
	s.record(OpRecord{Kind: OpCatchUp, Batch: maxBatch, OK: true,
		Reached: s.index.applied, LSN: s.tail(),
		Reason: "applied to=" + itoa(end) +
			" watermark" + onOff(advancedWM, " advanced", " held") +
			" done=" + onOff(done, "true", "false")})
	return s.index.applied, done, nil
}

// Verify 自检索引与主表，三类不一致可区分；追赶未完成报 ErrIndexStale（骨架）。
func (s *Store) Verify() ([]Diff, error) {
	s.lock()
	defer s.unlock()
	if s.Stale() {
		return nil, errf(ErrIndexStale,
			"verify requires caught-up index: watermark %d behind tail %d",
			s.index.watermark, s.tail())
	}
	return s.index.compareTo(s.tbl.snapshot()), nil
}

// Stale 报告索引是否落后于日志末尾。
func (s *Store) Stale() bool {
	return s.index.watermark < s.tail()
}

// Watermark 返回当前持久化水位。
func (s *Store) Watermark() int {
	return s.index.watermark
}

// Applied 返回索引内容实际已吸收到的 LSN（测试与可观测性用）。
func (s *Store) Applied() int {
	return s.index.applied
}

// Tail 返回日志末尾 LSN。
func (s *Store) Tail() int {
	return s.tail()
}

// OpLog 返回操作日志副本（输入、输出与判定依据）。
func (s *Store) OpLog() []OpRecord {
	s.lock()
	defer s.unlock()
	out := make([]OpRecord, len(s.log))
	copy(out, s.log)
	return out
}

const crashPanic = "indexstore: injected crash"

func (s *Store) load() error {
	entries, err := s.wal.load()
	if err != nil {
		return err
	}
	if err := s.tbl.load(); err != nil {
		return err
	}
	if err := s.index.load(); err != nil {
		return err
	}
	// 磁盘不变量校验：WAL 连续；主表与 WAL 末尾一致由同步提交保证；
	// 水位不超前、applied 夹在水位与日志末尾之间。
	for i, e := range entries {
		if e.LSN != i+1 {
			return errf(ErrLogGap, "wal gap on load: expected %d found %d", i+1, e.LSN)
		}
	}
	if s.index.watermark > s.index.applied {
		return errf(ErrLogGap, "watermark %d ahead of applied %d",
			s.index.watermark, s.index.applied)
	}
	if s.index.applied > len(entries) {
		return errf(ErrLogGap, "applied %d beyond log tail %d",
			s.index.applied, len(entries))
	}
	s.entries = entries
	return nil
}

func (s *Store) tail() int { return len(s.entries) }

func (s *Store) lock()   { <-s.mu }
func (s *Store) unlock() { s.mu <- struct{}{} }

func (s *Store) record(r OpRecord) {
	s.log = append(s.log, r)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func onOff(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
