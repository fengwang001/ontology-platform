// Package groupcommit 提供批量写入的组提交器：把多个并发写请求合并成批次
// 一次性持久化，并把每条请求的结果准确送回各自调用方。
//
// 语义概述：
//   - 同一时刻至多一个批次在持久化；待写请求按进入队列的顺序排队。
//   - 上一批结束后立即从队首起组新批，直到达到条数上限或再加一条将超过
//     字节上限为止。
//   - 序号在组批完成、交给持久化之前按队列顺序连续分配。
//   - 持久化成功则整批成功并各自返回序号；失败则整批以同一原因失败，
//     序号回收，下一批从回收处继续分配，保证已持久化的序号从 1 起连续、
//     无空洞、无重号。
//   - 空负载、单条超过字节上限、关闭后提交、创建参数非正都会立即被拒绝，
//     不占用序号也不进入队列。
//   - Close 会等待所有已入队请求得到结果后才返回。
package groupcommit

import (
	"errors"
	"log"
	"sync"
)

var (
	// ErrEmptyPayload 表示负载为空。
	ErrEmptyPayload = errors.New("groupcommit: empty payload")
	// ErrPayloadTooLarge 表示单条负载超过批次字节上限。
	ErrPayloadTooLarge = errors.New("groupcommit: payload exceeds max batch bytes")
	// ErrClosed 表示提交器已关闭。
	ErrClosed = errors.New("groupcommit: committer is closed")
	// ErrInvalidMaxItems 表示条数上限非正。
	ErrInvalidMaxItems = errors.New("groupcommit: MaxItems must be positive")
	// ErrInvalidMaxBytes 表示字节上限非正。
	ErrInvalidMaxBytes = errors.New("groupcommit: MaxBytes must be positive")
	// ErrNilPersister 表示持久化函数为空。
	ErrNilPersister = errors.New("groupcommit: persister must not be nil")
)

// PersistFunc 把一个批次整体持久化。返回 nil 表示整批成功，
// 返回非 nil 错误表示整批失败（全部未持久化）。
// 实现不得在返回后继续持有 batch 切片。
type PersistFunc func(batch [][]byte) error

// Config 是提交器的创建参数。
type Config struct {
	// MaxItems 是单批最大条数，必须为正。
	MaxItems int
	// MaxBytes 是单批最大字节数（各条负载长度之和），必须为正。
	MaxBytes int
	// Logger 可选，用于打印组批与持久化日志。
	Logger *log.Logger
}

// Result 是一条写请求的结果。
type Result struct {
	// Seq 是持久化成功后分配的序号（从 1 起连续）；失败时为 0。
	Seq uint64
	// Err 是失败原因；成功时为 nil。
	Err error
}

type request struct {
	payload []byte
	res     chan Result
}

// Committer 是组提交器。使用完毕后必须调用 Close。
type Committer struct {
	persist  PersistFunc
	maxItems int
	maxBytes int
	logger   *log.Logger

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []*request
	nextSeq uint64
	closed  bool

	wg sync.WaitGroup
}

// NewCommitter 创建并启动一个组提交器。参数非正或持久化函数为空时
// 返回可区分的错误，不占用任何资源。
func NewCommitter(cfg Config, persist PersistFunc) (*Committer, error) {
	if cfg.MaxItems <= 0 {
		return nil, ErrInvalidMaxItems
	}
	if cfg.MaxBytes <= 0 {
		return nil, ErrInvalidMaxBytes
	}
	if persist == nil {
		return nil, ErrNilPersister
	}
	c := &Committer{
		persist:  persist,
		maxItems: cfg.MaxItems,
		maxBytes: cfg.MaxBytes,
		logger:   cfg.Logger,
	}
	c.cond = sync.NewCond(&c.mu)
	c.start()
	return c, nil
}

// start 启动调度协程。与 NewCommitter 分离以便测试在启动前预置队列。
func (c *Committer) start() {
	c.wg.Add(1)
	go c.run()
}

// Write 提交一条负载并阻塞等待结果。成功返回序号，失败返回错误。
// 空负载、超限负载、关闭后提交都会被立即拒绝，不占用序号也不进入队列。
func (c *Committer) Write(payload []byte) (uint64, error) {
	if len(payload) == 0 {
		return 0, ErrEmptyPayload
	}
	if len(payload) > c.maxBytes {
		return 0, ErrPayloadTooLarge
	}
	req := &request{payload: payload, res: make(chan Result, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, ErrClosed
	}
	c.queue = append(c.queue, req)
	c.cond.Signal()
	c.mu.Unlock()

	r := <-req.res
	return r.Seq, r.Err
}

// Close 关闭提交器：不再接受新请求，等待所有已入队请求得到结果后返回。
// 重复调用是安全的。
func (c *Committer) Close() {
	c.mu.Lock()
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	c.wg.Wait()
}

// run 是唯一的调度协程，保证同一时刻至多一个批次在持久化。
func (c *Committer) run() {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		for len(c.queue) == 0 && !c.closed {
			c.cond.Wait()
		}
		if len(c.queue) == 0 {
			c.mu.Unlock()
			return
		}
		batch, seqs := c.formBatchLocked()
		c.mu.Unlock()

		payloads := make([][]byte, len(batch))
		for i, r := range batch {
			payloads[i] = r.payload
		}
		c.logf("组批完成: 条数=%d 序号=[%d..%d]，开始持久化", len(batch), seqs[0], seqs[len(seqs)-1])
		err := c.persist(payloads)
		if err != nil {
			c.mu.Lock()
			c.nextSeq -= uint64(len(batch))
			c.mu.Unlock()
			for _, r := range batch {
				r.res <- Result{Err: err}
			}
			c.logf("批次持久化失败: 条数=%d 原因=%v，序号已回收", len(batch), err)
			continue
		}
		for i, r := range batch {
			r.res <- Result{Seq: seqs[i]}
		}
		c.logf("批次持久化成功: 条数=%d 序号=[%d..%d]", len(batch), seqs[0], seqs[len(seqs)-1])
	}
}

// formBatchLocked 从队首起组批：直到达到条数上限或再加一条将超过字节上限，
// 并在组批完成时按队列顺序连续分配序号。调用时必须持有 c.mu。
func (c *Committer) formBatchLocked() ([]*request, []uint64) {
	n := 0
	bytes := 0
	for n < len(c.queue) && n < c.maxItems {
		size := len(c.queue[n].payload)
		if n > 0 && bytes+size > c.maxBytes {
			break
		}
		bytes += size
		n++
	}
	batch := make([]*request, n)
	copy(batch, c.queue[:n])
	c.queue = c.queue[n:]

	seqs := make([]uint64, n)
	for i := range seqs {
		c.nextSeq++
		seqs[i] = c.nextSeq
	}
	return batch, seqs
}

func (c *Committer) logf(format string, args ...any) {
	if c.logger != nil {
		c.logger.Printf(format, args...)
	}
}
