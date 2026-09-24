package pace

import (
	"errors"
	"math/rand"
	"slices"
	"testing"
)

// naive 是测试内朴素参照：从零逐步维护 (now, tokens, 队列) 与入/出计数。
type naive struct {
	now, tok           int64
	q                  []int64
	rate, catch, burst int64
	maxQ               int
	in, out            int64
}

func (m *naive) advance(t int64) {
	if t <= m.now {
		return
	}
	r := m.rate
	if len(m.q) > 0 { // 判定时刻 = Advance 开始时刻
		r = m.catch
	}
	m.tok = min(m.burst, m.tok+r*(t-m.now))
	m.now = t
}
func (m *naive) enqueue(ids []int64) bool {
	if len(m.q)+len(ids) > m.maxQ {
		return false
	}
	m.q = append(m.q, ids...)
	m.in += int64(len(ids))
	return true
}
func (m *naive) emit() []int64 {
	k := int(min(m.tok, int64(len(m.q))))
	r := append([]int64(nil), m.q[:k]...)
	m.q, m.tok, m.out = m.q[k:], m.tok-int64(k), m.out+int64(k)
	return r
}
func (m *naive) match(t *testing.T, p *Queue) {
	t.Helper()
	if p.Now() != m.now || p.Tokens() != m.tok || p.Pending() != len(m.q) {
		t.Fatalf("triple real=(%d,%d,%d) naive=(%d,%d,%d)", p.Now(), p.Tokens(), p.Pending(), m.now, m.tok, len(m.q))
	}
	if m.in != m.out+int64(len(m.q)) {
		t.Fatalf("conservation: %d != %d+%d", m.in, m.out, len(m.q))
	}
}

// TestNaiveEquivalenceRandom：随机参数与随机操作序列下，真实队列逐状态等于朴素参照。
func TestNaiveEquivalenceRandom(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		rate := int64(1 + r.Intn(4))
		m := &naive{rate: rate, catch: rate + int64(r.Intn(6)),
			burst: int64(1 + r.Intn(12)), maxQ: 5 + r.Intn(26)}
		m.tok = m.burst
		p, err := New(m.rate, m.catch, m.burst, m.maxQ)
		if err != nil {
			t.Fatal(err)
		}
		var nextID int64 = 1
		for n := 0; n < 300; n++ {
			switch r.Intn(3) {
			case 0:
				ids := make([]int64, 1+r.Intn(6))
				for i := range ids {
					ids[i], nextID = nextID, nextID+1
				}
				errP, okM := p.Enqueue(ids...), m.enqueue(ids)
				if (errP == nil) != okM {
					t.Fatalf("iter %d step %d: enqueue accept mismatch", iter, n)
				}
				if errP != nil && !errors.Is(errP, ErrQueueFull) {
					t.Fatalf("want ErrQueueFull, got %v", errP)
				}
			case 1:
				tm := m.now + int64(r.Intn(4))
				if err := p.Advance(tm); err != nil {
					t.Fatal(err)
				}
				m.advance(tm)
			case 2:
				if got, want := p.Emit(), m.emit(); !slices.Equal(got, want) {
					t.Fatalf("emit real=%v naive=%v", got, want)
				}
			}
			m.match(t, p)
		}
	}
}

// TestEmitDecisionIsOOne：吐出个数只由 len 与令牌决定（非导出计数器 scanned
// 直接读字段，不经任何导出方法），两档令牌情形下都不随 m 增长。
func TestEmitDecisionIsOOne(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		ids := make([]int64, m)
		for i := range ids {
			ids[i] = int64(i + 1)
		}
		// 令牌足够：一次吐完 m 个。
		full, err := New(1, int64(m), int64(m), m)
		if err != nil {
			t.Fatal(err)
		}
		if err := full.Enqueue(ids...); err != nil {
			t.Fatal(err)
		}
		got := full.Emit()
		if len(got) != m || got[0] != 1 || got[m-1] != int64(m) || full.scanned > 2 {
			t.Fatalf("m=%d full: len=%d scanned=%d", m, len(got), full.scanned)
		}
		// 令牌仅 1：只吐队头一个。
		one, _ := New(1, int64(m), 1, m)
		if err := one.Enqueue(ids...); err != nil {
			t.Fatal(err)
		}
		got = one.Emit()
		if len(got) != 1 || got[0] != 1 || one.Pending() != m-1 || one.scanned > 2 {
			t.Fatalf("m=%d one: len=%d scanned=%d", m, len(got), one.scanned)
		}
	}
}
