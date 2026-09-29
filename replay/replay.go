package replay

import "sync"

// Config 描述限速回放器的静态参数。
//
// 所有字段都必须为正数，否则 New 返回 ErrInvalidConfig。
type Config struct {
	BaseRate    int64 // 基准补充速率（令牌/时间单位），队列空时使用
	CatchUpRate int64 // 追赶补充速率（令牌/时间单位），队列有积压时使用
	Capacity    int64 // 令牌桶容量（突发上限），补充后令牌不得超过该值
	MaxQueue    int   // 待回放事件队列上限
}

// Replayer 是基于整数令牌桶与逻辑时钟的限速回放器。
//
// 逻辑时钟只随 Enqueue/Drain 的调用单调推进；令牌为整数，按推进前队列
// 是否有积压选择追赶速率或基准速率补充，并在补充后以桶容量封顶。事件
// 严格按 FIFO 顺序吐出，每吐出一个事件扣减一个令牌。
type Replayer[T any] struct {
	mu sync.RWMutex

	cfg    Config
	now    int64
	tokens int64
	queue  []T

	totalEnqueued int64
	totalDrained  int64
}

// State 是回放器某一时刻的一致性快照。
type State struct {
	Now           int64 // 当前逻辑时钟
	Tokens        int64 // 当前令牌数
	QueueLen      int   // 当前队列长度
	TotalEnqueued int64 // 累计成功入队事件数
	TotalDrained  int64 // 累计成功吐出事件数
}

// New 创建回放器，初始时钟为 0，令牌桶为满（Capacity 个令牌）。
// 参数不合法时返回 ErrInvalidConfig，不产生任何副作用。
func New[T any](cfg Config) (*Replayer[T], error) {
	if cfg.BaseRate <= 0 || cfg.CatchUpRate <= 0 || cfg.Capacity <= 0 || cfg.MaxQueue <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Replayer[T]{
		cfg:    cfg,
		tokens: cfg.Capacity,
		queue:  make([]T, 0, cfg.MaxQueue),
	}, nil
}

// Enqueue 将事件按 FIFO 顺序放入队列，并把逻辑时钟推进到 now。
//
// 时钟回退返回 ErrClockBackwards；队列已满返回 ErrQueueFull。
// 任一校验失败都整体拒绝，时钟、令牌与队列保持调用前状态。
func (r *Replayer[T]) Enqueue(now int64, event T) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.now {
		return ErrClockBackwards
	}
	if len(r.queue) >= r.cfg.MaxQueue {
		return ErrQueueFull
	}
	r.advanceLocked(now)
	r.queue = append(r.queue, event)
	r.totalEnqueued++
	return nil
}

// Drain 把逻辑时钟推进到 now，然后按入队顺序吐出令牌允许数量的事件：
// 每吐出一个事件扣一个令牌，直到令牌耗尽或队列清空。
//
// 时钟回退返回 ErrClockBackwards，且不改变任何状态。
// 没有可吐出的事件时返回 nil（长度为 0）。
func (r *Replayer[T]) Drain(now int64) ([]T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.now {
		return nil, ErrClockBackwards
	}
	r.advanceLocked(now)

	n := int(r.tokens)
	if n > len(r.queue) {
		n = len(r.queue)
	}
	if n == 0 {
		return nil, nil
	}

	out := make([]T, n)
	copy(out, r.queue[:n])

	var zero T
	for i := 0; i < n; i++ {
		r.queue[i] = zero // 避免持有已吐出事件的引用
	}
	r.queue = r.queue[n:]
	r.tokens -= int64(n)
	r.totalDrained += int64(n)
	return out, nil
}

// Snapshot 在同一把读锁下原子读取时钟、令牌、队列长度与累计计数，
// 保证并发读看到的各字段来自同一时刻、逐字段一致。
func (r *Replayer[T]) Snapshot() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return State{
		Now:           r.now,
		Tokens:        r.tokens,
		QueueLen:      len(r.queue),
		TotalEnqueued: r.totalEnqueued,
		TotalDrained:  r.totalDrained,
	}
}

// SelfCheck 校验内部不变量：
//   - 令牌数位于 [0, Capacity]；
//   - 队列长度位于 [0, MaxQueue]；
//   - 累计入队数 == 累计吐出数 + 当前队列长度。
func (r *Replayer[T]) SelfCheck() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.tokens < 0 || r.tokens > r.cfg.Capacity {
		return ErrInvalidConfig
	}
	if len(r.queue) < 0 || len(r.queue) > r.cfg.MaxQueue {
		return ErrQueueFull
	}
	if r.totalEnqueued != r.totalDrained+int64(len(r.queue)) {
		return ErrInvalidConfig
	}
	return nil
}

// advanceLocked 将时钟推进到 now，并按推进前队列是否有积压选择速率补充
// 整数令牌，补充后以桶容量封顶。调用方必须持有写锁。
func (r *Replayer[T]) advanceLocked(now int64) {
	dt := now - r.now
	if dt < 0 {
		dt = 0
	}
	rate := r.cfg.BaseRate
	if len(r.queue) > 0 {
		rate = r.cfg.CatchUpRate
	}

	if dt > 0 && rate > 0 && r.tokens < r.cfg.Capacity {
		// 距离填满还需多少令牌；若 dt*rate 已足够则直接封顶，
		// 同时用该比较规避大数相乘的整数溢出。
		missing := r.cfg.Capacity - r.tokens
		stepsToFull := (missing + rate - 1) / rate
		if dt >= stepsToFull {
			r.tokens = r.cfg.Capacity
		} else {
			r.tokens += dt * rate
		}
	}
	r.now = now
}
