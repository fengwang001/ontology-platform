package gateway

import (
	"sync"
	"sync/atomic"
	"time"

	"ontology/digest"
	"ontology/record"
)

// Gateway 是带幂等键的写入网关：管理多个键，串起执行、回放与清理。
type Gateway struct {
	now    func() time.Time
	ttl    time.Duration
	onJoin func(key string)
	mu     sync.Mutex
	store  map[string]*record.Record

	execCalls atomic.Int64
}

// New 按注入时钟与存活时长构造网关。
func New(cfg Config) *Gateway {
	if cfg.Now == nil {
		panic("gateway: Config.Now is required")
	}
	if cfg.TTL <= 0 {
		panic("gateway: Config.TTL must be positive")
	}
	return &Gateway{
		now:    cfg.Now,
		ttl:    cfg.TTL,
		onJoin: cfg.OnJoin,
		store:  make(map[string]*record.Record),
	}
}

// ExecCalls 返回执行函数被真正调用的累计次数，供测试核对。
func (g *Gateway) ExecCalls() int64 { return g.execCalls.Load() }

// Submit 提交一次写入。语义见包级说明：
// 同键同体恰好执行一次并可回放；同键异体立刻冲突；
// 失败同体可重试；执行中的并发提交合流等待同一结果。
func (g *Gateway) Submit(key string, body []byte, exec ExecFunc) Outcome {
	fp := digest.Of(body)

	g.mu.Lock()
	rec, ok := g.store[key]
	switch {
	case !ok || rec.ExpiredAt(g.now()):
		rec = record.New(fp, g.now(), g.ttl)
		g.store[key] = rec
		g.mu.Unlock()
		return g.run(key, rec, body, exec)

	case !rec.Fingerprint().Equal(fp):
		g.mu.Unlock()
		return conflictOutcome(key, rec)

	case rec.State() == record.Failed:
		rec.Retry(fp, g.now(), g.ttl)
		g.mu.Unlock()
		return g.run(key, rec, body, exec)

	default:
		// Running：后来者合流等待；Succeeded：直接回放。
		if rec.State() == record.Running && g.onJoin != nil {
			g.onJoin(key)
		}
		g.mu.Unlock()
		<-rec.Done()
		return replayOutcome(rec)
	}
}

// run 在不持有网关锁的情况下真正执行，避免长耗时函数卡住其他键。
func (g *Gateway) run(key string, rec *record.Record, body []byte, exec ExecFunc) Outcome {
	g.execCalls.Add(1)
	result, err := exec(body)

	g.mu.Lock()
	if err != nil {
		rec.Fail(err)
	} else {
		rec.Succeed(result)
	}
	g.mu.Unlock()

	return Outcome{Result: rec.Result(), Err: rec.Err(), Replayed: false, State: rec.State()}
}
