package pane

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// naive 是按规格逐条写成的朴素模拟：窗口用成员规则直接枚举，
// 每次 Advance 重扫全部窗格求最小 hold，不使用堆。
type naive struct {
	s, d    int64
	policy  Policy
	al      int64
	cap     int64
	in      int64
	out     int64
	panes   map[int64]*Pane
	dropped int64
}

func newNaive(s, d int64, p Policy, al, capacity int64) *naive {
	return &naive{s: s, d: d, policy: p, al: al, cap: capacity, in: -1, out: -1, panes: make(map[int64]*Pane)}
}

// windowsOf 按成员规则 ws <= ts < ws+S（ws 为 D 的整数倍）枚举窗口，
// 返回 ws 升序清单。ts >= 0，故 ts/d 即为 floor(ts/d)。
func (n *naive) windowsOf(ts int64) []int64 {
	var desc []int64
	for ws := (ts / n.d) * n.d; ws+n.s > ts; ws -= n.d {
		desc = append(desc, ws)
	}
	for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
		desc[i], desc[j] = desc[j], desc[i]
	}
	return desc
}

// classify 给出判定依据：0 缓冲、1 迟到、2 丢弃。
func (n *naive) classify(ws int64) int {
	end := ws + n.s
	switch {
	case n.in >= end+n.al:
		return 2
	case n.in >= end:
		return 1
	default:
		return 0
	}
}

func (n *naive) add(ts, val int64) ([]PaneOut, error) {
	if ts < 0 || ts > MaxTS || val < -MaxVal || val > MaxVal {
		return nil, ErrInvalidParam
	}
	wsList := n.windowsOf(ts)
	kinds := make([]int, len(wsList))
	var newPanes int64
	for i, ws := range wsList {
		kinds[i] = n.classify(ws)
		if kinds[i] == 0 {
			if _, ok := n.panes[ws]; !ok {
				newPanes++
			}
		}
	}
	if int64(len(n.panes))+newPanes > n.cap {
		return nil, ErrCapacity
	}
	var late []PaneOut
	for i, ws := range wsList {
		end := ws + n.s
		switch kinds[i] {
		case 2:
			n.dropped++
		case 1:
			f := ts
			if n.policy == End {
				f = end - 1
			}
			late = append(late, PaneOut{WS: ws, Count: 1, Sum: val, TS: max(f, n.out)})
		case 0:
			p, ok := n.panes[ws]
			if !ok {
				p = &Pane{WS: ws, MinTS: ts, MaxTS: ts}
				n.panes[ws] = p
			}
			p.Count++
			p.Sum += val
			if ts < p.MinTS {
				p.MinTS = ts
			}
			if ts > p.MaxTS {
				p.MaxTS = ts
			}
			var raw int64
			switch n.policy {
			case Earliest:
				raw = p.MinTS
			case Latest:
				raw = p.MaxTS
			case End:
				raw = end - 1
			}
			p.Hold = max(raw, n.out)
		}
	}
	return late, nil
}

func (n *naive) advance(ip int64) ([]PaneOut, error) {
	if ip < 0 || ip > MaxTS {
		return nil, ErrInvalidParam
	}
	if ip < n.in {
		return nil, ErrRegression
	}
	n.in = ip
	var due []int64
	for ws := range n.panes {
		if ws+n.s <= ip {
			due = append(due, ws)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	var out []PaneOut
	for _, ws := range due {
		p := n.panes[ws]
		out = append(out, PaneOut{WS: ws, Count: p.Count, Sum: p.Sum, TS: p.Hold})
		delete(n.panes, ws)
	}
	bound := ip
	hasMin := false
	var minHold int64
	for _, p := range n.panes {
		if !hasMin || p.Hold < minHold {
			minHold = p.Hold
			hasMin = true
		}
	}
	if hasMin && minHold < bound {
		bound = minHold
	}
	if bound > n.out {
		n.out = bound
	}
	return out, nil
}

func (n *naive) paneList() []Pane {
	out := make([]Pane, 0, len(n.panes))
	for _, p := range n.panes {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WS < out[j].WS })
	return out
}

func kindName(k int) string {
	switch k {
	case 1:
		return "迟到"
	case 2:
		return "丢弃"
	default:
		return "缓冲"
	}
}

// classifyLog 生成 Add 的判定依据日志（基于操作前状态）。
func (n *naive) classifyLog(ts int64) string {
	if ts < 0 || ts > MaxTS {
		return "ts 越界"
	}
	var sb strings.Builder
	for _, ws := range n.windowsOf(ts) {
		fmt.Fprintf(&sb, " ws=%d:%s", ws, kindName(n.classify(ws)))
	}
	return sb.String()
}

// TestRandomAgainstNaive 2000 组随机操作序列与朴素模拟逐步对照，
// 日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq=%d", seq), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seq)))
			d := int64(1 + rng.Intn(8))
			maxS := 16 * d
			if maxS > 64 {
				maxS = 64
			}
			s := d + rng.Int63n(maxS-d+1)
			policy := Policy(rng.Intn(3))
			al := rng.Int63n(11)
			capacity := int64(1 + rng.Intn(12))
			m, err := New(s, d, policy, al, capacity)
			if err != nil {
				t.Fatalf("New 意外拒绝: %v", err)
			}
			n := newNaive(s, d, policy, al, capacity)
			t.Logf("参数 S=%d D=%d policy=%d AL=%d Cap=%d", s, d, policy, al, capacity)

			ops := 40 + rng.Intn(40)
			for op := 0; op < ops; op++ {
				if rng.Intn(100) < 55 {
					// Add：ts 围绕当前 I 附近取值，覆盖缓冲/迟到/丢弃，
					// 小概率取越界值验证参数非法。
					var ts int64
					switch rng.Intn(100) {
					case 0:
						ts = -1
					case 1:
						ts = MaxTS + 1
					default:
						ts = n.in + rng.Int63n(3*s+3) - s - 1
						if ts < 0 {
							ts = 0
						}
					}
					val := rng.Int63n(201) - 100
					switch rng.Intn(100) {
					case 0:
						val = MaxVal + 1
					case 1:
						val = -MaxVal - 1
					}
					reason := n.classifyLog(ts)
					gotLate, gotErr := m.Add(ts, val)
					wantLate, wantErr := n.add(ts, val)
					t.Logf("op=%d Add(ts=%d,val=%d) I=%d O=%d |%s => late=%v err=%v",
						op, ts, val, n.in, n.out, reason, wantLate, wantErr)
					if gotErr != wantErr {
						t.Fatalf("op=%d Add(%d,%d) err=%v, 朴素模拟=%v", op, ts, val, gotErr, wantErr)
					}
					if !reflect.DeepEqual(gotLate, wantLate) {
						t.Fatalf("op=%d Add(%d,%d) 迟到清单=%+v, 朴素模拟=%+v", op, ts, val, gotLate, wantLate)
					}
				} else {
					// Advance：覆盖前进、不变、回退与越界。
					var ip int64
					switch r := rng.Intn(100); {
					case r < 5:
						ip = MaxTS + 1
					case r < 15:
						ip = n.in
					case r < 25:
						ip = n.in - 1
					default:
						ip = n.in + rng.Int63n(2*s+2)
						if ip < 0 {
							ip = 0
						}
					}
					gotOut, gotErr := m.Advance(ip)
					wantOut, wantErr := n.advance(ip)
					t.Logf("op=%d Advance(I'=%d) I=%d O=%d => onTime=%v err=%v",
						op, ip, n.in, n.out, wantOut, wantErr)
					if gotErr != wantErr {
						t.Fatalf("op=%d Advance(%d) err=%v, 朴素模拟=%v", op, ip, gotErr, wantErr)
					}
					if !reflect.DeepEqual(gotOut, wantOut) {
						t.Fatalf("op=%d Advance(%d) ON_TIME=%+v, 朴素模拟=%+v", op, ip, gotOut, wantOut)
					}
				}
				if got, want := m.Panes(), n.paneList(); !reflect.DeepEqual(got, want) {
					t.Fatalf("op=%d 窗格表=%+v, 朴素模拟=%+v", op, got, want)
				}
				if got, want := m.Output(), n.out; got != want {
					t.Fatalf("op=%d O=%d, 朴素模拟=%d", op, got, want)
				}
				if got, want := m.Dropped(), n.dropped; got != want {
					t.Fatalf("op=%d 丢弃计数=%d, 朴素模拟=%d", op, got, want)
				}
				if got, want := m.Input(), n.in; got != want {
					t.Fatalf("op=%d I=%d, 朴素模拟=%d", op, got, want)
				}
			}
		})
	}
}

// TestConcurrent 并发调用 Add/Advance/查询：结果等价于某个串行顺序，
// 不变量 O <= I、O 单调、hold >= O、表大小 <= Cap、同一窗口至多一个
// ON_TIME 在并发下保持。
func TestConcurrent(t *testing.T) {
	m := mustNew(t, 16, 4, Latest, 8, 64)
	var tsGen atomic.Int64
	var workers sync.WaitGroup
	done := make(chan struct{})

	// 4 个并发 Add 调用方。
	for g := 0; g < 4; g++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 2000; i++ {
				ts := tsGen.Add(1) - 1
				if _, err := m.Add(ts, int64(i%7)-3); err != nil && err != ErrCapacity {
					t.Errorf("Add(%d) 意外错误: %v", ts, err)
					return
				}
			}
		}()
	}

	// 1 个推进方：I' 单调不减，收集 ON_TIME 并检查同一窗口至多发出一次。
	var wm atomic.Int64
	workers.Add(1)
	go func() {
		defer workers.Done()
		seen := make(map[int64]bool)
		for i := 0; i < 1500; i++ {
			ip := wm.Add(3)
			out, err := m.Advance(ip)
			if err != nil {
				t.Errorf("Advance(%d) 意外错误: %v", ip, err)
				return
			}
			for _, p := range out {
				if seen[p.WS] {
					t.Errorf("窗口 ws=%d 发出两次 ON_TIME", p.WS)
					return
				}
				seen[p.WS] = true
			}
		}
	}()

	// 1 个监视方：O 单调不减、hold >= O、表大小 <= Cap。
	var monitor sync.WaitGroup
	monitor.Add(1)
	go func() {
		defer monitor.Done()
		lastO := int64(-1)
		for {
			select {
			case <-done:
				return
			default:
			}
			o := m.Output()
			if o < lastO {
				t.Errorf("O 回退: %d -> %d", lastO, o)
				return
			}
			lastO = o
			panes := m.Panes()
			if int64(len(panes)) > 64 {
				t.Errorf("窗格表大小 %d 超过 Cap", len(panes))
				return
			}
			for _, p := range panes {
				if p.Hold < o {
					t.Errorf("窗格 ws=%d hold=%d < O=%d", p.WS, p.Hold, o)
					return
				}
			}
			_ = m.Dropped()
		}
	}()

	workers.Wait()
	close(done)
	monitor.Wait()

	// 收尾不变量：O <= I、hold >= O、表大小 <= Cap。
	o, i := m.Output(), m.Input()
	if o > i {
		t.Fatalf("O=%d > I=%d", o, i)
	}
	panes := m.Panes()
	if int64(len(panes)) > 64 {
		t.Fatalf("窗格表大小 %d 超过 Cap", len(panes))
	}
	for _, p := range panes {
		if p.Hold < o {
			t.Fatalf("窗格 ws=%d hold=%d < O=%d", p.WS, p.Hold, o)
		}
	}
}
