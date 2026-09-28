package ontology

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// Logger 用于记录每步请求、判定结果与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

type partitionState struct {
	lastSeq int64
	// window 保存当前世代下最近接受的若干序号 -> 落盘位点。
	window map[int64]int64
	// ring 按接受顺序记录窗口内序号，用于淘汰最旧条目。
	ring []int64
}

type producerState struct {
	epoch      int64
	partitions map[string]*partitionState
}

// Broker 是幂等生产者服务端序号校验器。
type Broker struct {
	mu         sync.Mutex
	producers  map[string]*producerState
	log        map[string][][]byte
	windowSize int
	logger     Logger
}

// NewBroker 创建一个空的校验器。
func NewBroker(windowSize int, logger Logger) *Broker {
	if windowSize < 1 {
		windowSize = 1
	}
	if logger == nil {
		logger = log.New(os.Stdout, "broker ", log.LstdFlags|log.Lmicroseconds)
	}
	return &Broker{
		producers:  make(map[string]*producerState),
		log:        make(map[string][][]byte),
		windowSize: windowSize,
		logger:     logger,
	}
}

// Append 判定并（在接受时）落盘一条写请求。
func (b *Broker) Append(req Request) Result {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.logger.Printf("请求 producer=%q epoch=%d partition=%q seq=%d bytes=%d",
		req.Producer, req.Epoch, req.Partition, req.Seq, len(req.Payload))

	// 第一步：非法输入整体拒绝，不触碰任何状态。
	if req.Producer == "" || req.Partition == "" || req.Epoch < 0 || req.Seq < 0 {
		return b.reject(req, RejectInvalidRequest,
			"非法输入：生产者/分区为空或世代、序号为负")
	}

	ps, known := b.producers[req.Producer]

	// 第二步：世代围栏。旧世代的请求一律拒绝。
	if known && req.Epoch < ps.epoch {
		return b.reject(req, RejectFencedEpoch,
			"过期世代：请求世代 %d 小于当前世代 %d", req.Epoch, ps.epoch)
	}

	newEpoch := !known || req.Epoch > ps.epoch
	var pt *partitionState
	first := false
	if !newEpoch {
		pt, first = ps.partitions[req.Partition]
	} else {
		// 新世代下所有分区序号状态都视为尚未建立。
		first = true
	}

	// 第三步：新世代或新分区的首条记录序号必须为 0。
	// 判定在任何状态写入之前完成，拒绝时世代也不升级。
	if first {
		if req.Seq != 0 {
			if newEpoch {
				return b.reject(req, RejectFirstSeqNotZero,
					"新世代 %d 的首条记录序号必须为 0，实际为 %d", req.Epoch, req.Seq)
			}
			return b.reject(req, RejectFirstSeqNotZero,
				"新分区的首条记录序号必须为 0，实际为 %d", req.Seq)
		}

		if newEpoch {
			ps = &producerState{epoch: req.Epoch, partitions: make(map[string]*partitionState)}
			b.producers[req.Producer] = ps
		}
		pt = &partitionState{window: make(map[int64]int64)}
		ps.partitions[req.Partition] = pt
		offset := b.appendLog(req)
		pt.lastSeq = 0
		b.remember(pt, 0, offset)
		res := Result{Accepted: true, Offset: offset, Seq: 0,
			Basis: "新世代/新分区首条序号为 0，追加落盘"}
		b.logger.Printf("接受 producer=%q seq=%d offset=%d 依据=%s",
			req.Producer, 0, offset, res.Basis)
		return res
	}

	// 第四步：同世代同分区下，按“追加 / 窗口重复 / 其余拒绝”判定。
	switch {
	case req.Seq == pt.lastSeq+1:
		offset := b.appendLog(req)
		pt.lastSeq = req.Seq
		b.remember(pt, req.Seq, offset)
		res := Result{Accepted: true, Offset: offset, Seq: req.Seq,
			Basis: "序号等于最后序号 +1，顺序追加"}
		b.logger.Printf("接受 producer=%q seq=%d offset=%d 依据=%s",
			req.Producer, req.Seq, offset, res.Basis)
		return res

	case req.Seq <= pt.lastSeq:
		if offset, ok := pt.window[req.Seq]; ok {
			res := Result{Accepted: true, Duplicate: true, Offset: offset, Seq: req.Seq,
				Basis: "序号命中最近接受窗口，判定为重复请求，返回原位点且不落盘"}
			b.logger.Printf("重复确认 producer=%q seq=%d offset=%d 依据=%s",
				req.Producer, req.Seq, offset, res.Basis)
			return res
		}
		return b.reject(req, RejectDuplicateExpired,
			"序号 %d 已不在最近窗口内（最后序号 %d），重复请求已过期",
			req.Seq, pt.lastSeq)

	default:
		return b.reject(req, RejectOutOfOrder,
			"跳号：期望序号 %d，实际为 %d", pt.lastSeq+1, req.Seq)
	}
}

// Epoch 返回生产者当前世代；未知生产者返回 0 与 false。
func (b *Broker) Epoch(producer string) (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ps, ok := b.producers[producer]
	if !ok {
		return 0, false
	}
	return ps.epoch, true
}

// LastSeq 返回生产者在某分区当前世代下的最后序号；未知返回 0 与 false。
func (b *Broker) LastSeq(producer, partition string) (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ps, ok := b.producers[producer]
	if !ok {
		return 0, false
	}
	pt, ok := ps.partitions[partition]
	if !ok {
		return 0, false
	}
	return pt.lastSeq, true
}

// OffsetCount 返回分区已经连续落盘的记录条数（下一位点）。
func (b *Broker) OffsetCount(partition string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(len(b.log[partition]))
}

// WindowContains 报告某序号是否仍在当前世代该分区的重复窗口内。
func (b *Broker) WindowContains(producer, partition string, seq int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ps, ok := b.producers[producer]
	if !ok {
		return false
	}
	pt, ok := ps.partitions[partition]
	if !ok {
		return false
	}
	_, ok = pt.window[seq]
	return ok
}

func (b *Broker) appendLog(req Request) int64 {
	offset := int64(len(b.log[req.Partition]))
	payload := make([]byte, len(req.Payload))
	copy(payload, req.Payload)
	b.log[req.Partition] = append(b.log[req.Partition], payload)
	return offset
}

func (b *Broker) remember(pt *partitionState, seq, offset int64) {
	pt.window[seq] = offset
	pt.ring = append(pt.ring, seq)
	if len(pt.ring) > b.windowSize {
		drop := pt.ring[0]
		pt.ring = pt.ring[1:]
		delete(pt.window, drop)
	}
}

func (b *Broker) reject(req Request, reason RejectReason, format string, args ...any) Result {
	basis := sprintf(format, args...)
	b.logger.Printf("拒绝 producer=%q epoch=%d partition=%q seq=%d 原因=%s 依据=%s",
		req.Producer, req.Epoch, req.Partition, req.Seq, reason, basis)
	return Result{Accepted: false, Offset: -1, Seq: req.Seq, Reason: reason, Basis: basis}
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
