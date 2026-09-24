// Package lside：外键连接左侧（左表/哈希/订阅收发/结果表/日志/丢弃数），依赖 rside。
package lside

import (
	"errors"
	"hash/fnv"

	"ontology/rside"
)

// 哨兵错误：空键 / 空队列投递 / 待投递响应超限，三者互不相同。
var ErrEmptyKey, ErrEmptyQueue, ErrPendingLimit = errors.New("lside: key must not be empty"), errors.New("lside: deliver from an empty queue"), errors.New("lside: pending responses would exceed maxPending")

// Change 是一条变更日志：Delete=true 表示 -k，否则表示 +k=(Val,RVal)。
type Change struct {
	K, Val, RVal string
	Delete       bool
}

// Join 是单个连接实例的全部左侧状态（含右侧）；方法由 api 加锁后串行调用。
type Join struct {
	r                  *rside.RSide
	left               map[string][2]string
	result             map[string][2]string
	log                []Change
	discarded, maxPend int
}

func New(maxPending int) *Join {
	return &Join{r: rside.New(), left: map[string][2]string{}, result: map[string][2]string{}, maxPend: maxPending}
}
func rowHash(fk, v string) uint64 {
	h := fnv.New64a()
	h.Write(append(append([]byte(fk), 0), v...))
	return h.Sum64()
}

// PutLeft 写左行；相同二元组空操作。FK=NULL 立即撤回不订阅，否则换订阅并入队当前右值。
func (j *Join) PutLeft(k, fk, v string) error {
	if k == "" {
		return ErrEmptyKey
	}
	if o, ok := j.left[k]; ok && o[0] == fk && o[1] == v {
		return nil
	}
	if j.r.Pending()+1 > j.maxPend { // 预校验额度：此时尚未改任何状态
		return ErrPendingLimit
	}
	if o, ok := j.left[k]; ok && o[0] != "" {
		j.r.Unsubscribe(o[0], k)
	}
	j.left[k] = [2]string{fk, v}
	if fk == "" {
		j.emit(Change{K: k, Delete: true})
		return nil
	}
	j.r.Subscribe(fk, k, rowHash(fk, v))
	return nil
}

func (j *Join) DeleteLeft(k string) error {
	if k == "" {
		return ErrEmptyKey
	}
	o, ok := j.left[k]
	if !ok {
		return nil
	}
	j.r.Unsubscribe(o[0], k)
	delete(j.left, k)
	j.emit(Change{K: k, Delete: true})
	return nil
}

func (j *Join) PutRight(fk, rv string) error { return j.changeRight(fk, &rv) }
func (j *Join) DeleteRight(fk string) error  { return j.changeRight(fk, nil) }
func (j *Join) changeRight(fk string, rv *string) error {
	if fk == "" {
		return ErrEmptyKey
	}
	if v, ok := j.r.RightValue(fk); rv == nil && !ok || rv != nil && ok && v == *rv {
		return nil
	}
	if j.r.Pending()+j.r.SubsLen(fk) > j.maxPend { // 全部响应入队或一条都不入
		return ErrPendingLimit
	}
	j.r.ChangeRight(fk, rv)
	return nil
}

// Deliver 投递队首；左行缺失或哈希不符丢弃计数；非 nil 同值不输出，nil 撤回。
func (j *Join) Deliver(fk string) error {
	if fk == "" {
		return ErrEmptyKey
	}
	resp, ok := j.r.Pop(fk)
	if !ok {
		return ErrEmptyQueue
	}
	row, ex := j.left[resp.K]
	if !ex || rowHash(row[0], row[1]) != resp.Hash {
		j.discarded++
		return nil
	}
	if resp.RVal != nil {
		j.emit(Change{K: resp.K, Val: row[1], RVal: *resp.RVal})
	} else {
		j.emit(Change{K: resp.K, Delete: true})
	}
	return nil
}

func (j *Join) DrainAll() {
	for fk, ok := j.r.NextQueuedFK(); ok; fk, ok = j.r.NextQueuedFK() {
		_ = j.Deliver(fk)
	}
}

// emit 是唯一日志出口（不变量 2）：删除只在键当前存在时输出，写入只在值变化时输出。
func (j *Join) emit(c Change) {
	cur, ok := j.result[c.K]
	if c.Delete {
		if !ok {
			return
		}
		delete(j.result, c.K)
	} else {
		nv := [2]string{c.Val, c.RVal}
		if ok && cur == nv {
			return
		}
		j.result[c.K] = nv
	}
	j.log = append(j.log, c)
}

// View/Changelog 返回副本；Discarded 返回累计丢弃数。
func (j *Join) View() map[string][2]string {
	out := make(map[string][2]string, len(j.result))
	for k, v := range j.result {
		out[k] = v
	}
	return out
}
func (j *Join) Changelog() []Change {
	out := make([]Change, len(j.log))
	copy(out, j.log)
	return out
}
func (j *Join) Discarded() int { return j.discarded }
