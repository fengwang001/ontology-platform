// Package api 是对外接口：并发安全地包装 demux.Engine。依赖 demux。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/demux"
	"ontology/slot"
)

type Handle = slot.Handle

// 四类可判定哨兵错误，互不相同。
var (
	ErrNoSlots  = demux.ErrNoSlots  // Open 时无空闲槽
	ErrBadID    = demux.ErrBadID    // connID 不在 [0,C)
	ErrHalfOpen = demux.ErrHalfOpen // 帧/操作指向 FREE 槽
	ErrStale    = demux.ErrStale    // gen 与当前世代不符
)

// Demux 是多路复用信道的解复用器，并发安全。
type Demux struct {
	mu sync.Mutex
	e  *demux.Engine
}

func New(C int) *Demux { return &Demux{e: demux.New(C)} }

// Open 分配当前空闲的最小 connID，世代递增，返回句柄。
func (d *Demux) Open() (Handle, error) { d.mu.Lock(); defer d.mu.Unlock(); return d.e.Open() }

// Close 要求句柄世代匹配；成功后槽回 FREE 并清空累积数据。
func (d *Demux) Close(h Handle) error { d.mu.Lock(); defer d.mu.Unlock(); return d.e.Close(h) }

// Send 校验句柄世代；本机制不真正发帧，数据不落任何状态。
func (d *Demux) Send(h Handle, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.e.Send(h, data)
}

// Recv 收到一帧，按 (id, gen) 判定投递 / ErrBadID / ErrHalfOpen / ErrStale。
func (d *Demux) Recv(id, gen int, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.e.Recv(id, gen, data)
}

func (d *Demux) Data(id int) []byte { d.mu.Lock(); defer d.mu.Unlock(); return d.e.Data(id) }

func (d *Demux) Gen(id int) int { d.mu.Lock(); defer d.mu.Unlock(); return d.e.Gen(id) }

func (d *Demux) CheckOpenCost() error { d.mu.Lock(); defer d.mu.Unlock(); return d.e.CheckOpenCost() }

// SelfCheck 在内部临时实例上核验四条不变量（不改接收者状态，可并发只读）。
func (d *Demux) SelfCheck() error {
	// 不变量 2+3：八步序列——世代隔离与半开判定
	e := demux.New(4)
	for i := 0; i < 3; i++ {
		if _, err := e.Open(); err != nil {
			return err
		}
	}
	if err := e.Close(Handle{ID: 1, Gen: 1}); err != nil {
		return err
	}
	h, err := e.Open()
	if err != nil || h != (Handle{ID: 1, Gen: 2}) {
		return fmt.Errorf("selfcheck: reuse got %+v, %v", h, err)
	}
	if !errors.Is(e.Recv(1, 1, []byte("old")), ErrStale) {
		return errors.New("selfcheck: stale frame not rejected")
	}
	if !errors.Is(e.Recv(3, 1, []byte("x")), ErrHalfOpen) {
		return errors.New("selfcheck: half-open frame not rejected")
	}
	if err := e.Recv(1, 2, []byte("hi")); err != nil || string(e.Data(1)) != "hi" {
		return errors.New("selfcheck: delivery failed")
	}
	// 不变量 4：失败不留痕——被拒操作前后状态一致
	snap := func() string { return fmt.Sprint(e.Gen(1), e.Gen(3), string(e.Data(1))) }
	before := snap()
	e.Recv(99, 1, nil)                 // ErrBadID
	e.Recv(3, 1, nil)                  // ErrHalfOpen
	e.Recv(1, 1, nil)                  // ErrStale
	e.Close(Handle{ID: 1, Gen: 1})     // ErrStale
	e.Send(Handle{ID: 3, Gen: 1}, nil) // ErrHalfOpen
	if snap() != before {
		return errors.New("selfcheck: rejected op mutated state")
	}
	// 不变量 1：与朴素参照一致（伪随机序列逐步比对）
	return checkModel(12345, 2000)
}

// checkModel 用伪随机 Open/Close/Recv 序列比对 demux 与
// 朴素参照（线性扫描最小空闲槽）的世代、累积数据与错误有无。
func checkModel(seed uint32, steps int) error {
	const C = 8
	e := demux.New(C)
	var mOpen [C]bool
	var mGen [C]int
	var mData [C]string
	rng := seed*2654435761 + 1
	next := func(n int) int { rng = rng*1664525 + 1013904223; return int(rng>>8) % n }
	var hs []Handle
	for step := 0; step < steps; step++ {
		switch next(4) {
		case 0: // Open：参照为线性扫描最小空闲槽
			h, err := e.Open()
			mid := 0
			for mid < C && mOpen[mid] {
				mid++
			}
			if (err == nil) != (mid < C) || (err == nil && h != (Handle{ID: mid, Gen: mGen[mid] + 1})) {
				return fmt.Errorf("selfcheck: open %v,%v vs slot %d", h, err, mid)
			}
			if err == nil {
				mOpen[mid], mGen[mid], mData[mid] = true, mGen[mid]+1, ""
				hs = append(hs, h)
			}
		case 1: // Close：参照按规则校验句柄
			if len(hs) > 0 {
				i := next(len(hs))
				mok := mOpen[hs[i].ID] && mGen[hs[i].ID] == hs[i].Gen
				if (e.Close(hs[i]) == nil) != mok {
					return errors.New("selfcheck: close mismatch")
				}
				mOpen[hs[i].ID], mData[hs[i].ID] = false, ""
				hs = append(hs[:i], hs[i+1:]...)
			}
		default: // Recv：参照按规则判定后追加
			id, gen := next(C+2)-1, next(3)
			mok := id >= 0 && id < C && mOpen[id] && gen == mGen[id]
			if (e.Recv(id, gen, []byte("x")) == nil) != mok {
				return fmt.Errorf("selfcheck: recv(%d,%d) mismatch", id, gen)
			}
			if mok {
				mData[id] += "x"
			}
		}
		for id := 0; id < C; id++ {
			if e.Gen(id) != mGen[id] || string(e.Data(id)) != mData[id] {
				return fmt.Errorf("selfcheck: state mismatch id=%d", id)
			}
		}
	}
	return nil
}
