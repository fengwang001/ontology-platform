// Package xfer 实现多租户对象存储的桶所有权转移服务。
//
// 服务由 registry（桶归属、桶字节数与上传计数）、ledger（租户用量
// 与额度账本）与本包（转移要约与崩溃恢复日志）构成。全部校验次序
// 集中在 Service 中；所有公共操作在单把互斥锁下串行执行，结果等价
// 于某个串行顺序。
package xfer

import (
	"errors"
	"sync"

	"ontology/ledger"
	"ontology/registry"
)

// 服务返回的全部哨兵错误。
var (
	ErrBadParam       = errors.New("参数非法")
	ErrClock          = errors.New("时钟回退")
	ErrBucketExists   = errors.New("已存在")
	ErrNoBucket       = errors.New("桶不存在")
	ErrNotOwner       = errors.New("非属主")
	ErrFrozen         = errors.New("桶被冻结")
	ErrQuota          = errors.New("额度不足")
	ErrTooManyBuckets = errors.New("桶数超限")
	ErrHasOffer       = errors.New("已有要约")
	ErrNoOffer        = errors.New("无要约")
	ErrExpired        = errors.New("已到期")
	ErrNotParty       = errors.New("非当事人")
	ErrInFlight       = errors.New("有在途上传")
	ErrNeedRecovery   = errors.New("需要恢复")
)

const (
	maxNow   = int64(1_000_000_000_000) // now 上界 10^12
	maxSize  = int64(1_000_000_000_000) // size 上界 10^12
	maxTTL   = int64(1_000_000_000)     // ttl 上界 10^9
	maxQuota = int64(1_000_000_000_000_000)
)

// LogKind 标识崩溃恢复日志记录的种类。
type LogKind int

const (
	LogBegin LogKind = iota + 1 // 步骤①：写 BEGIN
	LogStep                     // 步骤②③④：每完成一步更新步骤号
	LogEnd                      // 步骤⑤：写 END
)

// LogEntry 是一条崩溃恢复日志记录。
type LogEntry struct {
	Kind   LogKind
	Bucket string
	From   string
	To     string
	Bytes  int64
	Step   int // 已完成步骤号（1..5）
}

// OfferInfo 是桶上登记的转移要约。
type OfferInfo struct {
	From     string
	To       string
	Deadline int64 // 截止时刻；now >= Deadline 即视为已到期
}

// Service 是桶所有权转移服务。构造后可在任意 goroutine 间并发使用。
type Service struct {
	mu  sync.Mutex
	reg *registry.Registry
	led *ledger.Ledger

	offers  map[string]OfferInfo
	lastNow int64 // 上一次被接受操作的 now

	log     []LogEntry
	crashed bool // 注入的崩溃已发生，等待 Recover
	crashAt int  // 下一次 Accept 在第 crashAt 步完成后崩溃；-1 表示不注入

	touched int64 // Accept 触碰的对象记录数，恒为 0（划转只动元数据）
}

// New 构造服务，maxB 为每租户最多拥有的桶数（1 到 1000）。
func New(maxB int) *Service {
	if maxB < 1 || maxB > 1000 {
		panic("xfer: MaxB 必须在 1 到 1000 之间")
	}
	return &Service{
		reg:     registry.New(maxB),
		led:     ledger.New(),
		offers:  make(map[string]OfferInfo),
		crashAt: -1,
	}
}

// InjectCrashAfterStep 是测试钩子：下一次通过校验的 Accept 在第 k 步
// 完成后崩溃（k 为 0 到 5，0 表示未做任何事）。-1 取消注入。
func (s *Service) InjectCrashAfterStep(k int) {
	if k < -1 || k > 5 {
		panic("xfer: 崩溃步骤必须在 0 到 5 之间")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashAt = k
}

// validNow 报告 now 是否在 [0, 10^12] 内。
func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// validNames 报告所有名字是否均非空。
func validNames(names ...string) bool {
	for _, n := range names {
		if n == "" {
			return false
		}
	}
	return true
}

// frozen 报告桶在 now 时刻是否处于冻结（有未到期要约）。
// 调用方需持有 s.mu。
func (s *Service) frozen(bucket string, now int64) bool {
	o, ok := s.offers[bucket]
	return ok && now < o.Deadline
}

// clearExpired 清除桶上已到期的要约（到期要约只落地于被接受的操作）。
// 调用方需持有 s.mu。
func (s *Service) clearExpired(bucket string, now int64) {
	if o, ok := s.offers[bucket]; ok && now >= o.Deadline {
		delete(s.offers, bucket)
	}
}

// CreateBucket 建桶：桶名全局唯一，t 已拥有 MaxB 个桶时报桶数超限。
func (s *Service) CreateBucket(t, bucket string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(t, bucket) || !validNow(now) {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	if s.reg.Has(bucket) {
		return ErrBucketExists
	}
	if s.reg.Count(t) >= s.reg.MaxB() {
		return ErrTooManyBuckets
	}
	s.lastNow = now
	s.reg.Create(t, bucket)
	return nil
}

// SetLimit 设置租户额度，总被接受（可低于现有用量），不推进时钟。
func (s *Service) SetLimit(t string, q int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if t == "" || q < 0 || q > maxQuota {
		return ErrBadParam
	}
	s.led.SetLimit(t, q)
	return nil
}

// Put 向桶写入 size 字节：属主用量与桶字节数同时增加。
// 拒绝次序：参数非法 > 时钟回退 > 桶不存在 > 非属主 > 桶被冻结 > 额度不足。
func (s *Service) Put(by, bucket string, size, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(by, bucket) || !validNow(now) || size < 1 || size > maxSize {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	b, ok := s.reg.Get(bucket)
	if !ok {
		return ErrNoBucket
	}
	if b.Owner != by {
		return ErrNotOwner
	}
	if s.frozen(bucket, now) {
		return ErrFrozen
	}
	if s.led.Usage(by)+size > s.led.Limit(by) {
		return ErrQuota
	}
	s.lastNow = now
	s.clearExpired(bucket, now)
	s.reg.AddBytes(bucket, size)
	s.reg.IncObjects(bucket, 1)
	s.led.Add(by, size)
	return nil
}

// Delete 从桶删除 size 字节：属主用量与桶字节数同时减少。
// 拒绝次序同 Put（无额度检查），size 大于桶字节数在冻结之后报参数非法。
func (s *Service) Delete(by, bucket string, size, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(by, bucket) || !validNow(now) || size < 1 || size > maxSize {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	b, ok := s.reg.Get(bucket)
	if !ok {
		return ErrNoBucket
	}
	if b.Owner != by {
		return ErrNotOwner
	}
	if s.frozen(bucket, now) {
		return ErrFrozen
	}
	if size > b.Bytes {
		return ErrBadParam
	}
	s.lastNow = now
	s.clearExpired(bucket, now)
	s.reg.AddBytes(bucket, -size)
	s.led.Sub(by, size)
	return nil
}

// BeginUpload 开启一个上传，桶的开启上传数加一。拒绝次序同 Put（无额度检查）。
func (s *Service) BeginUpload(by, bucket string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(by, bucket) || !validNow(now) {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	b, ok := s.reg.Get(bucket)
	if !ok {
		return ErrNoBucket
	}
	if b.Owner != by {
		return ErrNotOwner
	}
	if s.frozen(bucket, now) {
		return ErrFrozen
	}
	s.lastNow = now
	s.clearExpired(bucket, now)
	s.reg.IncOpen(bucket)
	return nil
}

// EndUpload 结束一个上传，桶的开启上传数减一。
// 不查属主与冻结；计数为 0 时报参数非法。
func (s *Service) EndUpload(by, bucket string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(by, bucket) || !validNow(now) {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	b, ok := s.reg.Get(bucket)
	if !ok {
		return ErrNoBucket
	}
	if b.Open == 0 {
		return ErrBadParam
	}
	s.lastNow = now
	s.clearExpired(bucket, now)
	s.reg.DecOpen(bucket)
	return nil
}

// Offer 登记转移要约，截止时刻为 now+ttl。
// 拒绝次序：参数非法 > 时钟回退 > 桶不存在 > 非属主 > 已有要约；
// 已到期的要约被新要约取代。
func (s *Service) Offer(bucket, from, to string, ttl, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(bucket, from, to) || !validNow(now) || ttl < 1 || ttl > maxTTL || to == from {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	b, ok := s.reg.Get(bucket)
	if !ok {
		return ErrNoBucket
	}
	if b.Owner != from {
		return ErrNotOwner
	}
	if o, ok := s.offers[bucket]; ok && now < o.Deadline {
		return ErrHasOffer
	}
	s.lastNow = now
	s.offers[bucket] = OfferInfo{From: from, To: to, Deadline: now + ttl}
	return nil
}

// Cancel 撤销要约，by 须为要约的 from 或 to。
// 拒绝次序：参数非法 > 时钟回退 > 桶不存在 > 无要约 > 已到期 > 非当事人。
func (s *Service) Cancel(bucket, by string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(bucket, by) || !validNow(now) {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	if !s.reg.Has(bucket) {
		return ErrNoBucket
	}
	o, ok := s.offers[bucket]
	if !ok {
		return ErrNoOffer
	}
	if now >= o.Deadline {
		return ErrExpired
	}
	if by != o.From && by != o.To {
		return ErrNotParty
	}
	s.lastNow = now
	delete(s.offers, bucket)
	return nil
}

// Accept 接受要约，原子划转桶所有权与账本用量。
// 拒绝次序：参数非法 > 时钟回退 > 桶不存在 > 无要约 > 已到期 >
// 非当事人 > 有在途上传 > 桶数超限 > 额度不足。
// 通过校验即推进时钟，随后按固定步骤执行并写日志。
func (s *Service) Accept(bucket, by string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrNeedRecovery
	}
	if !validNames(bucket, by) || !validNow(now) {
		return ErrBadParam
	}
	if now < s.lastNow {
		return ErrClock
	}
	b, ok := s.reg.Get(bucket)
	if !ok {
		return ErrNoBucket
	}
	o, ok := s.offers[bucket]
	if !ok {
		return ErrNoOffer
	}
	if now >= o.Deadline {
		return ErrExpired
	}
	if by != o.To {
		return ErrNotParty
	}
	if b.Open > 0 {
		return ErrInFlight
	}
	if s.reg.Count(o.To) >= s.reg.MaxB() {
		return ErrTooManyBuckets
	}
	if s.led.Usage(o.To)+b.Bytes > s.led.Limit(o.To) {
		return ErrQuota
	}
	s.lastNow = now
	s.executeTransfer(bucket, o.From, o.To, b.Bytes)
	if s.crashed {
		return ErrNeedRecovery
	}
	return nil
}

// executeTransfer 按固定步骤执行划转并写日志；注入的崩溃在对应步骤后生效。
// 步骤：①写 BEGIN ②from 减 ③to 加 ④改属主、桶数、清要约 ⑤写 END。
// 调用方需持有 s.mu。
func (s *Service) executeTransfer(bucket, from, to string, bytes int64) {
	entry := LogEntry{Bucket: bucket, From: from, To: to, Bytes: bytes}
	if s.crashAt == 0 {
		s.crash()
		return
	}
	entry.Kind = LogBegin
	entry.Step = 1
	s.log = append(s.log, entry)
	if s.crashAt == 1 {
		s.crash()
		return
	}
	s.led.Sub(from, bytes)
	s.logStep(bucket, 2)
	if s.crashAt == 2 {
		s.crash()
		return
	}
	s.led.Add(to, bytes)
	s.logStep(bucket, 3)
	if s.crashAt == 3 {
		s.crash()
		return
	}
	s.reg.Transfer(bucket, to)
	delete(s.offers, bucket)
	s.logStep(bucket, 4)
	if s.crashAt == 4 {
		s.crash()
		return
	}
	entry.Kind = LogEnd
	entry.Step = 5
	s.log = append(s.log, entry)
	if s.crashAt == 5 {
		s.crash()
		return
	}
}

// logStep 追加一条步骤推进记录。调用方需持有 s.mu。
func (s *Service) logStep(bucket string, step int) {
	s.log = append(s.log, LogEntry{Kind: LogStep, Bucket: bucket, Step: step})
}

// crash 使注入的崩溃生效：此后除 Recover 外的一切操作报需要恢复。
func (s *Service) crash() {
	s.crashed = true
	s.crashAt = -1
}

// Recover 崩溃恢复：向前补齐未完成的步骤，幂等，不推进时钟。
// 无 BEGIN 则不改任何状态；已做过的步骤按步骤号跳过，不重复执行。
func (s *Service) Recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	begin, step, done := s.scanLog()
	if begin >= 0 && !done {
		e := s.log[begin]
		if step < 2 {
			s.led.Sub(e.From, e.Bytes)
			s.logStep(e.Bucket, 2)
		}
		if step < 3 {
			s.led.Add(e.To, e.Bytes)
			s.logStep(e.Bucket, 3)
		}
		if step < 4 {
			s.reg.Transfer(e.Bucket, e.To)
			delete(s.offers, e.Bucket)
			s.logStep(e.Bucket, 4)
		}
		e.Kind = LogEnd
		e.Step = 5
		s.log = append(s.log, e)
	}
	s.crashed = false
}

// scanLog 扫描日志，返回最后一条 BEGIN 的下标、已完成步骤号与是否已写 END。
// 无 BEGIN 时 begin 为 -1。调用方需持有 s.mu。
func (s *Service) scanLog() (begin, step int, done bool) {
	begin, step = -1, 0
	for i, e := range s.log {
		switch e.Kind {
		case LogBegin:
			begin, step, done = i, e.Step, false
		case LogStep:
			if begin >= 0 && !done && e.Step > step {
				step = e.Step
			}
		case LogEnd:
			if begin >= 0 {
				done = true
			}
		}
	}
	return begin, step, done
}

// 以下为只读查询，崩溃期间照常可用，不改变任何状态。

// Usage 返回租户当前用量。
func (s *Service) Usage(t string) int64 { return s.led.Usage(t) }

// Limit 返回租户额度，未设置时为 0。
func (s *Service) Limit(t string) int64 { return s.led.Limit(t) }

// Owner 返回桶属主；桶不存在时 ok 为 false。
func (s *Service) Owner(bucket string) (string, bool) {
	b, ok := s.reg.Get(bucket)
	return b.Owner, ok
}

// BucketBytes 返回桶字节数；桶不存在时 ok 为 false。
func (s *Service) BucketBytes(bucket string) (int64, bool) {
	b, ok := s.reg.Get(bucket)
	return b.Bytes, ok
}

// OpenUploads 返回桶的开启上传数；桶不存在时 ok 为 false。
func (s *Service) OpenUploads(bucket string) (int64, bool) {
	b, ok := s.reg.Get(bucket)
	return b.Open, ok
}

// BucketObjects 返回桶的对象记录数；桶不存在时 ok 为 false。
func (s *Service) BucketObjects(bucket string) (int64, bool) {
	b, ok := s.reg.Get(bucket)
	return b.Objects, ok
}

// BucketCount 返回租户当前拥有的桶数。
func (s *Service) BucketCount(t string) int { return s.reg.Count(t) }

// OfferOf 返回桶上登记的要约；无要约时 ok 为 false。
func (s *Service) OfferOf(bucket string) (OfferInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.offers[bucket]
	return o, ok
}

// Log 返回崩溃恢复日志的副本。
func (s *Service) Log() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogEntry, len(s.log))
	copy(out, s.log)
	return out
}
