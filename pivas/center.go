package pivas

import "sync"

// Center 是排程与稳定期判定系统的门面，所有操作可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Center struct {
	mu sync.Mutex

	hasNow  bool
	lastNow int64

	drugs map[string]Drug
	pairs map[[2]string]struct{}

	benches    map[string]*bench
	benchOrder []string // 按编号升序，保证遍历确定

	orders map[string]*orderRec

	transport [2]int64 // 按存放方式分别配置的运送时长

	batchSeq int64
}

// NewCenter 创建系统，运送时长按存放方式分别配置且必须为正整数。
func NewCenter(roomTransportSec, coldTransportSec int64) (*Center, error) {
	if roomTransportSec <= 0 || coldTransportSec <= 0 {
		return nil, errf(CodeInvalidParam, "运送时长必须为正整数: room=%d cold=%d", roomTransportSec, coldTransportSec)
	}
	c := &Center{
		drugs:   make(map[string]Drug),
		pairs:   make(map[[2]string]struct{}),
		benches: make(map[string]*bench),
		orders:  make(map[string]*orderRec),
	}
	c.transport[StorageRoom] = roomTransportSec
	c.transport[StorageCold] = coldTransportSec
	return c, nil
}

// checkClock 校验 now 合法且不回退；通过校验不代表接受。
func (c *Center) checkClock(now int64) error {
	if now < 0 || now > MaxNow {
		return errf(CodeInvalidParam, "now 超出范围 [0,%d]: %d", MaxNow, now)
	}
	if c.hasNow && now < c.lastNow {
		return errf(CodeClockRollback, "now=%d 小于上次被接受操作的 now=%d", now, c.lastNow)
	}
	return nil
}

// accept 在操作被接受后推进时钟，并清除各台已完成批次。
// 仅在接受时清除，被拒绝的操作不得改变任何状态。
func (c *Center) accept(now int64) {
	for _, bn := range c.benches {
		bn.purgeCompleted(now)
	}
	c.lastNow = now
	c.hasNow = true
}
