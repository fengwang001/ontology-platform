// Package rule 定义规则更新类型、按版本顺序应用、规则集快照与阈值命中判定。
package rule

import (
	"errors"
	"sort"
)

// 规则非法的两类可判定错误。
var (
	ErrEmptyID = errors.New("rule: empty rule ID")
	ErrNoSuch  = errors.New("rule: no such rule")
)

// Update 是一次规则发布：Del 为真表示删除 ID，否则新增/覆盖阈值。
type Update struct {
	Del       bool
	ID        string
	Threshold int64
}

// Put 构造新增/覆盖更新，Delete 构造删除更新。
func Put(id string, th int64) Update { return Update{ID: id, Threshold: th} }
func Delete(id string) Update        { return Update{Del: true, ID: id} }

// Data 是一条到达某实例的数据，Tag 为到达时的全局版本，Inst 为路由到的实例。
type Data struct {
	Key, Val  int64
	Tag, Inst int
}

// Hit 是一条处理输出。
type Hit struct {
	Key, Val int64
	RuleID   string
	Ver      int
	Inst     int
}

// Apply 把一条更新作用到规则集 rs；非法更新整体失败、不改 rs。
func Apply(rs map[string]int64, u Update) error {
	if u.ID == "" {
		return ErrEmptyID
	}
	if u.Del {
		if _, ok := rs[u.ID]; !ok {
			return ErrNoSuch
		}
		delete(rs, u.ID)
		return nil
	}
	rs[u.ID] = u.Threshold
	return nil
}

// Snapshot 从空规则集重放发布日志前 ver 个版本，得到该版本的规则集。
// Log 是全局发布日志：按版本顺序应用，Len 即当前全局版本 G。
type Log struct {
	ups []Update
	rs  map[string]int64
}

// Publish 校验并追加一条更新；非法更新整体失败、不留痕。
func (l *Log) Publish(u Update) error {
	if l.rs == nil {
		l.rs = map[string]int64{}
	}
	if err := Apply(l.rs, u); err != nil {
		return err
	}
	l.ups = append(l.ups, u)
	return nil
}

// Len 返回当前全局版本 G；At 返回第 i 个版本的更新（0 起）；Updates 返回日志。
func (l *Log) Len() int          { return len(l.ups) }
func (l *Log) At(i int) Update   { return l.ups[i] }
func (l *Log) Updates() []Update { return l.ups }

func Snapshot(log []Update, ver int) map[string]int64 {
	rs := map[string]int64{}
	for _, u := range log[:ver] {
		_ = Apply(rs, u) // 日志发布时已校验，必成功
	}
	return rs
}

// Hits 返回 val 命中的规则 ID（Val >= Threshold），按字典序。
func Hits(rs map[string]int64, val int64) []string {
	var ids []string
	for id, th := range rs {
		if val >= th {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Naive 朴素参照：对每条数据从空规则集重放日志到其 Tag，再逐条规则判定。
func Naive(log []Update, data []Data) []Hit {
	var out []Hit
	for _, d := range data {
		for _, id := range Hits(Snapshot(log, d.Tag), d.Val) {
			out = append(out, Hit{Key: d.Key, Val: d.Val, RuleID: id, Ver: d.Tag, Inst: d.Inst})
		}
	}
	return out
}

// SameHits 比较两个命中多重集是否相同。
func SameHits(a, b []Hit) bool {
	if len(a) != len(b) {
		return false
	}
	c := map[Hit]int{}
	for _, h := range a {
		c[h]++
	}
	for _, h := range b {
		if c[h]--; c[h] < 0 {
			return false
		}
	}
	return true
}
