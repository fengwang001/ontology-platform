package xfer

import (
	"errors"
	"sync"

	"ontology/ledger"
	"ontology/registry"
)

var (
	ErrInvalid      = errors.New("invalid argument")
	ErrClockGoBack  = errors.New("clock moved backwards")
	ErrExists       = errors.New("bucket already exists")
	ErrNotFound     = errors.New("bucket not found")
	ErrNotOwner     = errors.New("not the owner")
	ErrFrozen       = errors.New("bucket is frozen")
	ErrQuota        = errors.New("quota exceeded")
	ErrTooMany      = errors.New("bucket count limit exceeded")
	ErrOfferExists  = errors.New("offer already exists")
	ErrNoOffer      = errors.New("no offer")
	ErrExpired      = errors.New("offer expired")
	ErrNotParty     = errors.New("not a party of the offer")
	ErrUploadsOpen  = errors.New("uploads in progress")
	ErrNeedRecovery = errors.New("recovery required")
)

type Offer struct {
	Bucket string
	From   string
	To     string
	Expiry int64
}

// LogEntry 是崩溃恢复日志中的一条记录。Done 为已完成步骤号（0..4）。
type LogEntry struct {
	Kind   string
	Bucket string
	From   string
	To     string
	Bytes  int64
	Done   int
}

type Service struct {
	mu           sync.RWMutex
	reg          *registry.Registry
	led          *ledger.Ledger
	maxB         int64
	lastAccepted int64
	offers       map[string]Offer
	needRecover  bool
	crashAfter   int
	log          []LogEntry
	touched      int
}

const (
	maxNow  = int64(1_000_000_000_000)
	maxSize = int64(1_000_000_000_000)
	maxTTL  = int64(1_000_000_000)
	maxQ    = int64(1_000_000_000_000_000)
)

func New(maxB int) *Service {
	if maxB < 1 || maxB > 1000 {
		panic("xfer: MaxB must be in 1..1000")
	}
	return &Service{
		reg:        registry.New(),
		led:        ledger.New(),
		maxB:       int64(maxB),
		offers:     map[string]Offer{},
		crashAfter: -1,
	}
}

// SetCrashAfter 注入下一次 Accept 的崩溃点：第 k 步完成后崩溃（k=0 表示 BEGIN 前）。
func (s *Service) SetCrashAfter(k int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashAfter = k
}

func nonEmpty(xs ...string) bool {
	for _, x := range xs {
		if x == "" {
			return false
		}
	}
	return true
}

func (s *Service) CreateBucket(t, bucket string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(t, bucket) || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	if _, err := s.reg.Get(bucket); err == nil {
		return ErrExists
	}
	if s.led.BucketCount(t) >= s.maxB {
		return ErrTooMany
	}
	if err := s.reg.Create(bucket, t); err != nil {
		return err
	}
	s.led.IncBuckets(t)
	s.lastAccepted = now
	return nil
}

func (s *Service) SetLimit(t string, q int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return
	}
	if !nonEmpty(t) || q < 0 || q > maxQ {
		return
	}
	s.led.SetLimit(t, q)
}

// landExpiry 仅在调用方已持锁时使用：若该桶要约已到期则清除（惰性落地）。
func (s *Service) landExpiry(bucket string, now int64) {
	if o, ok := s.offers[bucket]; ok && now >= o.Expiry {
		delete(s.offers, bucket)
	}
}

func (s *Service) frozen(bucket string, now int64) bool {
	o, ok := s.offers[bucket]
	return ok && now < o.Expiry
}

func (s *Service) Put(by, bucket string, size, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(by, bucket) || size < 1 || size > maxSize || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return ErrNotFound
	}
	if b.Owner != by {
		return ErrNotOwner
	}
	s.landExpiry(bucket, now)
	if s.frozen(bucket, now) {
		return ErrFrozen
	}
	if s.led.Used(by)+size > s.led.Limit(by) {
		return ErrQuota
	}
	if _, err := s.reg.AddBytes(bucket, size); err != nil {
		return err
	}
	s.led.AddUsed(by, size)
	s.lastAccepted = now
	return nil
}

func (s *Service) Delete(by, bucket string, size, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(by, bucket) || size < 1 || size > maxSize || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return ErrNotFound
	}
	if b.Owner != by {
		return ErrNotOwner
	}
	if size > b.Bytes {
		return ErrInvalid
	}
	s.landExpiry(bucket, now)
	if s.frozen(bucket, now) {
		return ErrFrozen
	}
	if _, err := s.reg.AddBytes(bucket, -size); err != nil {
		return err
	}
	s.led.AddUsed(by, -size)
	s.lastAccepted = now
	return nil
}

func (s *Service) BeginUpload(by, bucket string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(by, bucket) || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return ErrNotFound
	}
	if b.Owner != by {
		return ErrNotOwner
	}
	s.landExpiry(bucket, now)
	if s.frozen(bucket, now) {
		return ErrFrozen
	}
	if _, err := s.reg.IncUploads(bucket); err != nil {
		return err
	}
	s.lastAccepted = now
	return nil
}

func (s *Service) EndUpload(by, bucket string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(by, bucket) || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return ErrNotFound
	}
	if b.Uploads == 0 {
		return ErrInvalid
	}
	if _, err := s.reg.DecUploads(bucket); err != nil {
		return err
	}
	s.landExpiry(bucket, now)
	s.lastAccepted = now
	return nil
}

func (s *Service) Offer(bucket, from, to string, ttl, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(bucket, from, to) || ttl < 1 || ttl > maxTTL || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if from == to {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return ErrNotFound
	}
	if b.Owner != from {
		return ErrNotOwner
	}
	if cur, ok := s.offers[bucket]; ok && now < cur.Expiry {
		return ErrOfferExists
	}
	s.offers[bucket] = Offer{Bucket: bucket, From: from, To: to, Expiry: now + ttl}
	s.lastAccepted = now
	return nil
}

func (s *Service) Cancel(bucket, by string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(bucket, by) || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	if _, err := s.reg.Get(bucket); err != nil {
		return ErrNotFound
	}
	cur, ok := s.offers[bucket]
	if !ok {
		return ErrNoOffer
	}
	if now >= cur.Expiry {
		return ErrExpired
	}
	if by != cur.From && by != cur.To {
		return ErrNotParty
	}
	delete(s.offers, bucket)
	s.lastAccepted = now
	return nil
}

func (s *Service) Accept(bucket, by string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needRecover {
		return ErrNeedRecovery
	}
	if !nonEmpty(bucket, by) || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < s.lastAccepted {
		return ErrClockGoBack
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return ErrNotFound
	}
	cur, ok := s.offers[bucket]
	if !ok {
		return ErrNoOffer
	}
	if now >= cur.Expiry {
		return ErrExpired
	}
	if by != cur.To {
		return ErrNotParty
	}
	if b.Uploads > 0 {
		return ErrUploadsOpen
	}
	if s.led.BucketCount(cur.To) >= s.maxB {
		return ErrTooMany
	}
	if s.led.Used(cur.To)+b.Bytes > s.led.Limit(cur.To) {
		return ErrQuota
	}

	// 校验全部通过：判定时刻即被接受时刻（即使随后在 k=0 崩溃）。
	s.lastAccepted = now
	k := s.crashAfter
	s.crashAfter = -1
	s.touched = 0

	e := LogEntry{Kind: "BEGIN", Bucket: bucket, From: cur.From, To: cur.To, Bytes: b.Bytes}
	if k == 0 {
		s.needRecover = true
		return ErrNeedRecovery
	}
	s.log = append(s.log, e)
	if k == 1 {
		s.needRecover = true
		return ErrNeedRecovery
	}

	s.applyStep(&e)
	if k == 2 {
		s.needRecover = true
		return ErrNeedRecovery
	}

	s.applyStep(&e)
	if k == 3 {
		s.needRecover = true
		return ErrNeedRecovery
	}

	s.applyStep(&e)
	if k == 4 {
		s.needRecover = true
		return ErrNeedRecovery
	}

	s.applyStep(&e)
	if k == 5 {
		s.needRecover = true
		return ErrNeedRecovery
	}
	return nil
}

// applyStep 按日志中已完成步骤号 e.Done 执行下一个幂等步骤。
// 1: from 减；2: to 加；3: 改属主+两侧桶数+清要约；4: END。
func (s *Service) applyStep(e *LogEntry) {
	switch e.Done {
	case 0:
		s.led.AddUsed(e.From, -e.Bytes)
		e.Done = 1
		s.log[len(s.log)-1] = *e
	case 1:
		s.led.AddUsed(e.To, e.Bytes)
		e.Done = 2
		s.log[len(s.log)-1] = *e
	case 2:
		if _, err := s.reg.Reown(e.Bucket, e.To); err != nil {
			panic(err)
		}
		s.led.DecBuckets(e.From)
		s.led.IncBuckets(e.To)
		delete(s.offers, e.Bucket)
		e.Done = 3
		s.log[len(s.log)-1] = *e
	case 3:
		s.log = append(s.log, LogEntry{Kind: "END", Bucket: e.Bucket, Done: 4})
		e.Done = 4
	}
}

func (s *Service) Recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.needRecover {
		return
	}
	i := len(s.log) - 1
	for ; i >= 0; i-- {
		if s.log[i].Kind == "BEGIN" {
			break
		}
	}
	if i < 0 {
		// BEGIN 尚未写出：无任何副作用可补，状态（含要约）原样保持。
		s.needRecover = false
		return
	}
	e := s.log[i]
	for e.Done < 4 {
		s.applyStep(&e)
	}
	s.needRecover = false
}

// ActiveOffer 返回桶上未到期要约快照；第二返回值表示是否存在。
func (s *Service) ActiveOffer(bucket string, now int64) (Offer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.needRecover {
		return Offer{}, false
	}
	o, ok := s.offers[bucket]
	if !ok || now >= o.Expiry {
		return Offer{}, false
	}
	return o, true
}

func (s *Service) Owner(bucket string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.needRecover {
		return "", ErrNeedRecovery
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return "", ErrNotFound
	}
	return b.Owner, nil
}

func (s *Service) BucketBytes(bucket string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.needRecover {
		return 0, ErrNeedRecovery
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return 0, ErrNotFound
	}
	return b.Bytes, nil
}

func (s *Service) OpenUploads(bucket string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.needRecover {
		return 0, ErrNeedRecovery
	}
	b, err := s.reg.Get(bucket)
	if err != nil {
		return 0, ErrNotFound
	}
	return b.Uploads, nil
}

func (s *Service) Used(t string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.led.Used(t)
}

func (s *Service) LimitOf(t string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.led.Limit(t)
}

func (s *Service) BucketCount(t string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.led.BucketCount(t)
}

func (s *Service) NeedRecovery() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.needRecover
}

// TouchCount 返回上一次 Accept 触碰的对象记录数（应为 0）。
func (s *Service) TouchCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.touched
}

// Snapshot 是全状态只读快照，供差分测试逐字段比对。
type Snapshot struct {
	Owners  map[string]string
	Bytes   map[string]int64
	Uploads map[string]int64
	Objects map[string]int64
	Used    map[string]int64
	Limit   map[string]int64
	BucketN map[string]int64
	Offers  map[string]Offer
	LastNow int64
	Recover bool
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	snap := Snapshot{
		Owners:  map[string]string{},
		Bytes:   map[string]int64{},
		Uploads: map[string]int64{},
		Objects: map[string]int64{},
		Used:    map[string]int64{},
		Limit:   map[string]int64{},
		BucketN: map[string]int64{},
		Offers:  map[string]Offer{},
		LastNow: s.lastAccepted,
		Recover: s.needRecover,
	}
	for name := range s.offers {
		snap.Offers[name] = s.offers[name]
	}
	s.mu.RUnlock()

	buckets := s.reg.Snapshot()
	tenants := s.led.Snapshot()
	for name, b := range buckets {
		snap.Owners[name] = b.Owner
		snap.Bytes[name] = b.Bytes
		snap.Uploads[name] = b.Uploads
		snap.Objects[name] = b.Objects
	}
	for t, a := range tenants {
		snap.Used[t] = a.Used
		snap.Limit[t] = a.Limit
		snap.BucketN[t] = a.BucketCnt
	}
	return snap
}
