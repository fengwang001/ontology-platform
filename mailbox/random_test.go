package mailbox_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/mailbox"
)

// modelBox 是按题目规则逐步写成的朴素模拟，用于与真实实现对拍。
// 它只使用切片与线性扫描，每条分支直接对应规则条文。
type modelBox struct {
	K, P, L   int
	seq       int64
	maxNow    int64
	msgs      []modelMsg
	hasMark   bool
	markN     int64
	markSeq   int64
	drainedN  int64 // 已被取走标记返回的 n 之和
	discarded int64 // 计入标记的丢弃总数
	note      string
}

type modelMsg struct {
	id, ck string
	prio   int
	exp    int64
	seq    int64
}

func newModel(k, p, l int) *modelBox {
	return &modelBox{K: k, P: p, L: l}
}

func (m *modelBox) purge(now int64) {
	kept := m.msgs[:0]
	for _, msg := range m.msgs {
		if msg.exp > now {
			kept = append(kept, msg)
		}
	}
	m.msgs = kept
}

func (m *modelBox) plainCount() int {
	n := 0
	for _, msg := range m.msgs {
		if msg.ck == "" {
			n++
		}
	}
	return n
}

func (m *modelBox) ckKinds() int {
	n := 0
	for _, msg := range m.msgs {
		if msg.ck != "" {
			n++
		}
	}
	return n
}

func (m *modelBox) bump(c int64) {
	m.seq++
	if m.hasMark {
		m.markN += c
	} else {
		m.hasMark = true
		m.markN = c
	}
	m.markSeq = m.seq
	m.discarded += c
}

func (m *modelBox) enqueue(id, ck string, prio int, ttl, now int64) (mailbox.Result, error) {
	m.note = ""
	if id == "" || len(ck) > mailbox.MaxCKLen || (prio != 0 && prio != 1) ||
		ttl < 0 || ttl > mailbox.MaxTTL || now < 0 || now > mailbox.MaxNow {
		m.note = "参数非法"
		return 0, mailbox.ErrInvalidParam
	}
	if now < m.maxNow {
		m.note = fmt.Sprintf("时钟回退 now=%d < maxNow=%d", now, m.maxNow)
		return 0, mailbox.ErrClockRollback
	}
	for _, msg := range m.msgs {
		if msg.id == id && msg.exp > now {
			m.note = "与存活消息 id 重复"
			return 0, mailbox.ErrDuplicateID
		}
	}
	m.maxNow = now
	m.purge(now)
	if ttl == 0 {
		m.note = "ttl 为 0，不存放"
		return mailbox.Dropped, nil
	}
	exp := now + ttl
	if ck != "" {
		for i, msg := range m.msgs {
			if msg.ck == ck {
				m.msgs = append(m.msgs[:i], m.msgs[i+1:]...)
				m.seq++
				m.msgs = append(m.msgs, modelMsg{id, ck, prio, exp, m.seq})
				m.note = fmt.Sprintf("同 ck=%q 替换，新序号 %d", ck, m.seq)
				return mailbox.Collapsed, nil
			}
		}
		if m.ckKinds() == m.K {
			c := int64(m.ckKinds() + 1)
			kept := m.msgs[:0]
			for _, msg := range m.msgs {
				if msg.ck == "" {
					kept = append(kept, msg)
				}
			}
			m.msgs = kept
			m.bump(c)
			m.note = fmt.Sprintf("折叠溢出 c=%d，标记 n=%d", c, m.markN)
			return mailbox.Overflow, nil
		}
	} else if m.plainCount() == m.P {
		c := int64(m.plainCount() + 1)
		kept := m.msgs[:0]
		for _, msg := range m.msgs {
			if msg.ck != "" {
				kept = append(kept, msg)
			}
		}
		m.msgs = kept
		m.bump(c)
		m.note = fmt.Sprintf("不可折叠溢出 c=%d，标记 n=%d", c, m.markN)
		return mailbox.Overflow, nil
	}
	m.seq++
	fresh := modelMsg{id, ck, prio, exp, m.seq}
	m.msgs = append(m.msgs, fresh)
	if len(m.msgs) <= m.L {
		m.note = fmt.Sprintf("存入，序号 %d", m.seq)
		return mailbox.Stored, nil
	}
	// 淘汰：普通类序号最小者，否则高类序号最小者。
	victim := -1
	for i, msg := range m.msgs {
		if msg.prio == 0 && (victim < 0 || msg.seq < m.msgs[victim].seq) {
			victim = i
		}
	}
	if victim < 0 {
		for i := range m.msgs {
			if victim < 0 || m.msgs[i].seq < m.msgs[victim].seq {
				victim = i
			}
		}
	}
	self := m.msgs[victim].seq == fresh.seq
	victimSeq := m.msgs[victim].seq
	m.msgs = append(m.msgs[:victim], m.msgs[victim+1:]...)
	m.bump(1)
	if self {
		m.note = "新消息自身被淘汰"
		return mailbox.Evicted, nil
	}
	m.note = fmt.Sprintf("存入后淘汰 seq=%d", victimSeq)
	return mailbox.Stored, nil
}

// orderedItems 按出队顺序构造条目；liveOnly 时只含 exp > now 的存活消息。
func (m *modelBox) orderedItems(now int64, liveOnly bool) []mailbox.Item {
	var high, normal []mailbox.Item
	for _, msg := range m.msgs {
		if liveOnly && msg.exp <= now {
			continue
		}
		it := mailbox.Item{ID: msg.id, CK: msg.ck, Prio: msg.prio, Exp: msg.exp, Seq: uint64(msg.seq)}
		if msg.prio == 1 {
			high = append(high, it)
		} else {
			normal = append(normal, it)
		}
	}
	if m.hasMark {
		high = append(high, mailbox.Item{Marker: true, N: m.markN, Seq: uint64(m.markSeq)})
	}
	bySeq := func(items []mailbox.Item) {
		sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	}
	bySeq(high)
	bySeq(normal)
	return append(high, normal...)
}

func (m *modelBox) drain(now int64, cnt int) ([]mailbox.Item, error) {
	m.note = ""
	if cnt < 1 || cnt > mailbox.MaxCnt || now < 0 || now > mailbox.MaxNow {
		m.note = "参数非法"
		return nil, mailbox.ErrInvalidParam
	}
	if now < m.maxNow {
		m.note = fmt.Sprintf("时钟回退 now=%d < maxNow=%d", now, m.maxNow)
		return nil, mailbox.ErrClockRollback
	}
	m.maxNow = now
	m.purge(now)
	items := m.orderedItems(now, false)
	if len(items) > cnt {
		items = items[:cnt]
	}
	for _, it := range items {
		if it.Marker {
			m.drainedN += m.markN
			m.hasMark = false
			m.markN = 0
			m.markSeq = 0
		} else {
			for i, msg := range m.msgs {
				if msg.id == it.ID {
					m.msgs = append(m.msgs[:i], m.msgs[i+1:]...)
					break
				}
			}
		}
	}
	m.note = fmt.Sprintf("取出 %d 项", len(items))
	return items, nil
}

func (m *modelBox) peek(now int64) ([]mailbox.Item, error) {
	m.note = ""
	if now < 0 || now > mailbox.MaxNow {
		m.note = "参数非法"
		return nil, mailbox.ErrInvalidParam
	}
	if now < m.maxNow {
		m.note = fmt.Sprintf("时钟回退 now=%d < maxNow=%d", now, m.maxNow)
		return nil, mailbox.ErrClockRollback
	}
	m.note = "只读返回存活项"
	return m.orderedItems(now, true), nil
}

// checkInvariants 验证题目要求的不变量。
func (m *modelBox) checkInvariants(t *testing.T) {
	t.Helper()
	if got := m.ckKinds(); got > m.K {
		t.Fatalf("invariant: ck kinds %d > K %d", got, m.K)
	}
	if got := m.plainCount(); got > m.P {
		t.Fatalf("invariant: plain %d > P %d", got, m.P)
	}
	if got := len(m.msgs); got > m.L {
		t.Fatalf("invariant: total %d > Lmax %d", got, m.L)
	}
	seen := map[string]bool{}
	for _, msg := range m.msgs {
		if msg.ck == "" {
			continue
		}
		if seen[msg.ck] {
			t.Fatalf("invariant: duplicate ck %q", msg.ck)
		}
		seen[msg.ck] = true
	}
	var markN int64
	if m.hasMark {
		markN = m.markN
	}
	if m.discarded != markN+m.drainedN {
		t.Fatalf("invariant: discarded %d != marker %d + drained %d",
			m.discarded, markN, m.drainedN)
	}
}

func sameErr(a, b error) bool {
	return a == b
}

func sameItems(a, b []mailbox.Item) bool {
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

// TestRandomAgainstModel 用 2000 组随机操作序列将真实实现与朴素模拟对拍，
// 日志打印每组输入、输出与判定依据。
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 2000
	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewSource(int64(s)))
		k := 1 + rng.Intn(4)
		p := 1 + rng.Intn(5)
		l := 1 + rng.Intn(8)
		real, err := mailbox.New(k, p, l)
		if err != nil {
			t.Fatalf("seq %d: New: %v", s, err)
		}
		model := newModel(k, p, l)
		ops := 20 + rng.Intn(20)
		t.Logf("序列 %d: K=%d P=%d Lmax=%d ops=%d", s, k, p, l, ops)

		var clock int64
		for op := 0; op < ops; op++ {
			switch rng.Intn(20) {
			case 0:
				clock -= int64(1 + rng.Intn(3)) // 可能触发时钟回退
			case 1:
				// 时钟不变
			default:
				clock += int64(rng.Intn(4))
			}
			if clock < 0 {
				clock = 0
			}
			now := clock
			if rng.Intn(50) == 0 {
				now = -1 // 非法 now
			}
			if rng.Intn(50) == 0 {
				now = mailbox.MaxNow + 1 // 非法 now
			}

			switch rng.Intn(10) {
			case 0, 1: // Drain
				cnt := 1 + rng.Intn(6)
				if rng.Intn(30) == 0 {
					cnt = 0
				}
				if rng.Intn(30) == 0 {
					cnt = mailbox.MaxCnt + 1
				}
				gotItems, gotErr := real.Drain(now, cnt)
				wantItems, wantErr := model.drain(now, cnt)
				t.Logf("  op%d Drain(now=%d,cnt=%d) -> %v err=%v | 依据: %s",
					op, now, cnt, keys(gotItems), gotErr, model.note)
				if !sameErr(gotErr, wantErr) {
					t.Fatalf("seq %d op %d Drain: err %v, want %v", s, op, gotErr, wantErr)
				}
				if !sameItems(gotItems, wantItems) {
					t.Fatalf("seq %d op %d Drain: %v, want %v",
						s, op, keys(gotItems), keys(wantItems))
				}
			case 2, 3: // Peek
				gotItems, gotErr := real.Peek(now)
				wantItems, wantErr := model.peek(now)
				t.Logf("  op%d Peek(now=%d) -> %v err=%v | 依据: %s",
					op, now, keys(gotItems), gotErr, model.note)
				if !sameErr(gotErr, wantErr) {
					t.Fatalf("seq %d op %d Peek: err %v, want %v", s, op, gotErr, wantErr)
				}
				if !sameItems(gotItems, wantItems) {
					t.Fatalf("seq %d op %d Peek: %v, want %v",
						s, op, keys(gotItems), keys(wantItems))
				}
			default: // Enqueue
				id := fmt.Sprintf("id%d", rng.Intn(8))
				if rng.Intn(40) == 0 {
					id = "" // 非法 id
				}
				ck := ""
				if rng.Intn(10) < 6 {
					ck = fmt.Sprintf("k%d", rng.Intn(4))
				}
				if rng.Intn(60) == 0 {
					ck = strings.Repeat("c", 65) // 非法 ck
				}
				prio := 0
				if rng.Intn(10) < 4 {
					prio = 1
				}
				if rng.Intn(60) == 0 {
					prio = 2 // 非法 prio
				}
				var ttl int64
				switch rng.Intn(20) {
				case 0:
					ttl = 0
				case 1:
					ttl = mailbox.MaxTTL
				case 2:
					ttl = -1 // 非法 ttl
				case 3:
					ttl = mailbox.MaxTTL + 1 // 非法 ttl
				default:
					ttl = int64(1 + rng.Intn(30))
				}
				gotRes, gotErr := real.Enqueue(id, ck, prio, ttl, now)
				wantRes, wantErr := model.enqueue(id, ck, prio, ttl, now)
				t.Logf("  op%d Enqueue(id=%q,ck=%q,prio=%d,ttl=%d,now=%d) -> %v err=%v | 依据: %s",
					op, id, ck, prio, ttl, now, gotRes, gotErr, model.note)
				if !sameErr(gotErr, wantErr) {
					t.Fatalf("seq %d op %d Enqueue: err %v, want %v", s, op, gotErr, wantErr)
				}
				if gotErr == nil && gotRes != wantRes {
					t.Fatalf("seq %d op %d Enqueue: result %v, want %v", s, op, gotRes, wantRes)
				}
			}
			model.checkInvariants(t)
		}

		// 收尾：全量 Drain 比对剩余状态。
		gotItems, gotErr := real.Drain(clock+100, mailbox.MaxCnt)
		wantItems, wantErr := model.drain(clock+100, mailbox.MaxCnt)
		t.Logf("  最终 Drain -> %v err=%v", keys(gotItems), gotErr)
		if !sameErr(gotErr, wantErr) {
			t.Fatalf("seq %d final Drain: err %v, want %v", s, gotErr, wantErr)
		}
		if !sameItems(gotItems, wantItems) {
			t.Fatalf("seq %d final Drain: %v, want %v", s, keys(gotItems), keys(wantItems))
		}
		model.checkInvariants(t)
	}
}
