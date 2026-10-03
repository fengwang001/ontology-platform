// Package alertstore 维护告警记录：按标签指纹去重，支持 Fire/Resolve，
// 并对活跃告警（firing 与尚未被通知吸收的 resolved）总数施加上限。
// 本包不做并发控制，调用方（notify.Notifier）负责串行化。
package alertstore

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Labels 是 1 到 16 对非空字节串键值（长度各不超过 64）的映射。
type Labels map[string]string

// 状态类错误。
var (
	ErrFull      = errors.New("alertstore: active alert limit reached")
	ErrNotFiring = errors.New("alertstore: alert is not firing")
)

// CheckLabels 校验标签集是否满足规格约束。
func CheckLabels(l Labels) error {
	if len(l) < 1 || len(l) > 16 {
		return fmt.Errorf("labels count %d out of [1,16]", len(l))
	}
	for k, v := range l {
		if k == "" || len(k) > 64 {
			return fmt.Errorf("bad label key %q", k)
		}
		if v == "" || len(v) > 64 {
			return fmt.Errorf("bad label value for key %q", k)
		}
	}
	return nil
}

// Fingerprint 返回按键字节序排列的 k=v 以逗号连接的串。
func Fingerprint(l Labels) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(l[k])
	}
	return b.String()
}

// Alert 是一条告警记录。字段只允许通过 Store 方法修改。
type Alert struct {
	Fp      string // 标签指纹
	Labels  Labels
	Firing  bool  // false 表示 resolved（尚未被通知吸收）
	Since   int64 // 本次进入 firing 的时刻
	Deduped int   // 重复 Fire 的次数
}

// Store 是告警集合，按指纹去重，总数不超过 max。
type Store struct {
	max    int
	alerts map[string]*Alert
}

// New 创建容量为 maxActive 的 Store。
func New(maxActive int) *Store {
	return &Store{max: maxActive, alerts: make(map[string]*Alert)}
}

// Fire 上报告警：新指纹在建满时返回 ErrFull；已 firing 只累加 Deduped；
// 保留中的 resolved 原地回到 firing 并更新 Since（不占新名额）。
func (s *Store) Fire(now int64, l Labels) error {
	fp := Fingerprint(l)
	if a, ok := s.alerts[fp]; ok {
		if a.Firing {
			a.Deduped++
			return nil
		}
		a.Firing = true
		a.Since = now
		return nil
	}
	if len(s.alerts) >= s.max {
		return ErrFull
	}
	cp := make(Labels, len(l))
	for k, v := range l {
		cp[k] = v
	}
	s.alerts[fp] = &Alert{Fp: fp, Labels: cp, Firing: true, Since: now}
	return nil
}

// Resolve 把 firing 告警置为 resolved；非 firing 返回 ErrNotFiring。
func (s *Store) Resolve(now int64, l Labels) error {
	fp := Fingerprint(l)
	a, ok := s.alerts[fp]
	if !ok || !a.Firing {
		return ErrNotFiring
	}
	a.Firing = false
	return nil
}

// Get 按指纹取告警，不存在返回 nil。
func (s *Store) Get(fp string) *Alert { return s.alerts[fp] }

// Remove 物理删除指定指纹的告警。
func (s *Store) Remove(fp string) { delete(s.alerts, fp) }

// Len 返回当前活跃告警总数。
func (s *Store) Len() int { return len(s.alerts) }

// Snapshot 返回全部告警的快照（顺序未定义，调用方自行排序）。
func (s *Store) Snapshot() []*Alert {
	out := make([]*Alert, 0, len(s.alerts))
	for _, a := range s.alerts {
		out = append(out, a)
	}
	return out
}
