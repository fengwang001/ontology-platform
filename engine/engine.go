package engine

import (
	"errors"
	"sync"

	"ontology/model"
)

type Status string

const (
	Running   Status = "Running"
	Completed Status = "Completed"
	Stuck     Status = "Stuck"
)

var ErrNoActive = errors.New("engine: task has no active token")

// JoinStat 给出一个汇合节点的触发次数。
type JoinStat struct {
	Node  int
	Fires int
}

// State 是 Status 的只读快照。
type State struct {
	Status   Status
	EndCount int
	Joins    []JoinStat
}

// Instance 是版本固定后的可执行实例（由 repo.Start 构造）。
type Instance struct {
	mu sync.Mutex

	n    int
	kind []model.NodeType

	// act[t]：Task t 上的活动令牌数。
	act []int
	// arr[v][i]：汇合 v 的第 i 条入边上的到达令牌数。
	arr [][]int
	// ein[v]：汇合 v 的各入边来源（结构入边，按加入顺序）。
	ein [][]int

	fires    []int // 汇合触发次数（所有节点索引，非汇合恒 0）
	endCount int

	// 有效图（裁剪后）邻接与传递闭包（位图）：canReach[u] 为 u 可达节点集合。
	eout     [][]int
	canReach []uint64

	// 非导出探针：OrJoin 判定时考察的"其它令牌所在节点"次数。
	orProbe int
}

// Status 返回当前只读状态。
func (in *Instance) Status() State {
	in.mu.Lock()
	defer in.mu.Unlock()

	st := State{Status: in.classify(), EndCount: in.endCount}
	for v := 1; v <= in.n; v++ {
		if in.kind[v] == model.AndJoin || in.kind[v] == model.OrJoin {
			st.Joins = append(st.Joins, JoinStat{Node: v, Fires: in.fires[v]})
		}
	}
	return st
}
