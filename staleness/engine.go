package staleness

import (
	"fmt"
	"sort"
	"sync"
)

// asset 是图中的一个资产定义及其全部分区状态。
type asset struct {
	name       string
	first      int
	last       int
	edges      []edge // 上游依赖边
	depth      int    // 源为 0，其余为上游层深最大值加一
	partitions map[int]*partition
}

// edge 表示上游 upstream 到本资产的一条依赖，分区 d 读取 [d+lo, d+hi]。
type edge struct {
	upstream *asset
	lo       int
	hi       int
}

// partition 是单个分区的物化状态。
type partition struct {
	version      int
	materialized bool
	// consumed 记录最近一次成功物化开始时全部输入分区此刻的版本：
	// 上游资产名 -> 输入分区号 -> 消费版本。
	consumed map[string]map[int]int
	running  bool
}

// run 是一次进行中的物化运行。
type run struct {
	id       int
	asset    *asset
	part     int
	consumed map[string]map[int]int
	active   bool
}

// Engine 持有资产图与全部分区状态，所有公开方法均可并发调用，
// 每次判定基于同一把互斥锁保护下的一致快照。
type Engine struct {
	mu      sync.Mutex
	assets  map[string]*asset
	runs    map[int]*run
	nextRun int
}

// lookup 校验资产存在且分区在声明范围内。
func (e *Engine) lookup(assetName string, part int) (*asset, error) {
	a, ok := e.assets[assetName]
	if !ok {
		return nil, newError(KindUnknownAsset, fmt.Sprintf("未知资产 %q", assetName))
	}
	if part < a.first || part > a.last {
		return nil, newError(KindPartitionOutOfRange,
			fmt.Sprintf("资产 %q 的分区 %d 不在声明范围 [%d,%d] 内", assetName, part, a.first, a.last))
	}
	return a, nil
}

// stateOf 返回分区状态，未触碰过的分区返回零值（未物化、版本 0）。
func (a *asset) stateOf(part int) *partition {
	p, ok := a.partitions[part]
	if !ok {
		p = &partition{}
		a.partitions[part] = p
	}
	return p
}

// peek 只读地返回分区状态，未触碰过时返回 nil。
func (a *asset) peek(part int) *partition {
	return a.partitions[part]
}

// inputs 返回分区 d 的全部输入分区：对每条上游边取 [d+lo, d+hi] 闭区间，
// 落在上游声明范围外的忽略。结果按（资产名，分区号）升序，保证确定性。
func (e *Engine) inputs(a *asset, d int) []PartitionRef {
	var refs []PartitionRef
	for _, eg := range a.edges {
		for k := eg.lo; k <= eg.hi; k++ {
			p := d + k
			if p >= eg.upstream.first && p <= eg.upstream.last {
				refs = append(refs, PartitionRef{Asset: eg.upstream.name, Partition: p})
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Asset != refs[j].Asset {
			return refs[i].Asset < refs[j].Asset
		}
		return refs[i].Partition < refs[j].Partition
	})
	return refs
}

// ExternalWrite 对源资产分区做外部写入：版本加一，首次写入即视为已物化。
func (e *Engine) ExternalWrite(assetName string, part int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, err := e.lookup(assetName, part)
	if err != nil {
		return err
	}
	if len(a.edges) > 0 {
		return newError(KindNonSourceExternalWrite,
			fmt.Sprintf("资产 %q 有上游依赖，不是源，禁止外部写入", assetName))
	}
	p := a.stateOf(part)
	p.version++
	p.materialized = true
	return nil
}

// StartRun 开始一次物化运行，记录全部输入分区此刻的版本，返回运行号。
func (e *Engine) StartRun(assetName string, part int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, err := e.lookup(assetName, part)
	if err != nil {
		return 0, err
	}
	p := a.stateOf(part)
	if p.running {
		return 0, newError(KindAlreadyRunning,
			fmt.Sprintf("资产 %q 的分区 %d 已有运行中的物化", assetName, part))
	}
	inputs := e.inputs(a, part)
	var missing, stale []PartitionRef
	for _, ref := range inputs {
		up := e.assets[ref.Asset]
		upState := up.peek(ref.Partition)
		switch {
		case upState == nil || !upState.materialized:
			missing = append(missing, ref)
		case e.staleLocked(up, ref.Partition, make(map[PartitionRef]bool)):
			stale = append(stale, ref)
		}
	}
	if len(missing) > 0 || len(stale) > 0 {
		return 0, &Error{
			Kind: KindInputsNotReady,
			Message: fmt.Sprintf("资产 %q 的分区 %d 输入未就绪：缺失 %v，过期 %v",
				assetName, part, missing, stale),
			Missing: missing,
			Stale:   stale,
		}
	}
	consumed := make(map[string]map[int]int, len(inputs))
	for _, ref := range inputs {
		up := e.assets[ref.Asset]
		m, ok := consumed[ref.Asset]
		if !ok {
			m = make(map[int]int)
			consumed[ref.Asset] = m
		}
		m[ref.Partition] = up.peek(ref.Partition).version
	}
	e.nextRun++
	id := e.nextRun
	e.runs[id] = &run{id: id, asset: a, part: part, consumed: consumed, active: true}
	p.running = true
	return id, nil
}

// CompleteRun 结束一次运行。成功则分区版本加一并以开始时的记录作为消费版本；
// 失败不改变任何状态。运行号不存在或已结束时拒绝。
func (e *Engine) CompleteRun(runID int, success bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.runs[runID]
	if !ok || !r.active {
		return newError(KindRunNotFound, fmt.Sprintf("运行号 %d 不存在或已结束", runID))
	}
	r.active = false
	p := r.asset.stateOf(r.part)
	p.running = false
	if success {
		p.version++
		p.materialized = true
		p.consumed = r.consumed
	}
	return nil
}
