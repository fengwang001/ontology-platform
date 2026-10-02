package router

// 本文件是按题目规则逐步写成的朴素模拟，用于与 Router 的实现对照。
// 模拟刻意采用最直接的写法：每次全量扫描绑定、逐步落实，不依赖
// Router 的任何内部代码。

import (
	"fmt"
	"sort"
	"strings"
)

type mBinding struct {
	host string
	last int64
}

type model struct {
	T, D       int64
	status     map[string]HostStatus
	drainStart map[string]int64
	bindings   map[string]mBinding
	maxNow     int64
}

func newModel(t, d int64) *model {
	return &model{
		T:          t,
		D:          d,
		status:     make(map[string]HostStatus),
		drainStart: make(map[string]int64),
		bindings:   make(map[string]mBinding),
	}
}

func (m *model) live(id string, now int64) int {
	n := 0
	for _, b := range m.bindings {
		if b.host == id && b.last+m.T > now {
			n++
		}
	}
	return n
}

// settle 按 id 升序落实排空主机，并把移除依据追加到 notes。
func (m *model) settle(now int64, notes *[]string) {
	var ids []string
	for id, s := range m.status {
		if s == Draining {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		live := m.live(id, now)
		deadline := m.drainStart[id] + m.D
		if live == 0 || now >= deadline {
			m.status[id] = Removed
			delete(m.drainStart, id)
			removedKeys := 0
			for k, b := range m.bindings {
				if b.host == id {
					delete(m.bindings, k)
					removedKeys++
				}
			}
			why := "无有效绑定"
			if live > 0 {
				why = fmt.Sprintf("到达排空期限（now=%d >= s+D=%d）", now, deadline)
			}
			*notes = append(*notes, fmt.Sprintf("落实：%s 移除（%s），删除 %d 个绑定", id, why, removedKeys))
		}
	}
}

func (m *model) checkClock(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < m.maxNow {
		return ErrClockRollback
	}
	return nil
}

func (m *model) addHost(id string) (error, string) {
	if id == "" {
		return ErrEmptyHostID, "拒绝：id 为空"
	}
	if _, ok := m.status[id]; ok {
		return ErrHostExists, "拒绝：id 已存在（含已移除）"
	}
	m.status[id] = Active
	return nil, "登记为活跃态"
}

func (m *model) route(key string, now int64) (string, error, string) {
	if key == "" {
		return "", ErrEmptyKey, "拒绝：会话键为空串"
	}
	if err := m.checkClock(now); err != nil {
		return "", err, "拒绝：时间/时钟检查未通过，状态不变"
	}
	m.maxNow = now
	var notes []string
	m.settle(now, &notes)
	if b, ok := m.bindings[key]; ok && b.last+m.T > now {
		expiry := b.last + m.T
		b.last = now
		m.bindings[key] = b
		notes = append(notes, fmt.Sprintf("绑定有效（last+T=%d > %d），粘到 %s 并刷新 last=%d", expiry, now, b.host, now))
		return b.host, nil, strings.Join(notes, "；")
	}
	if _, ok := m.bindings[key]; ok {
		delete(m.bindings, key)
		notes = append(notes, "绑定已过期（last+T <= now），丢弃")
	}
	best := ""
	bestLive := 0
	found := false
	var ids []string
	for id, s := range m.status {
		if s == Active {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		l := m.live(id, now)
		if !found || l < bestLive {
			best, bestLive, found = id, l, true
		}
	}
	if !found {
		notes = append(notes, "无活跃主机（落实与最大 now 推进保留）")
		return "", ErrNoActiveHost, strings.Join(notes, "；")
	}
	m.bindings[key] = mBinding{host: best, last: now}
	notes = append(notes, fmt.Sprintf("选活跃主机中有效绑定最少者 %s（%d 个），建立绑定 last=%d", best, bestLive, now))
	return best, nil, strings.Join(notes, "；")
}

func (m *model) drain(id string, now int64) (error, string) {
	if _, ok := m.status[id]; !ok {
		return ErrHostNotFound, "拒绝：主机不存在"
	}
	if err := m.checkClock(now); err != nil {
		return err, "拒绝：时间/时钟检查未通过，状态不变"
	}
	m.maxNow = now
	var notes []string
	m.settle(now, &notes)
	if m.status[id] != Active {
		notes = append(notes, fmt.Sprintf("拒绝：主机非活跃态（%s），落实与最大 now 推进保留", m.status[id]))
		return ErrHostNotActive, strings.Join(notes, "；")
	}
	m.status[id] = Draining
	m.drainStart[id] = now
	notes = append(notes, fmt.Sprintf("转为排空态，s=%d", now))
	m.settle(now, &notes)
	return nil, strings.Join(notes, "；")
}

func (m *model) statusOf(id string, now int64) (HostStatus, error, string) {
	if _, ok := m.status[id]; !ok {
		return Removed, ErrHostNotFound, "拒绝：主机不存在"
	}
	if err := m.checkClock(now); err != nil {
		return Removed, err, "拒绝：时间/时钟检查未通过，状态不变"
	}
	m.maxNow = now
	var notes []string
	m.settle(now, &notes)
	notes = append(notes, fmt.Sprintf("状态为 %s", m.status[id]))
	return m.status[id], nil, strings.Join(notes, "；")
}

func (m *model) liveOf(id string, now int64) (int, error, string) {
	if _, ok := m.status[id]; !ok {
		return 0, ErrHostNotFound, "拒绝：主机不存在"
	}
	if err := m.checkClock(now); err != nil {
		return 0, err, "拒绝：时间/时钟检查未通过，状态不变"
	}
	m.maxNow = now
	var notes []string
	m.settle(now, &notes)
	n := m.live(id, now)
	notes = append(notes, fmt.Sprintf("有效绑定数 %d", n))
	return n, nil, strings.Join(notes, "；")
}
