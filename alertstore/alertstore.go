// Package alertstore 保存按标签指纹索引的告警记录。
package alertstore

import (
	"errors"
	"sort"
)

// 存储层错误，notify 层按同一组哨兵错误暴露给调用方。
var (
	ErrAlertLimit = errors.New("active alert limit reached")
	ErrNotFiring  = errors.New("alert is not firing")
)

// Labels 是非空字节串键到非空字节串值的映射。
type Labels map[string]string

// Alert 是一条告警在某时刻的快照。
type Alert struct {
	Fingerprint string
	Labels      Labels
	Since       int64
	Firing      bool
	Deduped     int
}

// Store 保存全部告警记录。
type Store struct {
	maxActive int
	alerts    map[string]*Alert
}

// New 创建容量上限为 maxActive 的存储。
func New(maxActive int) *Store {
	return &Store{maxActive: maxActive, alerts: map[string]*Alert{}}
}

// Fire 新建或复活一条告警；已 firing 时累加 Deduped。
// 第二个返回值表示本次是否为重复 Fire（Deduped 加一）。
func (s *Store) Fire(now int64, labels Labels) (*Alert, bool, error) {
	fp := Fingerprint(labels)
	if a, ok := s.alerts[fp]; ok {
		if a.Firing {
			a.Deduped++
			return cloneAlert(a), true, nil
		}
		a.Firing = true
		a.Since = now
		a.Deduped = 0
		return cloneAlert(a), false, nil
	}
	if len(s.alerts) >= s.maxActive {
		return nil, false, ErrAlertLimit
	}
	a := &Alert{
		Fingerprint: fp,
		Labels:      cloneLabels(labels),
		Since:       now,
		Firing:      true,
	}
	s.alerts[fp] = a
	return cloneAlert(a), false, nil
}

// Resolve 将 firing 告警置为 resolved。
func (s *Store) Resolve(labels Labels) (*Alert, error) {
	a, ok := s.alerts[Fingerprint(labels)]
	if !ok || !a.Firing {
		return nil, ErrNotFiring
	}
	a.Firing = false
	return cloneAlert(a), nil
}

// ActiveCount 返回 firing 与未吸收 resolved 的总数。
func (s *Store) ActiveCount() int { return len(s.alerts) }

// Get 返回指纹对应告警的快照，不存在返回 nil。
func (s *Store) Get(fp string) *Alert {
	if a, ok := s.alerts[fp]; ok {
		return cloneAlert(a)
	}
	return nil
}

// Snapshot 返回全部告警快照，顺序不定。
func (s *Store) Snapshot() []*Alert {
	out := make([]*Alert, 0, len(s.alerts))
	for _, a := range s.alerts {
		out = append(out, cloneAlert(a))
	}
	return out
}

// Delete 清除一条记录（resolved 已被通知吸收）。
func (s *Store) Delete(fp string) { delete(s.alerts, fp) }

func cloneLabels(labels Labels) Labels {
	c := make(Labels, len(labels))
	for k, v := range labels {
		c[k] = v
	}
	return c
}

func cloneAlert(a *Alert) *Alert {
	c := *a
	c.Labels = cloneLabels(a.Labels)
	return &c
}

// ---- 指纹与分组键 ----
// Fingerprint 返回按键字节序排列的 k=v 以逗号连接的指纹。
func Fingerprint(labels Labels) string {
	keys := sortedKeys(labels)
	var buf []byte
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, k...)
		buf = append(buf, '=')
		buf = append(buf, labels[k]...)
	}
	return string(buf)
}

// GroupKey 按 groupNames 顺序取标签值（缺失记空串），以 \x00 连接。
func GroupKey(labels Labels, groupNames []string) string {
	var buf []byte
	for i, name := range groupNames {
		if i > 0 {
			buf = append(buf, 0)
		}
		buf = append(buf, labels[name]...)
	}
	return string(buf)
}

func sortedKeys(labels Labels) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
