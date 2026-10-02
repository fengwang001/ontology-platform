package router

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// 本文件包含一个严格按规格逐条写成的朴素模拟（model），
// 与 Router 在 2000 组随机操作序列上逐步对照，
// 日志打印每步的输入、输出与判定依据。

type mBinding struct {
	host string
	last int64
}

type mHost struct {
	status HostStatus
	s      int64 // 排空起点
}

type model struct {
	t, d     int64
	hosts    map[string]*mHost
	bindings map[string]mBinding
	maxNow   int64
	basis    []string // 当前操作的判定依据
}

func newModel(t, d int64) *model {
	return &model{
		t:        t,
		d:        d,
		hosts:    make(map[string]*mHost),
		bindings: make(map[string]mBinding),
	}
}

func (m *model) note(format string, args ...any) {
	m.basis = append(m.basis, fmt.Sprintf(format, args...))
}

func (m *model) live(id string, now int64) int {
	n := 0
	for _, b := range m.bindings {
		if b.host == id && b.last+m.t > now {
			n++
		}
	}
	return n
}

func (m *model) settle(now int64) {
	var ids []string
	for id, h := range m.hosts {
		if h.status == StatusDraining {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		h := m.hosts[id]
		live := m.live(id, now)
		if live == 0 || now >= h.s+m.d {
			h.status = StatusRemoved
			for k, b := range m.bindings {
				if b.host == id {
					delete(m.bindings, k)
				}
			}
			m.note("落实: %s 移除 (有效绑定=%d, now-s=%d, D=%d)", id, live, now-h.s, m.d)
		}
	}
}

func (m *model) checkClock(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < m.maxNow {
		return ErrClockRegression
	}
	return nil
}

func (m *model) addHost(id string) error {
	m.basis = m.basis[:0]
	if id == "" {
		m.note("参数非法: 空 id")
		return ErrEmptyHostID
	}
	if _, ok := m.hosts[id]; ok {
		m.note("参数非法: id 已存在(含已移除)")
		return ErrHostExists
	}
	m.hosts[id] = &mHost{status: StatusActive}
	m.note("登记活跃主机 %s", id)
	return nil
}

func (m *model) route(key string, now int64) (string, error) {
	m.basis = m.basis[:0]
	if key == "" {
		m.note("参数非法: 空键")
		return "", ErrEmptyKey
	}
	if err := m.checkClock(now); err != nil {
		m.note("时钟检查失败: %v", err)
		return "", err
	}
	m.maxNow = now
	m.settle(now)
	if b, ok := m.bindings[key]; ok {
		if b.last+m.t > now {
			m.bindings[key] = mBinding{host: b.host, last: now}
			m.note("绑定有效 (last=%d, last+T=%d > now=%d), 刷新 last", b.last, b.last+m.t, now)
			return b.host, nil
		}
		delete(m.bindings, key)
		m.note("绑定过期 (last+T=%d 不大于 now=%d), 丢弃", b.last+m.t, now)
	}
	var active []string
	for id, h := range m.hosts {
		if h.status == StatusActive {
			active = append(active, id)
		}
	}
	if len(active) == 0 {
		m.note("无活跃主机")
		return "", ErrNoActiveHost
	}
	sort.Strings(active)
	best := active[0]
	bestLive := m.live(best, now)
	for _, id := range active[1:] {
		if n := m.live(id, now); n < bestLive {
			best, bestLive = id, n
		}
	}
	m.bindings[key] = mBinding{host: best, last: now}
	m.note("新绑定: 活跃主机中最少有效绑定=%d, 选中 %s", bestLive, best)
	return best, nil
}

func (m *model) drain(id string, now int64) error {
	m.basis = m.basis[:0]
	h, ok := m.hosts[id]
	if !ok {
		m.note("参数非法: 主机不存在")
		return ErrHostNotFound
	}
	if err := m.checkClock(now); err != nil {
		m.note("时钟检查失败: %v", err)
		return err
	}
	m.maxNow = now
	m.settle(now)
	if h.status != StatusActive {
		m.note("状态非法: %s 当前为 %v", id, h.status)
		return ErrHostNotActive
	}
	h.status = StatusDraining
	h.s = now
	m.note("%s 转为排空, s=%d", id, now)
	m.settle(now)
	return nil
}

func (m *model) status(id string, now int64) (HostStatus, error) {
	m.basis = m.basis[:0]
	h, ok := m.hosts[id]
	if !ok {
		m.note("参数非法: 主机不存在")
		return StatusActive, ErrHostNotFound
	}
	if err := m.checkClock(now); err != nil {
		m.note("时钟检查失败: %v", err)
		return StatusActive, err
	}
	m.maxNow = now
	m.settle(now)
	m.note("状态=%v", h.status)
	return h.status, nil
}

func (m *model) liveCount(id string, now int64) (int, error) {
	m.basis = m.basis[:0]
	if _, ok := m.hosts[id]; !ok {
		m.note("参数非法: 主机不存在")
		return 0, ErrHostNotFound
	}
	if err := m.checkClock(now); err != nil {
		m.note("时钟检查失败: %v", err)
		return 0, err
	}
	m.maxNow = now
	m.settle(now)
	n := m.live(id, now)
	m.note("有效绑定数=%d", n)
	return n, nil
}

var (
	modelHostIDs = []string{"h0", "h1", "h2", "h3", "h4"}
	modelKeys    = []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7"}
	// probeIDs 含一个从不登记的 id，用于覆盖“主机不存在”。
	probeIDs = []string{"h0", "h1", "h2", "h3", "h4", "void"}
)

// TestModelReplay 用 2000 组随机操作序列对照 Router 与朴素模拟，
// 每组序列结束后还逐台主机核对状态与有效绑定数。
func TestModelReplay(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 30
	for seq := 0; seq < sequences; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq%04d", seq), func(t *testing.T) {
			rnd := rand.New(rand.NewSource(int64(seq)*7919 + 13))
			idle := int64(1 + rnd.Intn(30))
			drain := int64(1 + rnd.Intn(40))
			r, err := NewRouter(idle, drain)
			if err != nil {
				t.Fatalf("NewRouter(%d, %d) 失败: %v", idle, drain, err)
			}
			m := newModel(idle, drain)
			t.Logf("序列 %d: T=%d D=%d", seq, idle, drain)

			var cur int64
			nextNow := func() int64 {
				switch x := rnd.Intn(100); {
				case x < 75:
					cur += int64(rnd.Intn(8))
				case x < 85:
					// 原地不动
				case x < 93:
					cur -= int64(1 + rnd.Intn(4)) // 时钟回退（可能为负）
				case x < 97:
					return -1 // 时间非法
				default:
					return 1_000_000_000_000_001 // 时间非法
				}
				return cur
			}
			pickHost := func() string {
				if rnd.Intn(20) == 0 {
					return ""
				}
				if rnd.Intn(20) == 0 {
					return "void"
				}
				return modelHostIDs[rnd.Intn(len(modelHostIDs))]
			}
			pickKey := func() string {
				if rnd.Intn(20) == 0 {
					return ""
				}
				return modelKeys[rnd.Intn(len(modelKeys))]
			}

			for op := 0; op < opsPerSeq; op++ {
				kind := rnd.Intn(11)
				switch {
				case kind < 2: // AddHost
					id := pickHost()
					got := r.AddHost(id)
					want := m.addHost(id)
					m.logOp(t, seq, op, fmt.Sprintf("AddHost(%q)", id), got, want)
				case kind < 7: // Route
					key, now := pickKey(), nextNow()
					gotHost, gotErr := r.Route(key, now)
					wantHost, wantErr := m.route(key, now)
					m.logOp(t, seq, op, fmt.Sprintf("Route(%q, %d)", key, now), gotErr, wantErr)
					if gotErr == nil && gotHost != wantHost {
						t.Fatalf("seq=%d op=%d Route(%q, %d): 路由结果 %q != 模拟 %q",
							seq, op, key, now, gotHost, wantHost)
					}
				case kind < 9: // Drain
					id, now := pickHost(), nextNow()
					got := r.Drain(id, now)
					want := m.drain(id, now)
					m.logOp(t, seq, op, fmt.Sprintf("Drain(%q, %d)", id, now), got, want)
				case kind < 10: // Status
					id, now := pickHost(), nextNow()
					gotS, gotErr := r.Status(id, now)
					wantS, wantErr := m.status(id, now)
					m.logOp(t, seq, op, fmt.Sprintf("Status(%q, %d)", id, now), gotErr, wantErr)
					if gotErr == nil && gotS != wantS {
						t.Fatalf("seq=%d op=%d Status(%q, %d): 状态 %v != 模拟 %v",
							seq, op, id, now, gotS, wantS)
					}
				default: // Live
					id, now := pickHost(), nextNow()
					gotN, gotErr := r.Live(id, now)
					wantN, wantErr := m.liveCount(id, now)
					m.logOp(t, seq, op, fmt.Sprintf("Live(%q, %d)", id, now), gotErr, wantErr)
					if gotErr == nil && gotN != wantN {
						t.Fatalf("seq=%d op=%d Live(%q, %d): 计数 %d != 模拟 %d",
							seq, op, id, now, gotN, wantN)
					}
				}
			}

			// 序列结束后，在最大 now 上逐台主机核对状态与有效绑定数。
			for _, id := range probeIDs {
				gotS, gotErr := r.Status(id, m.maxNow)
				wantS, wantErr := m.status(id, m.maxNow)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq=%d 终态核对 Status(%q): err %v != 模拟 %v", seq, id, gotErr, wantErr)
				}
				if gotErr == nil && gotS != wantS {
					t.Fatalf("seq=%d 终态核对 Status(%q): %v != 模拟 %v", seq, id, gotS, wantS)
				}
				gotN, gotErr := r.Live(id, m.maxNow)
				wantN, wantErr := m.liveCount(id, m.maxNow)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq=%d 终态核对 Live(%q): err %v != 模拟 %v", seq, id, gotErr, wantErr)
				}
				if gotErr == nil && gotN != wantN {
					t.Fatalf("seq=%d 终态核对 Live(%q): %d != 模拟 %d", seq, id, gotN, wantN)
				}
			}
			t.Logf("序列 %d 终态核对通过 (maxNow=%d)", seq, m.maxNow)
		})
	}
}

// logOp 打印单步输入、输出与判定依据，并断言两侧错误一致。
func (m *model) logOp(t *testing.T, seq, op int, input string, got, want error) {
	t.Helper()
	basis := strings.Join(m.basis, "; ")
	t.Logf("seq=%d op=%d 输入=%s 输出err=%v 期望err=%v 依据: %s", seq, op, input, got, want, basis)
	if !errors.Is(got, want) {
		t.Fatalf("seq=%d op=%d %s: err %v != 模拟 %v (依据: %s)", seq, op, input, got, want, basis)
	}
}
