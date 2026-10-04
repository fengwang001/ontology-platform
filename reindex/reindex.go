// Package reindex 协调源索引到目标索引的在线重建。
package reindex

import (
	"errors"
	"sync"

	"ontology/dest"
	"ontology/source"
)

type State int

const (
	Idle State = iota
	Running
	Switched
)

var (
	ErrInvalid     = errors.New("reindex: invalid argument")
	ErrState       = errors.New("reindex: invalid state")
	ErrNotFinished = errors.New("reindex: backfill not finished")
	ErrTolerance   = errors.New("reindex: failure set exceeds tolerance")
)

// Coordinator 在线重建协调器。
type Coordinator struct {
	mu      sync.RWMutex
	src     *source.Source
	dst     *dest.Dest
	state   State
	B       int
	snap    []source.Doc
	scanned int
}

// New 创建协调器：内部源/目标由同一把锁线性化。
func New(L int) (*Coordinator, error) {
	d, err := dest.New(L)
	if err != nil {
		return nil, ErrInvalid
	}
	c := &Coordinator{dst: d}
	c.src = source.NewWithLock(&c.mu)
	c.src.SetHook(c.forwardPut, c.forwardDelete)
	return c, nil
}

// forwardPut / forwardDelete 是源接受写入后的原子转发钩子。
// 调用时已持有 c.mu（源 Put/Delete 持锁进入），故直接操作 dest。
func (c *Coordinator) forwardPut(doc source.Doc) {
	if c.state != Running {
		return
	}
	c.dst.Index(doc.ID, doc.Body, doc.Seq)
}

func (c *Coordinator) forwardDelete(id string, seq int64) {
	if c.state != Running {
		return
	}
	c.dst.Delete(id, seq)
}

// Source 暴露内部源（供测试通过协调器发起源写入）。
func (c *Coordinator) Source() *source.Source { return c.src }

// Dest 暴露内部目标（测试/校验用）。
func (c *Coordinator) Dest() *dest.Dest { return c.dst }

func (c *Coordinator) State() State { return c.state }

// Put / Delete 供使用者在协调器上发起源写入；Running 时同原子步双写到 dest。
func (c *Coordinator) Put(id, body string) (int64, error) {
	return c.src.Put(id, body)
}

func (c *Coordinator) Delete(id string) (int64, error) {
	return c.src.Delete(id)
}

func (c *Coordinator) Start(B int) error {
	if B < 1 || B > 1000 {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != Idle || len(c.dst.Records()) != 0 {
		return ErrState
	}
	c.B = B
	c.snap = c.src.Snapshot()
	c.scanned = 0
	c.state = Running
	return nil
}

func (c *Coordinator) Step() (applied, conflicts, incompat int, done bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != Running {
		return 0, 0, 0, false, ErrState
	}
	if c.scanned >= len(c.snap) {
		return 0, 0, 0, true, nil
	}
	end := c.scanned + c.B
	if end > len(c.snap) {
		end = len(c.snap)
	}
	batch := c.snap[c.scanned:end]
	for _, doc := range batch {
		a, inc := c.dst.Index(doc.ID, doc.Body, doc.Seq)
		switch {
		case a && inc:
			incompat++
		case a:
			applied++
		default:
			conflicts++
		}
	}
	c.scanned = end
	return applied, conflicts, incompat, c.scanned >= len(c.snap), nil
}

func (c *Coordinator) Cutover(tol int) error {
	if tol < 0 || tol > 1_000_000 {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != Running {
		return ErrState
	}
	if c.scanned < len(c.snap) {
		return ErrNotFinished
	}
	if len(c.dst.Failed()) > tol {
		return ErrTolerance
	}
	c.dst.PurgeTombstones()
	c.src.MarkSwitched()
	c.state = Switched
	return nil
}

func (c *Coordinator) Abort() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != Running {
		return ErrState
	}
	c.dst.Clear()
	c.snap = nil
	c.scanned = 0
	c.B = 0
	c.state = Idle
	return nil
}

// Scanned 返回未导出语义的已扫描条目数（测试证明用）。
func (c *Coordinator) Scanned() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.scanned
}
