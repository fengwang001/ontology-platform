package stackmgr

import "time"

// growSize 计算增长后的尺寸：必须是“满足 need 的最小尺寸”，且位于
// base, cur*1(=cur), cur*F, cur*F^2 ... 的整数倍链上；不超过 max。
// 取等处：need == 某合法尺寸时选该尺寸（不放大）；超过 max 返回 -1。
func growSize(cur, need, max, factor int) int {
	if need <= cur {
		return cur
	}
	if need > max {
		return -1
	}
	target := cur
	for target < need {
		target *= factor
		if target > max {
			return -1
		}
	}
	return target
}

// shrinkSize 计算收缩目标尺寸：仍为 base 经 F 几何链可达的值，
// 不小于 used，且一次只缩小一级档（cur/F）。
func shrinkSize(cur, used, base, factor int) int {
	if cur <= base {
		return cur
	}
	cand := cur / factor
	if cand < base {
		cand = base
	}
	if cand < used {
		return cur
	}
	return cand
}

// shouldShrink 依据严格分数比较判断是否触发收缩：used/size < Num/Den。
func (c Config) shouldShrink(used, size int) bool {
	r := c.ShrinkRatio
	return used*r.Den < size*r.Num
}

// growLocked 要求已同时持有 snapMu 与 co.mu：原子完成预留、申请、
// 拷贝、指针修正与切换。返回 true 表示发生了一次增长。
func (m *Manager) growLocked(co *coroutine, need int) (bool, error) {
	if need <= co.size {
		return false, nil
	}
	target := growSize(co.size, need, m.cfg.MaxStackSize, m.cfg.GrowthFactor)
	if target < 0 {
		return false, errf(ClassStackOverflow, "stack overflow: cannot grow within max %d",
			m.cfg.MaxStackSize)
	}
	extra := target - co.size
	if !m.qa.tryReserveLocked(extra) {
		return false, errf(ClassQuota, "quota: need %d more slots, insufficient global quota", extra)
	}
	base, ok := m.qa.allocLocked(target)
	if !ok {
		m.qa.releaseLocked(extra)
		return false, errf(ClassQuota, "quota: allocation of %d slots rejected", target)
	}
	co.finishRelocate(base, target)
	return true, nil
}

// grow 是增长搬迁入口（调用方持有 co.mu）。
//
// 为避免与 Stats 的“snapMu → 全部 co.mu”形成锁序环，这里用 TryLock：
// 抢到 snapMu 就在不释放 co.mu 的前提下完成原子搬迁；抢不到则完全
// 释放 co.mu 退避后重取并重试。整个过程中该协程的压弹等操作都要先
// 取 co.mu，故重取后由调用方继续，不会丢操作。
func (m *Manager) grow(co *coroutine, need int) (bool, error) {
	if m.snapMu.TryLock() {
		grew, err := m.growLocked(co, need)
		m.snapMu.Unlock()
		return grew, err
	}
	for {
		co.mu.Unlock()
		time.Sleep(time.Microsecond)
		co.mu.Lock()
		if m.snapMu.TryLock() {
			grew, err := m.growLocked(co, need)
			m.snapMu.Unlock()
			return grew, err
		}
	}
}

// shrinkLocked 要求已同时持有 snapMu 与 co.mu。
func (m *Manager) shrinkLocked(co *coroutine, target int) bool {
	if target >= co.size {
		return false
	}
	extra := co.size - target
	base, ok := m.qa.allocLocked(target)
	if !ok {
		return false
	}
	co.finishRelocate(base, target)
	m.qa.releaseLocked(extra)
	return true
}

// shrink 是收缩搬迁入口（调用方持有 co.mu）。
//
// 先尝试无退让的 TryLock；抢不到时释放 co.mu 后阻塞获取 snapMu、
// 再重新获取 co.mu。Stats 的锁序是 snapMu → 各 co.mu，
// 故这里释放 co.mu 后不会与 Stats 形成环。
// 单协程结构修改串行在 co.mu，重取后状态与释放前一致（本次 Pop
// 已完成的部分是纯栈内改动，发生在释放之前）。
func (m *Manager) shrink(co *coroutine, target int) bool {
	if m.snapMu.TryLock() {
		done := m.shrinkLocked(co, target)
		m.snapMu.Unlock()
		return done
	}
	co.mu.Unlock()
	m.snapMu.Lock()
	co.mu.Lock()
	done := m.shrinkLocked(co, target)
	m.snapMu.Unlock()
	return done
}
