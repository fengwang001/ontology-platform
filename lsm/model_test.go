package lsm

// 本文件是一个独立编写的朴素模型，用于与正式实现对照随机操作序列。
// 模型刻意使用最直接的数据结构（map + 线性扫描），不复用正式实现的
// 索引、闭包与打分代码，以便发现实现层面的偏差。

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
)

// naivePlan 是朴素模型中的在途计划。
type naivePlan struct {
	level  int
	target int
	kind   PlanKind
	inputs []uint64
	next   []uint64
}

// naive 是朴素模型服务。
type naive struct {
	cfg     Config
	files   map[uint64]FileMeta
	pinned  map[uint64]bool
	lastKey map[int][]byte
	plans   map[uint64]*naivePlan
	nextID  uint64
}

func newNaive(cfg Config) *naive {
	return &naive{
		cfg:     cfg,
		files:   make(map[uint64]FileMeta),
		pinned:  make(map[uint64]bool),
		lastKey: make(map[int][]byte),
		plans:   make(map[uint64]*naivePlan),
		nextID:  1,
	}
}

// levelFiles 线性收集某层文件，按 (Smallest, ID) 排序。
func (n *naive) levelFiles(level int) []FileMeta {
	var out []FileMeta
	for _, f := range n.files {
		if f.Level == level {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// add 朴素登记：线性检查不变量。
func (n *naive) add(f FileMeta) error {
	if err := f.validate(n.cfg.NumLevels); err != nil {
		return err
	}
	if _, dup := n.files[f.ID]; dup {
		return fmt.Errorf("%w: dup id %d", ErrInvalidArgument, f.ID)
	}
	if f.Level > 0 {
		for _, g := range n.files {
			if g.Level != f.Level {
				continue
			}
			if overlaps(g.Smallest, g.Largest, f.Smallest, f.Largest) &&
				!bytes.Equal(g.Largest, f.Smallest) && !bytes.Equal(f.Largest, g.Smallest) {
				return fmt.Errorf("%w: naive overlap", ErrInvariant)
			}
		}
	}
	n.files[f.ID] = f
	return nil
}

// score 朴素精确打分。
func (n *naive) score(level int) *big.Rat {
	if level == 0 {
		var cnt int64
		for _, f := range n.files {
			if f.Level == 0 {
				cnt++
			}
		}
		return big.NewRat(cnt, n.cfg.L0Trigger)
	}
	var total int64
	for _, f := range n.files {
		if f.Level == level {
			total += f.Size
		}
	}
	num := new(big.Rat).SetInt64(total)
	den := new(big.Rat).SetInt(n.cfg.levelTargetBytes(level))
	return num.Quo(num, den)
}

// closure 朴素边界闭合：反复全量扫描直到稳定。
func (n *naive) closure(level int, set map[uint64]bool) error {
	for {
		grown := false
		for _, f := range n.files {
			if f.Level != level || set[f.ID] {
				continue
			}
			for id := range set {
				g := n.files[id]
				if bytes.Equal(f.Smallest, g.Largest) || bytes.Equal(f.Largest, g.Smallest) {
					if n.pinned[f.ID] {
						return errLevelConflict
					}
					set[f.ID] = true
					grown = true
					break
				}
			}
		}
		if !grown {
			return nil
		}
	}
}

// pick 朴素选取。
func (n *naive) pick() (uint64, *naivePlan, error) {
	type cand struct {
		level int
		score *big.Rat
	}
	var cands []cand
	for l := 0; l < n.cfg.NumLevels-1; l++ {
		sc := n.score(l)
		if sc.Cmp(big.NewRat(1, 1)) >= 0 {
			cands = append(cands, cand{l, sc})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if c := cands[i].score.Cmp(cands[j].score); c != 0 {
			return c > 0
		}
		return cands[i].level < cands[j].level
	})
	for _, c := range cands {
		plan, err := n.build(c.level)
		if err == errLevelConflict {
			continue
		}
		if err != nil {
			return 0, nil, err
		}
		if plan == nil {
			continue
		}
		id := n.nextID
		n.nextID++
		n.plans[id] = plan
		for _, fid := range append(append([]uint64{}, plan.inputs...), plan.next...) {
			n.pinned[fid] = true
		}
		return id, plan, nil
	}
	return 0, nil, nil
}

// build 朴素构造单层计划。
func (n *naive) build(level int) (*naivePlan, error) {
	var inputSet map[uint64]bool
	if level == 0 {
		// 起点：编号最小的零层文件。
		var seed *FileMeta
		for _, f := range n.files {
			if f.Level != 0 {
				continue
			}
			if seed == nil || f.ID < seed.ID {
				f := f
				seed = &f
			}
		}
		if seed == nil {
			return nil, nil
		}
		if n.pinned[seed.ID] {
			return nil, errLevelConflict
		}
		inputSet = map[uint64]bool{seed.ID: true}
		// 反复按合并区间扩展（含端点相等）。
		for {
			var in interval
			for id := range inputSet {
				f := n.files[id]
				in.extend(f.Smallest, f.Largest)
			}
			grown := false
			for _, f := range n.files {
				if f.Level != 0 || inputSet[f.ID] {
					continue
				}
				if overlaps(f.Smallest, f.Largest, in.lo, in.hi) {
					if n.pinned[f.ID] {
						return nil, errLevelConflict
					}
					inputSet[f.ID] = true
					grown = true
				}
			}
			if !grown {
				break
			}
		}
	} else {
		files := n.levelFiles(level)
		if len(files) == 0 {
			return nil, nil
		}
		seed := files[0]
		if lk, ok := n.lastKey[level]; ok {
			found := false
			for _, f := range files {
				if bytes.Compare(f.Smallest, lk) > 0 {
					seed = f
					found = true
					break
				}
			}
			if !found {
				seed = files[0] // 回绕
			}
		}
		if n.pinned[seed.ID] {
			return nil, errLevelConflict
		}
		inputSet = map[uint64]bool{seed.ID: true}
		if err := n.closure(level, inputSet); err != nil {
			return nil, err
		}
	}
	// 合并区间。
	var in interval
	var inputs []uint64
	for id := range inputSet {
		f := n.files[id]
		in.extend(f.Smallest, f.Largest)
		inputs = append(inputs, id)
	}
	// 下一层重叠（含端点相等）。
	nextSet := make(map[uint64]bool)
	for _, f := range n.files {
		if f.Level != level+1 {
			continue
		}
		if overlaps(f.Smallest, f.Largest, in.lo, in.hi) {
			if n.pinned[f.ID] {
				return nil, errLevelConflict
			}
			nextSet[f.ID] = true
		}
	}
	if err := n.closure(level+1, nextSet); err != nil {
		return nil, err
	}
	var next []uint64
	for id := range nextSet {
		next = append(next, id)
	}
	sortIDs := func(s []uint64) { sort.Slice(s, func(i, j int) bool { return s[i] < s[j] }) }
	sortIDs(inputs)
	sortIDs(next)
	kind := PlanRewrite
	if len(inputs) == 1 && len(next) == 0 {
		kind = PlanMove
	}
	return &naivePlan{level: level, target: level + 1, kind: kind, inputs: inputs, next: next}, nil
}

// install 朴素安装，错误优先级与正式实现一致。
func (n *naive) install(planID uint64, outputs []FileMeta) error {
	plan, ok := n.plans[planID]
	// 1. 参数非法。
	seen := make(map[uint64]bool)
	for _, o := range outputs {
		if err := o.validate(n.cfg.NumLevels); err != nil {
			return err
		}
		if seen[o.ID] {
			return fmt.Errorf("%w: dup output id", ErrInvalidArgument)
		}
		seen[o.ID] = true
		if ok && o.Level != plan.target {
			return fmt.Errorf("%w: wrong target level", ErrInvalidArgument)
		}
	}
	// 2. 文件已被占用 / 编号冲突。
	for _, o := range outputs {
		if _, exists := n.files[o.ID]; !exists {
			continue
		}
		isInput := false
		if ok {
			for _, id := range append(append([]uint64{}, plan.inputs...), plan.next...) {
				if id == o.ID {
					isInput = true
				}
			}
		}
		if isInput {
			continue
		}
		if n.pinned[o.ID] {
			return fmt.Errorf("%w: naive pinned", ErrFilePinned)
		}
		return fmt.Errorf("%w: naive id collision", ErrInvalidArgument)
	}
	// 3. 计划不存在。
	if !ok {
		return fmt.Errorf("%w: naive plan %d", ErrPlanNotFound, planID)
	}
	// 4. 层不变量：输出互相之间、以及与目标层未被消耗文件之间。
	consumed := make(map[uint64]bool)
	for _, id := range plan.next {
		consumed[id] = true
	}
	var all []FileMeta
	for _, f := range n.files {
		if f.Level == plan.target && !consumed[f.ID] {
			all = append(all, f)
		}
	}
	all = append(all, outputs...)
	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })
	for i := 1; i < len(all); i++ {
		if all[i-1].ID == all[i].ID {
			continue
		}
		if bytes.Compare(all[i-1].Largest, all[i].Smallest) > 0 {
			return fmt.Errorf("%w: naive install overlap", ErrInvariant)
		}
	}
	// 生效前先从源层输入算出本次压实终点键。
	var maxKey []byte
	if plan.level > 0 {
		for _, id := range plan.inputs {
			f := n.files[id]
			if maxKey == nil || bytes.Compare(f.Largest, maxKey) > 0 {
				maxKey = f.Largest
			}
		}
	}
	// 生效：删除输入、加入输出、释放占用、更新终点键。
	for _, id := range plan.inputs {
		delete(n.files, id)
		delete(n.pinned, id)
	}
	for _, id := range plan.next {
		delete(n.files, id)
		delete(n.pinned, id)
	}
	for _, o := range outputs {
		n.files[o.ID] = o
	}
	if plan.level > 0 {
		n.lastKey[plan.level] = maxKey
	}
	delete(n.plans, planID)
	return nil
}

// cancel 朴素取消。
func (n *naive) cancel(planID uint64) error {
	plan, ok := n.plans[planID]
	if !ok {
		return fmt.Errorf("%w: naive plan %d", ErrPlanNotFound, planID)
	}
	for _, id := range append(append([]uint64{}, plan.inputs...), plan.next...) {
		delete(n.pinned, id)
	}
	delete(n.plans, planID)
	return nil
}
