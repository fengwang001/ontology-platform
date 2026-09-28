package idempotency

import (
	"fmt"
	"log/slog"
	"sync"
)

// Request 是一次生产者写请求。
type Request struct {
	ProducerID string
	Epoch      int64
	Partition  int32
	Sequence   int64
	Payload    []byte
}

// Result 是写请求被接受（含重复确认）后的结果。
// Offset 为记录在分区日志中的位点；Duplicate 为 true 表示
// 该请求是窗口内重复，Offset 为原记录首次落盘时的位点。
type Result struct {
	Offset    int64
	Duplicate bool
}

type partKey struct {
	producer  string
	partition int32
}

// partitionState 记录某 (生产者, 分区) 在当前世代下的接收状态。
type partitionState struct {
	lastSeq int64
	window  map[int64]int64 // 序号 -> 位点，仅保留最近 windowSize 条
	order   []int64         // 窗口内序号的先进先出队列
	log     [][]byte        // 分区日志，下标即位点
}

// Validator 是幂等生产者的服务端序号校验器，可并发使用。
type Validator struct {
	mu         sync.Mutex
	windowSize int
	logger     *slog.Logger
	epochs     map[string]int64
	parts      map[partKey]*partitionState
}

// NewValidator 创建校验器。windowSize 为每个 (生产者, 分区)
// 保留的最近已接受序号条数，必须为正数。
func NewValidator(windowSize int) *Validator {
	if windowSize <= 0 {
		panic("idempotency: windowSize must be positive")
	}
	return &Validator{
		windowSize: windowSize,
		logger:     slog.Default(),
		epochs:     make(map[string]int64),
		parts:      make(map[partKey]*partitionState),
	}
}

// SetLogger 替换判定日志的输出器，返回校验器本身便于链式调用。
func (v *Validator) SetLogger(l *slog.Logger) *Validator {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.logger = l
	return v
}

// Append 按四步顺序判定写请求：
//  1. 合法性：字段非法整体拒绝（ErrInvalidRequest）；
//  2. 世代围栏：世代低于当前世代拒绝（ErrProducerFenced），
//     高于当前世代则升级世代并清空该生产者全部分区状态；
//  3. 首条约束：新世代或新分区的首条记录序号必须为零；
//  4. 序号判定：等于最后序号加一则追加；命中序号窗口则判重复
//     并返回原位点；小于等于最后序号但已滑出窗口判
//     ErrDuplicateSequenceStale；大于最后序号加一判
//     ErrOutOfOrderSequence。
//
// 重复确认与任何拒绝都不改变日志、世代或序号状态。
func (v *Validator) Append(req Request) (Result, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// 第 1 步：合法性。
	if req.ProducerID == "" || req.Epoch < 0 || req.Partition < 0 ||
		req.Sequence < 0 || req.Payload == nil {
		v.log(req, "reject", "invalid request fields")
		return Result{}, fmt.Errorf("%w: producer=%q epoch=%d partition=%d sequence=%d payloadNil=%t",
			ErrInvalidRequest, req.ProducerID, req.Epoch, req.Partition, req.Sequence, req.Payload == nil)
	}

	// 第 2 步：世代围栏与升级。升级仅在请求被接受时提交，
	// 被拒的升级请求不得改变当前世代。
	curEpoch, seen := v.epochs[req.ProducerID]
	if seen && req.Epoch < curEpoch {
		v.log(req, "reject", fmt.Sprintf("stale epoch: current epoch is %d", curEpoch))
		return Result{}, fmt.Errorf("%w: producer=%q epoch=%d current=%d",
			ErrProducerFenced, req.ProducerID, req.Epoch, curEpoch)
	}
	bump := !seen || req.Epoch > curEpoch

	key := partKey{producer: req.ProducerID, partition: req.Partition}
	ps, ok := v.parts[key]

	// 第 3 步：新世代或新分区的首条记录序号必须为零。
	if bump || !ok {
		if req.Sequence != 0 {
			v.log(req, "reject", "first record of new epoch/partition must have sequence 0")
			return Result{}, fmt.Errorf("%w: producer=%q partition=%d first sequence=%d, want 0",
				ErrOutOfOrderSequence, req.ProducerID, req.Partition, req.Sequence)
		}
		if bump {
			v.epochs[req.ProducerID] = req.Epoch
			for key := range v.parts {
				if key.producer == req.ProducerID {
					delete(v.parts, key)
				}
			}
			v.log(req, "epoch-bump", fmt.Sprintf("epoch advanced to %d, partition state reset", req.Epoch))
		}
		ps = &partitionState{lastSeq: -1, window: make(map[int64]int64)}
		v.parts[key] = ps
		return v.appendLocked(req, ps)
	}

	// 第 4 步：序号判定。
	switch {
	case req.Sequence == ps.lastSeq+1:
		return v.appendLocked(req, ps)
	case req.Sequence > ps.lastSeq+1:
		v.log(req, "reject", fmt.Sprintf("sequence gap: last=%d", ps.lastSeq))
		return Result{}, fmt.Errorf("%w: producer=%q partition=%d sequence=%d, want %d",
			ErrOutOfOrderSequence, req.ProducerID, req.Partition, req.Sequence, ps.lastSeq+1)
	}
	if offset, dup := ps.window[req.Sequence]; dup {
		v.log(req, "duplicate", fmt.Sprintf("sequence already appended at offset %d", offset))
		return Result{Offset: offset, Duplicate: true}, nil
	}
	v.log(req, "reject", fmt.Sprintf("sequence evicted from window: last=%d", ps.lastSeq))
	return Result{}, fmt.Errorf("%w: producer=%q partition=%d sequence=%d last=%d",
		ErrDuplicateSequenceStale, req.ProducerID, req.Partition, req.Sequence, ps.lastSeq)
}

// appendLocked 将记录追加到分区日志并推进序号窗口。调用方须持有锁。
func (v *Validator) appendLocked(req Request, ps *partitionState) (Result, error) {
	offset := int64(len(ps.log))
	payload := make([]byte, len(req.Payload))
	copy(payload, req.Payload)
	ps.log = append(ps.log, payload)
	ps.lastSeq = req.Sequence
	ps.window[req.Sequence] = offset
	ps.order = append(ps.order, req.Sequence)
	if len(ps.order) > v.windowSize {
		delete(ps.window, ps.order[0])
		ps.order = ps.order[1:]
	}
	v.log(req, "append", fmt.Sprintf("offset=%d", offset))
	return Result{Offset: offset}, nil
}

func (v *Validator) log(req Request, decision, reason string) {
	v.logger.Info("idempotency decision",
		"producer", req.ProducerID,
		"epoch", req.Epoch,
		"partition", req.Partition,
		"sequence", req.Sequence,
		"decision", decision,
		"reason", reason,
	)
}
