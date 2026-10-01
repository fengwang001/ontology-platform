package queue

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// model 是按规则逐条直译的朴素模拟，用作被测队列的对照。
// 每个操作返回判定依据日志，便于比对失败时定位分歧。
type model struct {
	cap    int
	policy Policy
	items  []modelEntry
	dead   []DeadLetter
	maxNow int64
	seen   bool
}

type modelEntry struct {
	msg      any
	expireAt int64
}

func newModel(capacity int, policy Policy) *model {
	return &model{cap: capacity, policy: policy}
}

// headExpired 从队首起连续统计已过期（到期时刻 <= now）的消息。
func (m *model) headExpired(now int64) int {
	n := 0
	for n < len(m.items) && m.items[n].expireAt <= now {
		n++
	}
	return n
}

func (m *model) commitCleanup(n int) {
	for _, e := range m.items[:n] {
		m.dead = append(m.dead, DeadLetter{Message: e.msg, Reason: ReasonExpired})
	}
	m.items = m.items[n:]
}

func (m *model) enqueue(msg any, ttl, now int64) (error, string) {
	if m.seen && now < m.maxNow {
		return ErrClockBackward, fmt.Sprintf("now=%d < 已见最大 now=%d，时钟倒退优先", now, m.maxNow)
	}
	if ttl < 0 {
		return ErrNegativeTTL, fmt.Sprintf("ttl=%d 为负，拒绝", ttl)
	}
	expired := m.headExpired(now)
	basis := fmt.Sprintf("队首连续过期 %d 条，清理后长度 %d，容量 %d", expired, len(m.items)-expired, m.cap)
	if len(m.items)-expired >= m.cap && m.policy == Reject {
		return ErrQueueFull, basis + "，拒绝策略且清理后仍满，清理不落地"
	}
	m.commitCleanup(expired)
	if len(m.items) >= m.cap {
		m.dead = append(m.dead, DeadLetter{Message: m.items[0].msg, Reason: ReasonOverflow})
		basis += fmt.Sprintf("，仍满，丢弃队首 %v（溢出）", m.items[0].msg)
		m.items = m.items[1:]
	}
	m.items = append(m.items, modelEntry{msg: msg, expireAt: now + ttl})
	m.maxNow, m.seen = now, true
	return nil, basis + "，入队成功"
}

func (m *model) dequeue(now int64) (any, error, string) {
	if m.seen && now < m.maxNow {
		return nil, ErrClockBackward, fmt.Sprintf("now=%d < 已见最大 now=%d，时钟倒退优先", now, m.maxNow)
	}
	expired := m.headExpired(now)
	basis := fmt.Sprintf("队首连续过期 %d 条，清理后长度 %d", expired, len(m.items)-expired)
	if len(m.items)-expired == 0 {
		return nil, ErrEmptyQueue, basis + "，清理后队空，清理不落地"
	}
	m.commitCleanup(expired)
	head := m.items[0]
	m.items = m.items[1:]
	m.maxNow, m.seen = now, true
	return head.msg, nil, basis + fmt.Sprintf("，出队 %v", head.msg)
}

// TestModelComparison 用固定种子的随机操作序列对照朴素模拟，
// 每步打印输入、输出与判定依据。
func TestModelComparison(t *testing.T) {
	for _, policy := range []Policy{DropHead, Reject} {
		policy := policy
		name := "DropHead"
		if policy == Reject {
			name = "Reject"
		}
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(20261002))
			q := mustNew(t, 3, policy)
			m := newModel(3, policy)
			now := int64(0)
			seq := 0
			for step := 0; step < 3000; step++ {
				// 时钟单调前进，偶尔停滞；偶发倒退与负 ttl 以覆盖拒绝路径。
				now += int64(rng.Intn(4))
				if rng.Intn(50) == 0 {
					now -= int64(rng.Intn(10))
				}
				ttl := int64(rng.Intn(8))
				if rng.Intn(50) == 0 {
					ttl = -1
				}
				if rng.Intn(2) == 0 {
					msg := seq
					seq++
					gotErr := q.Enqueue(msg, ttl, now)
					wantErr, basis := m.enqueue(msg, ttl, now)
					t.Logf("step=%d Enqueue(msg=%d, ttl=%d, now=%d) -> err=%v | 判定依据: %s",
						step, msg, ttl, now, gotErr, basis)
					if gotErr != wantErr {
						t.Fatalf("step=%d 入队错误不一致: 队列=%v 模型=%v", step, gotErr, wantErr)
					}
				} else {
					gotMsg, gotErr := q.Dequeue(now)
					wantMsg, wantErr, basis := m.dequeue(now)
					t.Logf("step=%d Dequeue(now=%d) -> msg=%v err=%v | 判定依据: %s",
						step, now, gotMsg, gotErr, basis)
					if gotErr != wantErr || gotMsg != wantMsg {
						t.Fatalf("step=%d 出队不一致: 队列=(%v,%v) 模型=(%v,%v)",
							step, gotMsg, gotErr, wantMsg, wantErr)
					}
				}
				if q.Len() != len(m.items) {
					t.Fatalf("step=%d 长度不一致: 队列=%d 模型=%d", step, q.Len(), len(m.items))
				}
				gotDead, wantDead := q.DeadLetters(), m.dead
				if len(gotDead) != len(wantDead) {
					t.Fatalf("step=%d 死信条数不一致: 队列=%d 模型=%d", step, len(gotDead), len(wantDead))
				}
				for i := range gotDead {
					if gotDead[i] != wantDead[i] {
						t.Fatalf("step=%d 死信[%d] 不一致: 队列=%v 模型=%v", step, i, gotDead[i], wantDead[i])
					}
				}
			}
			t.Logf("对照完成：3000 步，最终长度=%d 死信=%d 条", q.Len(), len(m.dead))
		})
	}
}

// TestConcurrent 并发调用入队、出队与查询，验证结果等价于某个
// 串行顺序：每条成功入队的消息最终恰好处于在队中、已出队、
// 死信三者之一，且长度始终不超过容量。
func TestConcurrent(t *testing.T) {
	const capacity = 8
	q := mustNew(t, capacity, DropHead)

	var now atomic.Int64
	var nextID atomic.Int64
	var enqueued atomic.Int64
	var mu sync.Mutex
	dequeued := map[int64]bool{}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				switch i % 3 {
				case 0:
					id := nextID.Add(1)
					if err := q.Enqueue(id, int64(i%7), now.Load()); err == nil {
						enqueued.Add(1)
					}
				case 1:
					if msg, err := q.Dequeue(now.Load()); err == nil {
						mu.Lock()
						dequeued[msg.(int64)] = true
						mu.Unlock()
					}
				default:
					if q.Len() > capacity {
						t.Errorf("长度 %d 超过容量 %d", q.Len(), capacity)
					}
					_ = q.DeadLetters()
				}
			}
		}()
	}
	// 单调推进的时钟，避免时钟倒退干扰并发场景。
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				now.Add(1)
			}
		}
	}()
	wg.Wait()
	close(done)

	// 守恒：成功入队数 = 已出队 + 死信 + 仍在队中。
	dead := q.DeadLetters()
	total := int(enqueued.Load())
	inQueue := q.Len()
	mu.Lock()
	out := len(dequeued)
	mu.Unlock()
	if out+len(dead)+inQueue != total {
		t.Fatalf("守恒破坏: 出队 %d + 死信 %d + 在队 %d != 入队 %d",
			out, len(dead), inQueue, total)
	}
	// 无消息同时出现在出队与死信中。
	mu.Lock()
	for _, d := range dead {
		if dequeued[d.Message.(int64)] {
			t.Fatalf("消息 %v 既已出队又进入死信", d.Message)
		}
	}
	mu.Unlock()
	t.Logf("并发守恒成立: 入队 %d = 出队 %d + 死信 %d + 在队 %d",
		total, out, len(dead), inQueue)
}
