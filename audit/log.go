package audit

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
)

// failHook 可由测试注入：使“下一次” Append 失败，用于验证
// 审计写入失败时动作整体回退、且不产生序号空洞。
type failHook struct {
	mu       sync.Mutex
	failNext bool
	err      error
}

// AuditLog 是按对象类型组织的只增审计日志。
//
// 每个类型拥有独立的锁与独立的序号空间：序号从 1 开始，随
// Append 原子地单调递增、无空洞。记录只允许追加，不提供任何
// 修改/删除既有记录的接口；相邻记录以 SHA-256 哈希链绑定，
// 任何对既有记录的篡改都会在 VerifyChain 中被发现。
type AuditLog struct {
	mu sync.Mutex
	// shards[typeName] 下记录追加与序号分配在同一把锁内完成。
	shards map[string]*logShard

	fail       failHook
	subs       []func(typeName string, rec Record)
	rangeReads int
}

type logShard struct {
	mu       sync.Mutex
	records  []Record
	lastHash []byte
}

// NewAuditLog 创建空的审计日志。
func NewAuditLog() *AuditLog {
	return &AuditLog{shards: make(map[string]*logShard)}
}

// Subscribe 注册一个追加成功后的监听器（如重放器的在线快照维护）。
// 监听器在类型锁外被回调，不得反向调用 Append。
func (l *AuditLog) Subscribe(fn func(typeName string, rec Record)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.subs = append(l.subs, fn)
}

// InjectNextAppendFailure 令下一次 Append 返回 err（测试用）。
func (l *AuditLog) InjectNextAppendFailure(err error) {
	l.fail.mu.Lock()
	defer l.fail.mu.Unlock()
	l.fail.failNext = true
	l.fail.err = err
}

func (l *AuditLog) takeFailure() (error, bool) {
	l.fail.mu.Lock()
	defer l.fail.mu.Unlock()
	if !l.fail.failNext {
		return nil, false
	}
	err := l.fail.err
	l.fail.failNext = false
	l.fail.err = nil
	if err == nil {
		err = fmt.Errorf("injected storage failure")
	}
	return err, true
}

// appendInput 是一条待追加记录除去序号与哈希之外的内容。
type appendInput struct {
	kind      Kind
	actionID  string
	changes   []Change
	targetSeq int64
}

// Append 在类型锁内一次性完成“失败判定 → 分配序号 → 计算哈希链
// → 落盘（内存）”。失败注入发生在分配序号之前，因此失败不占用
// 序号，保证序号连续无洞。
func (l *AuditLog) Append(typeName string, in appendInput) (Record, error) {
	if err, fail := l.takeFailure(); fail {
		return Record{}, err
	}

	l.mu.Lock()
	shard := l.shards[typeName]
	if shard == nil {
		shard = &logShard{}
		l.shards[typeName] = shard
	}
	subs := append([]func(string, Record){}, l.subs...)
	l.mu.Unlock()

	shard.mu.Lock()
	defer shard.mu.Unlock()

	seq := int64(len(shard.records) + 1)
	rec := Record{
		Seq:       seq,
		Kind:      in.kind,
		ActionID:  in.actionID,
		Changes:   append([]Change(nil), in.changes...),
		TargetSeq: in.targetSeq,
		PrevHash:  append([]byte(nil), shard.lastHash...),
	}
	rec.Hash = hashRecord(rec)
	shard.records = append(shard.records, rec)
	shard.lastHash = rec.Hash

	for _, fn := range subs {
		fn(typeName, rec)
	}
	return rec, nil
}

// LastSeq 返回某类型当前最大序号（无记录时为 0）。
func (l *AuditLog) LastSeq(typeName string) int64 {
	shard := l.shardFor(typeName)
	if shard == nil {
		return 0
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return int64(len(shard.records))
}

// At 按序号读取记录（序号从 1 开始），ok=false 表示不存在。
// 返回深拷贝，调用方无法借其修改审计序列。
func (l *AuditLog) At(typeName string, seq int64) (Record, bool) {
	shard := l.shardFor(typeName)
	if shard == nil {
		return Record{}, false
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if seq < 1 || int(seq) > len(shard.records) {
		return Record{}, false
	}
	return cloneRecord(shard.records[seq-1]), true
}

// Range 返回半开区间 [from,to) 内记录的拷贝切片。
func (l *AuditLog) Range(typeName string, from, to int64) []Record {
	shard := l.shardFor(typeName)
	if shard == nil {
		return nil
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	n := int64(len(shard.records))
	if from < 0 {
		from = 0
	}
	if to > n {
		to = n
	}
	if from >= to {
		return nil
	}
	l.addRangeReads(to - from)
	out := make([]Record, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, cloneRecord(shard.records[i]))
	}
	return out
}

func (l *AuditLog) addRangeReads(n int64) {
	l.mu.Lock()
	l.rangeReads += int(n)
	l.mu.Unlock()
}

// ResetReadCounters 清空区间读取计数（复杂度证明用）。
func (l *AuditLog) ResetReadCounters() {
	l.mu.Lock()
	l.rangeReads = 0
	l.mu.Unlock()
}

// RangeReadCount 返回区间重放累计读取的记录条数。
func (l *AuditLog) RangeReadCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rangeReads
}

func (l *AuditLog) shardFor(typeName string) *logShard {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.shards[typeName]
}

// VerifyChain 重算某类型全部记录的哈希链，任何被修改/删除/插入的
// 痕迹都会导致序号或哈希不一致而返回错误。
func (l *AuditLog) VerifyChain(typeName string) error {
	shard := l.shardFor(typeName)
	if shard == nil {
		return nil
	}
	shard.mu.Lock()
	records := append([]Record(nil), shard.records...)
	shard.mu.Unlock()

	var prev []byte
	for i, rec := range records {
		if rec.Seq != int64(i+1) {
			return fmt.Errorf("audit chain broken: unexpected seq %d at position %d", rec.Seq, i)
		}
		want := hashRecord(rec)
		if !equalBytes(rec.Hash, want) {
			return fmt.Errorf("audit chain broken: record seq %d hash mismatch", rec.Seq)
		}
		if !equalBytes(rec.PrevHash, prev) {
			return fmt.Errorf("audit chain broken: record seq %d prev-hash mismatch", rec.Seq)
		}
		prev = rec.Hash
	}
	return nil
}

func cloneRecord(r Record) Record {
	cp := r
	cp.Changes = append([]Change(nil), r.Changes...)
	cp.PrevHash = append([]byte(nil), r.PrevHash...)
	cp.Hash = append([]byte(nil), r.Hash...)
	return cp
}

func equalBytes(a, b []byte) bool {
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

// hashRecord 对记录的完整业务内容及其前驱哈希做 SHA-256。
func hashRecord(r Record) []byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(r.Seq))
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(r.Kind))
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(r.TargetSeq))
	h.Write(buf[:])
	h.Write([]byte(r.ActionID))
	h.Write([]byte{0})
	for _, c := range r.Changes {
		h.Write([]byte(c.Instance))
		h.Write([]byte{0})
		h.Write([]byte(c.Before))
		h.Write([]byte{0})
		h.Write([]byte(c.After))
		h.Write([]byte{0})
	}
	h.Write([]byte{0})
	h.Write(r.PrevHash)
	return h.Sum(nil)
}

// String 返回记录的可读形式，供测试打印输入/输出/判定依据。
func (r Record) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "seq=%d kind=%s action=%s", r.Seq, r.Kind, r.ActionID)
	if r.Kind == KindCorrection {
		fmt.Fprintf(&b, " corrects=%d", r.TargetSeq)
	}
	b.WriteString(" changes=[")
	for i, c := range r.Changes {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s:%q->%q", c.Instance, c.Before, c.After)
	}
	b.WriteByte(']')
	return b.String()
}
