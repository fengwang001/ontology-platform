// Package snap 在 state 之上生成全量基线（base）与增量（delta），
// 并按检查点顺序覆盖合并以恢复状态。依赖方向：snap -> state。
package snap

import (
	"errors"
	"fmt"

	"ontology/state"
)

// ErrNoBase 表示从未做过检查点就尝试 Recover。
var ErrNoBase = errors.New("snap: no base checkpoint to recover from")

// Change 是 delta 中单个键的记录：Deleted=true 即 tombstone。
type Change struct {
	Value   int64
	Deleted bool
}

// Snapshot 是一次检查点的内容。
type Snapshot struct {
	IsBase bool              // 首次检查点为 true（全量基线）
	Data   map[string]Change // base：整张表；delta：仅变化键（含 tombstone）
}

// Checkpointer 负责检查点的生成、留存与恢复合并。
type Checkpointer struct {
	st      *state.State
	history []Snapshot
	prev    map[string]int64 // 上一次检查点时刻的状态（增量维护，绝不全表扫描）
	// scanCount 为非导出计数器：最近一次 Checkpoint 为生成 delta 而遍历的键个数。
	// 不提供任何能读出其数值的导出途径；仅同包白盒测试可直接核验。
	scanCount int
}

// New 基于给定状态创建 Checkpointer。
func New(st *state.State) *Checkpointer {
	return &Checkpointer{st: st, prev: map[string]int64{}}
}

// Checkpoint 记录当前状态：首次为全量基线，之后为相对上一次检查点的最小增量。
func (c *Checkpointer) Checkpoint() error {
	if len(c.history) == 0 {
		cur := c.st.Snapshot() // 深拷贝，作为新的 prev
		data := make(map[string]Change, len(cur))
		for k, v := range cur {
			data[k] = Change{Value: v}
		}
		c.prev = cur
		c.history = append(c.history, Snapshot{IsBase: true, Data: data})
		c.st.DrainDirty() // 基线前的脏键全部入账
		c.scanCount = 0   // 基线不是 delta，无 delta 遍历
		return nil
	}
	dirty := c.st.DrainDirty()
	c.scanCount = len(dirty) // 只遍历脏键，不随总键数 m 增长
	data := map[string]Change{}
	for _, k := range dirty {
		v, ok := c.st.Get(k)
		pv, pExisted := c.prev[k]
		switch {
		case ok && pExisted && pv == v:
			// 操作期间被改动、但最终值与上次检查点相同：delta 不记。
		case ok:
			data[k] = Change{Value: v} // 值变化/新增（含值为 0 的新键）：记新值
			c.prev[k] = v
		case pExisted:
			data[k] = Change{Deleted: true} // 删除：必须记 tombstone
			delete(c.prev, k)
		default:
			// 上次检查点时就不存在的键，本周期新增后又删除：相对上次未变化，不进 delta
		}
	}
	c.history = append(c.history, Snapshot{IsBase: false, Data: data})
	return nil
}

// Recover 加载 base 后按顺序覆盖全部 delta（后写覆盖先写、tombstone 删键），
// 返回重建结果的深拷贝。
func (c *Checkpointer) Recover() (map[string]int64, error) {
	if len(c.history) == 0 {
		return nil, ErrNoBase // 失败不留痕：无任何检查点
	}
	out := map[string]int64{}
	for _, sn := range c.history { // history 天然按检查点时间有序
		for k, ch := range sn.Data {
			if ch.Deleted {
				delete(out, k)
			} else {
				out[k] = ch.Value // 后写覆盖先写
			}
		}
	}
	return out, nil
}

// History 返回全部检查点内容的深拷贝，供外部观察（不含 scanCount）。
func (c *Checkpointer) History() []Snapshot {
	out := make([]Snapshot, len(c.history))
	for i, sn := range c.history {
		d := make(map[string]Change, len(sn.Data))
		for k, v := range sn.Data {
			d[k] = v
		}
		out[i] = Snapshot{IsBase: sn.IsBase, Data: d}
	}
	return out
}

// SelfCheck 在独立临时实例上核验：delta 只遍历脏键，遍历数不随总键数 m 增长。
// 只返回成败，绝不外泄 scanCount 的数值。
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		st := state.New(m + 1)
		c := New(st)
		for i := 0; i < m; i++ {
			if err := st.Set(fmt.Sprintf("k%d", i), int64(i)); err != nil {
				return err
			}
		}
		if err := c.Checkpoint(); err != nil { // 全量基线，清空脏集
			return err
		}
		if err := st.Set("k0", -1); err != nil { // 只碰 1 个键
			return err
		}
		if err := c.Checkpoint(); err != nil {
			return err
		}
		if c.scanCount > 2 { // 期望恰为脏键数 1；2 为允许的小常数上界
			return fmt.Errorf("snap selfcheck: m=%d scanned %d keys for one dirty key", m, c.scanCount)
		}
	}
	return nil
}
