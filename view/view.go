// Package view 维护分组聚合视图，编排 Apply → Recompute → Commit 多阶段提交。
package view

import (
	"errors"
	"os"
	"sort"
	"sync"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

var (
	// ErrVersion 版本回退或同版本内容冲突。
	ErrVersion = errors.New("view: version regression or conflict")
	// ErrNoGroup 分组键缺失。
	ErrNoGroup = errors.New("view: missing group key")
	// ErrNaN 值为 NaN。
	ErrNaN = errors.New("view: NaN value")
	// ErrNoRecord 删除/更新了不存在的记录。
	ErrNoRecord = errors.New("view: unknown record id")
	// ErrDup 重复插入同一 id。
	ErrDup = errors.New("view: duplicate record id")
	// ErrCrash 故障注入的模拟崩溃；返回后必须丢弃视图并经 Open 恢复。
	ErrCrash = errors.New("view: simulated crash")
)

// Stage 是故障注入的崩溃点。
type Stage int

const (
	StageNone      Stage = iota // 不注入
	StageApply                  // Apply 中途（日志追加前）
	StageRecompute              // Recompute 中途（日志已追加、内存未改）
	StageCommit                 // Commit 之前（内存已改、版本未提交）
)

// GroupState 是一组聚合结果的快照。
type GroupState struct {
	Count    int64
	Sum      float64
	Min      float64
	Max      float64
	Distinct int64
}

// View 是分组聚合视图。
type View struct {
	mu         sync.RWMutex
	groups     map[string]*group
	records    map[string]recRef
	maxVersion uint64
	last       change.Change
	hasLast    bool
	rejected   uint64
	recompute  [agg.NumKinds]int64
	visits     [agg.NumKinds]int64
	jw         *journal.Writer
	crashAt    Stage
}

// Open 打开视图；journalPath 已存在时重放恢复（截断坏尾巴），否则新建。
// journalPath 为空串时纯内存运行。
func Open(journalPath string) (*View, error) {
	v := &View{groups: map[string]*group{}, records: map[string]recRef{}}
	if journalPath == "" {
		return v, nil
	}
	if _, err := os.Stat(journalPath); err != nil {
		v.jw, err = journal.Create(journalPath)
		return v, err
	}
	recs, valid, err := journal.Replay(journalPath)
	if err != nil {
		var je *journal.Error
		if !errors.As(err, &je) {
			return nil, err
		}
		if terr := os.Truncate(journalPath, int64(valid)); terr != nil {
			return nil, terr
		}
	}
	for _, c := range recs {
		v.mutate(c)
		v.commit(c)
	}
	v.jw, err = journal.Append(journalPath)
	return v, err
}

// Close 关闭底层日志。
func (v *View) Close() error {
	if v.jw != nil {
		return v.jw.Close()
	}
	return nil
}

// InjectCrash 在下一次 Apply 的指定阶段注入崩溃（仅测试/演示用）。
func (v *View) InjectCrash(s Stage) { v.crashAt = s }

// Apply 应用一条变更：校验 → 日志追加 → Recompute → Commit。
func (v *View) Apply(c change.Change) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if c.Version < v.maxVersion ||
		(c.Version == v.maxVersion && !(v.hasLast && v.last.Equal(c))) {
		v.rejected++
		return ErrVersion
	}
	if c.Version == v.maxVersion && v.hasLast {
		return nil // 重复投递，幂等跳过
	}
	if err := v.validate(c); err != nil {
		v.rejected++
		return err
	}
	if v.crashAt == StageApply {
		v.crashAt = StageNone
		return ErrCrash
	}
	if v.jw != nil {
		if err := v.jw.Log(c); err != nil {
			return err
		}
	}
	if v.crashAt == StageRecompute {
		v.crashAt = StageNone
		return ErrCrash
	}
	v.mutate(c)
	if v.crashAt == StageCommit {
		v.crashAt = StageNone
		return ErrCrash
	}
	v.commit(c)
	return nil
}

// Group 查询单组；组不存在时返回 ok=false（而非零值）。
func (v *View) Group(name string) (GroupState, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	g, ok := v.groups[name]
	if !ok {
		return GroupState{}, false
	}
	return GroupState{Count: int64(len(g.members)), Sum: g.sum.Value(),
		Min: g.min, Max: g.max, Distinct: int64(len(g.distinct))}, true
}

// Groups 返回全部组名（字典序）。
func (v *View) Groups() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]string, 0, len(v.groups))
	for name := range v.groups {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Snapshot 返回全部组的逐字段快照。
func (v *View) Snapshot() map[string]GroupState {
	out := map[string]GroupState{}
	for _, name := range v.Groups() {
		gs, _ := v.Group(name)
		out[name] = gs
	}
	return out
}

// Rejected 返回被拒绝（乱序/非法）的变更数。
func (v *View) Rejected() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.rejected
}

// MaxVersion 返回已应用的最大版本号。
func (v *View) MaxVersion() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.maxVersion
}

// RecomputeStats 只读导出非导出计数器：各聚合器的重算次数与访问成员数。
func (v *View) RecomputeStats() (count, visits [agg.NumKinds]int64) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.recompute, v.visits
}
