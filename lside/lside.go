// Package lside 维护左表、行哈希、结果表与变更日志，并校验来自 rside 的响应。
// 依赖 rside；不是并发安全的，由调用方串行化。
package lside

import (
	"fmt"
	"hash/fnv"

	"ontology/rside"
)

// Row 是一行左表数据：FK 为空串表示 NULL。
type Row struct{ FK, Val string }

// Hash 计算左行哈希：FNV-1a 64(FK + "\x00" + Val)。
func Hash(fk, val string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fk))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(val))
	return h.Sum64()
}

// Left 是左表 + 结果表 + 变更日志。
type Left struct {
	r         *rside.Right
	tab       map[string]Row
	res       map[string][2]string
	log       []string
	discarded int
}

func New(r *rside.Right) *Left {
	return &Left{r: r, tab: make(map[string]Row), res: make(map[string][2]string)}
}

// PutLeft 写入左行。(FK,Val) 与当前完全相同为空操作。任何失败都不留痕。
func (l *Left) PutLeft(k, fk, val string) error {
	if k == "" {
		return rside.ErrEmptyKey
	}
	old, ok := l.tab[k]
	if ok && old.FK == fk && old.Val == val {
		return nil
	}
	if fk != "" && !l.r.CanEnqueue(1) { // 预检：订阅必然入队一条响应
		return rside.ErrTooManyPending
	}
	if ok && old.FK != "" {
		l.r.Unsubscribe(old.FK, k)
	}
	l.tab[k] = Row{fk, val}
	if fk == "" {
		if _, in := l.res[k]; in {
			delete(l.res, k)
			l.log = append(l.log, "-"+k)
		}
		return nil
	}
	return l.r.Subscribe(fk, k, Hash(fk, val))
}

// DeleteLeft 删除左行。不存在为空操作。
func (l *Left) DeleteLeft(k string) error {
	if k == "" {
		return rside.ErrEmptyKey
	}
	old, ok := l.tab[k]
	if !ok {
		return nil
	}
	if old.FK != "" {
		l.r.Unsubscribe(old.FK, k)
	}
	delete(l.tab, k)
	if _, in := l.res[k]; in {
		delete(l.res, k)
		l.log = append(l.log, "-"+k)
	}
	return nil
}

// Deliver 投递 fk 队首响应：校验哈希，采纳则更新结果表与变更日志，否则计入丢弃数。
func (l *Left) Deliver(fk string) error {
	resp, err := l.r.Deliver(fk)
	if err != nil {
		return err
	}
	row, ok := l.tab[resp.K]
	if !ok || Hash(row.FK, row.Val) != resp.Hash {
		l.discarded++
		return nil
	}
	if resp.HasRVal {
		nv := [2]string{row.Val, resp.RVal}
		if cur, in := l.res[resp.K]; !in || cur != nv {
			l.res[resp.K] = nv
			l.log = append(l.log, fmt.Sprintf("+%s=(%s,%s)", resp.K, row.Val, resp.RVal))
		}
	} else if _, in := l.res[resp.K]; in {
		delete(l.res, resp.K)
		l.log = append(l.log, "-"+resp.K)
	}
	return nil
}

// View 返回结果表副本；Changelog 返回变更日志副本；Discarded 返回丢弃数。
func (l *Left) View() map[string][2]string {
	out := make(map[string][2]string, len(l.res))
	for k, v := range l.res {
		out[k] = v
	}
	return out
}

func (l *Left) Changelog() []string { return append([]string(nil), l.log...) }

func (l *Left) Discarded() int { return l.discarded }

// TabSnapshot 返回左表副本（供批量重算比对）。
func (l *Left) TabSnapshot() map[string]Row {
	out := make(map[string]Row, len(l.tab))
	for k, v := range l.tab {
		out[k] = v
	}
	return out
}

// CheckSubs 校验不变量 3：订阅表与左表、行哈希严格对应。
func (l *Left) CheckSubs() error {
	_, subs := l.r.Snapshot()
	for k, row := range l.tab {
		if row.FK == "" {
			continue
		}
		if h, ok := subs[row.FK][k]; !ok || h != Hash(row.FK, row.Val) {
			return fmt.Errorf("lside: row %s missing/stale in sub[%s]", k, row.FK)
		}
	}
	for fk, m := range subs {
		for k, h := range m {
			row, ok := l.tab[k]
			if !ok || row.FK != fk || Hash(row.FK, row.Val) != h {
				return fmt.Errorf("lside: stray sub[%s][%s]", fk, k)
			}
		}
	}
	return nil
}
