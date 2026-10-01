package registry

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// naiveModel 是按需求文字逐句写成的“朴素逐步模拟”，与生产实现完全独立：
// 不用互斥锁（单线程重放），状态结构也重新定义。
type naiveModel struct {
	conns     map[string]naiveConn
	pending   map[string]naivePending
	published []Publish
	lastNow   int64
	seq       uint64
}

type naiveConn struct {
	k      int64
	active int64
	will   *Will
}

type naivePending struct {
	topic   string
	payload string
	dueAt   int64
	seq     uint64
}

func newNaiveModel() *naiveModel {
	return &naiveModel{conns: map[string]naiveConn{}, pending: map[string]naivePending{}}
}

func timeNowSeed() int64 { return time.Now().UnixNano() }

type naiveOp struct {
	kind     string
	id       string
	k        int64
	will     *Will
	normal   bool
	now      int64
	wantCode ErrorCode
}

// run 逐步执行一个操作，返回（拒绝码、本次入口处理+操作产生的发布列表）。
func (m *naiveModel) run(op naiveOp) (ErrorCode, []Publish) {
	// 1. 时钟倒退优先于一切参数检查。
	if op.now < m.lastNow {
		return ErrClockWentBack, nil
	}
	// 2. 参数非法（顺序：空 id、K 负、D 负）。
	if op.id == "" {
		return ErrEmptyClientID, nil
	}
	if op.kind == "connect" {
		if op.k < 0 {
			return ErrNegativeKeepAlive, nil
		}
		if op.will != nil && op.will.DelayMillis < 0 {
			return ErrNegativeDelay, nil
		}
	}

	// 3. 入口处理：先按 id 升序判定全部保活超时。
	m.lastNow = op.now
	ids := make([]string, 0, len(m.conns))
	for id := range m.conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := m.conns[id]
		if c.k > 0 && op.now-c.active > 1500*c.k {
			delete(m.conns, id)
			m.seq++
			seq := m.seq
			if c.will != nil {
				m.pending[id] = naivePending{
					topic:   c.will.Topic,
					payload: c.will.Payload,
					dueAt:   op.now + c.will.DelayMillis,
					seq:     seq,
				}
			}
		}
	}
	// 4. 入口处理第二阶段：发布所有 dueAt <= now 的遗嘱，按（dueAt, seq）升序。
	due := make([]string, 0)
	for id, pw := range m.pending {
		if pw.dueAt <= op.now {
			due = append(due, id)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := m.pending[due[i]], m.pending[due[j]]
		if a.dueAt != b.dueAt {
			return a.dueAt < b.dueAt
		}
		return a.seq < b.seq
	})
	published := make([]Publish, 0)
	for _, id := range due {
		pw := m.pending[id]
		rec := Publish{Client: id, Topic: pw.topic, Payload: pw.payload, PublishedAt: op.now}
		delete(m.pending, id)
		m.published = append(m.published, rec)
		published = append(published, rec)
	}

	// 5. 操作自身（活动/断开的在线检查在入口处理之后）。
	switch op.kind {
	case "connect":
		delete(m.conns, op.id)   // 接管：旧连接立即替换、旧遗嘱作废
		delete(m.pending, op.id) // 接管：等待中的遗嘱一并取消
		m.conns[op.id] = naiveConn{k: op.k, active: op.now, will: op.will}
	case "active":
		if _, ok := m.conns[op.id]; !ok {
			return ErrClientNotOnline, published
		}
		c := m.conns[op.id]
		c.active = op.now
		m.conns[op.id] = c
	case "disconnect":
		c, ok := m.conns[op.id]
		if !ok {
			return ErrClientNotOnline, published
		}
		delete(m.conns, op.id)
		if op.normal {
			// 正常断开：遗嘱作废
			break
		}
		m.seq++
		seq := m.seq
		if c.will != nil {
			m.pending[op.id] = naivePending{
				topic:   c.will.Topic,
				payload: c.will.Payload,
				dueAt:   op.now + c.will.DelayMillis,
				seq:     seq,
			}
		}
	case "advance":
		// 入口处理已完成全部工作。
	}
	return 0, published
}

// TestAgainstNaiveModel：随机生成的调用序列在真实实现与朴素模型上逐步对照。
func TestAgainstNaiveModel(t *testing.T) {
	seed := timeNowSeed()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("naive model differential seed=%d", seed)

	for iter := 0; iter < 200; iter++ {
		real := New(DiscardLogger{})
		naive := newNaiveModel()
		now := int64(1000)
		pool := []string{"a", "b", "c"}

		for step := 0; step < 120; step++ {
			// 时间只在约 8% 的步骤倒退，以覆盖“整体拒绝且不改状态”分支。
			op := naiveOp{}
			if rng.Intn(12) == 0 {
				now -= int64(rng.Intn(5) + 1)
			} else {
				now += int64(rng.Intn(1200))
			}
			op.now = now

			switch rng.Intn(6) {
			case 0, 1:
				op.kind = "connect"
				op.id = pool[rng.Intn(len(pool))]
				op.k = int64(rng.Intn(3)) // 0..2
				if rng.Intn(5) != 0 {
					op.will = &Will{
						Topic:       "topic/" + op.id,
						Payload:     fmt.Sprintf("payload-%d", rng.Intn(4)),
						DelayMillis: int64(rng.Intn(2500)),
					}
				}
			case 2:
				op.kind = "active"
				if idx := rng.Intn(len(pool) + 1); idx < len(pool) {
					op.id = pool[idx]
				}
				// 否则保持空 id，覆盖 ErrEmptyClientID。
			case 3:
				op.kind = "disconnect"
				op.id = pool[rng.Intn(len(pool))]
				op.normal = rng.Intn(2) == 0
			case 4:
				op.kind = "advance"
				op.id = "advance"
			case 5:
				// 故意非法参数：空 id / 负 K / 负 D。
				op.kind = "connect"
				switch rng.Intn(3) {
				case 0:
					op.id = ""
					op.k = int64(rng.Intn(2))
				case 1:
					op.id = pool[rng.Intn(len(pool))]
					op.k = -1
				case 2:
					op.id = pool[rng.Intn(len(pool))]
					op.k = 0
					op.will = &Will{Topic: "t", DelayMillis: -1}
				}
			}

			var realCode ErrorCode
			before := len(real.Published())
			switch op.kind {
			case "connect":
				realCode = errCode(real.Connect(op.id, op.k, op.will, op.now))
			case "active":
				realCode = errCode(real.Active(op.id, op.now))
			case "disconnect":
				realCode = errCode(real.Disconnect(op.id, op.normal, op.now))
			case "advance":
				_, err := real.Advance(op.now)
				realCode = errCode(err)
			}
			realPublished := real.Published()[before:]

			naiveCode, naivePublished := naive.run(op)
			if realCode != naiveCode {
				t.Fatalf("iter=%d step=%d op=%+v code mismatch real=%d naive=%d real.lastNow=%d naive.lastNow=%d seed=%d",
					iter, step, op, realCode, naiveCode, real.lastNow, naive.lastNow, seed)
			}
			if realCode == 0 {
				if !publishesEqual(realPublished, naivePublished) {
					t.Fatalf("iter=%d step=%d op=%+v step-publish mismatch\nreal=%+v\nnaive=%+v\nseed=%d",
						iter, step, op, realPublished, naivePublished, seed)
				}
			}
			if !publishesEqual(real.Published(), naive.published) {
				t.Fatalf("iter=%d step=%d op=%+v full publish log mismatch\nreal=%+v\nnaive=%+v\nseed=%d",
					iter, step, op, real.Published(), naive.published, seed)
			}
		}
	}
}

func publishesEqual(a, b []Publish) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
