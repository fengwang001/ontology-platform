package replay

import (
	"sync"

	"ontology/code"
	"ontology/history"
)

// Runner 在单个 history.Store 上执行确定性重放。
//
// Runner 内部还维护一个非导出的“比较次数”计数器（按工作流隔离）：
// 每次成功 Run 增加“消费条数 + 续跑条数”。它只随本工作流的成功运行
// 增长，不随其他工作流的历史增长。
type Runner struct {
	store *history.Store

	cmpMu sync.Mutex
	cmp   map[string]int64
}

// NewRunner 创建绑定到 store 的 Runner。
func NewRunner(store *history.Store) *Runner {
	return &Runner{store: store, cmp: make(map[string]int64)}
}

// Run 对工作流 wf 执行代码 program：
//
//  1. 先取一次 Snapshot（长度 L），随后所有比对只基于该快照；
//  2. 顺序执行代码，消费历史或生成续跑事件，遇第一个非确定性错误即返回；
//  3. 成功且续跑非空时，以 Append(wf, L, 新事件) 一次写入：
//     并发抢先 → history.ErrConflict，超长 → history.ErrCapacity，本次不留事件；
//  4. 任何错误都不追加事件。
//
// 成功返回 (消费条数, 续跑条数)。
func (r *Runner) Run(wf []byte, program code.Code) (consumed int, continued int, err error) {
	if len(wf) == 0 {
		return 0, 0, ErrArgument
	}
	if err = program.Validate(); err != nil {
		return 0, 0, err
	}

	snap := r.store.Snapshot(wf)
	res, err := newSimulator(snap).run(program)
	if err != nil {
		return 0, 0, err
	}

	if len(res.newEvents) > 0 {
		if err = r.store.Append(wf, len(snap), res.newEvents); err != nil {
			return 0, 0, err
		}
	}

	r.addComparisons(wf, int64(res.consumed+len(res.newEvents)))
	return res.consumed, len(res.newEvents), nil
}

func (r *Runner) addComparisons(wf []byte, delta int64) {
	r.cmpMu.Lock()
	r.cmp[string(wf)] += delta
	r.cmpMu.Unlock()
}

// comparisons 返回工作流 wf 累计的比较次数（非导出计数器，供包内测试读取）。
func (r *Runner) comparisons(wf []byte) int64 {
	r.cmpMu.Lock()
	defer r.cmpMu.Unlock()
	return r.cmp[string(wf)]
}
