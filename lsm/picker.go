package lsm

import (
	"errors"
	"fmt"
	"sort"
)

// errLevelConflict 内部信号：本层在选取过程中触碰了被在途计划占用的文件，
// 该层本次放弃，改选分数次高的层。放弃不留下任何占用或起点记录。
var errLevelConflict = errors.New("lsm: level conflicts with in-flight plan")

// levelCandidate 是一个分数不小于一、可参与选择的层。
type levelCandidate struct {
	level int
	score score
}

// pickLocked 计算本次压实计划。没有任何层达标时返回 (nil, nil)，这是正常结果。
// 调用方必须持有锁。
func (s *Service) pickLocked() (*Plan, error) {
	cands := s.candidates()
	for _, c := range cands {
		plan, err := s.buildPlan(c)
		if err == errLevelConflict {
			continue
		}
		if err != nil {
			return nil, err
		}
		if plan == nil {
			continue
		}
		s.adoptPlan(plan)
		return plan, nil
	}
	return nil, nil
}

// candidates 返回所有分数不小于一的层（最后一层不参与），
// 按分数降序、层号升序排列，分数比较使用精确有理数。
func (s *Service) candidates() []levelCandidate {
	var cands []levelCandidate
	for l := 0; l < s.cfg.NumLevels-1; l++ {
		sc := s.levelScore(l)
		if sc.atLeastOne() {
			cands = append(cands, levelCandidate{level: l, score: sc})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if c := cands[i].score.cmp(cands[j].score); c != 0 {
			return c > 0
		}
		return cands[i].level < cands[j].level
	})
	return cands
}

// levelScore 计算第 l 层的精确分数。
func (s *Service) levelScore(l int) score {
	lv := &s.levels[l]
	if l == 0 {
		return l0Score(int64(lv.count()), s.cfg.L0Trigger)
	}
	return levelScore(lv.totalBytes, s.cfg.levelTargetBytes(l))
}

// buildPlan 为指定层构造计划；触碰被占用文件时返回 errLevelConflict。
func (s *Service) buildPlan(c levelCandidate) (*Plan, error) {
	l := c.level
	var inputs []FileMeta
	var seedReason string
	var err error
	if l == 0 {
		inputs, seedReason, err = s.l0Inputs()
	} else {
		inputs, seedReason, err = s.nonL0Inputs(l)
	}
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, nil
	}

	in := inputInterval(inputs)

	// 纳入下一层所有与输入合并区间有重叠（含端点相等）的文件。
	next, err := s.overlapPinned(l+1, in)
	if err != nil {
		return nil, err
	}
	// 对下一层的输入同样做边界闭合；闭合带来的区间扩大不反过来改变本层输入。
	next, err = s.closeSet(l+1, next)
	if err != nil {
		return nil, err
	}

	kind := PlanRewrite
	if len(inputs) == 1 && len(next) == 0 {
		// 直接下移：下一层没有任何重叠文件，区间在目标层天然满足层不变量。
		kind = PlanMove
	}

	plan := &Plan{
		Level:       l,
		TargetLevel: l + 1,
		Kind:        kind,
		Inputs:      inputs,
		NextInputs:  next,
		Score:       c.score.String(),
	}
	plan.Reason = fmt.Sprintf(
		"level %d score %s >= 1; %s; inputs=%v merged=[%x,%x]; next-level %d overlap+closure=%v; kind=%s",
		l, c.score.String(), seedReason, fileIDs(inputs), in.lo, in.hi, l+1, fileIDs(next), kind)
	return plan, nil
}

// l0Inputs 零层起点与扩展：从编号最小的零层文件出发，
// 反复纳入所有与当前已选文件合并区间有重叠（含端点相等）的零层文件，
// 直到不再增加。零层的扩展规则已涵盖端点相等，故边界闭合被自然包含。
func (s *Service) l0Inputs() ([]FileMeta, string, error) {
	lv := &s.levels[0]
	if len(lv.l0Files) == 0 {
		return nil, "", nil
	}
	seed := lv.l0Files[0] // 编号最小
	if s.isPinned(seed.ID) {
		return nil, "", errLevelConflict
	}
	selected := map[uint64]bool{seed.ID: true}
	out := []FileMeta{seed}
	for {
		in := inputInterval(out)
		grown := false
		for _, f := range lv.l0Files {
			if selected[f.ID] {
				continue
			}
			if overlaps(f.Smallest, f.Largest, in.lo, in.hi) {
				if s.isPinned(f.ID) {
					return nil, "", errLevelConflict
				}
				selected[f.ID] = true
				out = append(out, f)
				grown = true
			}
		}
		if !grown {
			break
		}
	}
	// out 按发现顺序追加，统一按 ID 升序返回以保证可复现。
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	reason := fmt.Sprintf("L0 seed=file %d (smallest ID), expanded by overlap(inclusive) to %d files", seed.ID, len(out))
	return out, reason, nil
}

// nonL0Inputs 非零层起点与边界闭合：
// 起点是上次压实终点键之后（严格大于）区间最小值最小的文件；
// 不存在则回绕到本层区间最小值最小的文件；从未压实过则从最小者开始。
// 随后做边界闭合：凡与集合内文件端点相接（最大值等于最小值或反之）
// 的同层文件都必须纳入，反复闭合到稳定。
func (s *Service) nonL0Inputs(l int) ([]FileMeta, string, error) {
	lv := &s.levels[l]
	if lv.count() == 0 {
		return nil, "", nil
	}
	var seed FileMeta
	var reason string
	if lv.hasLastKey {
		if f, ok := lv.ix.firstAfter(lv.lastKey); ok {
			seed = f
			reason = fmt.Sprintf("seed=file %d (smallest min-key strictly after last compacted key %x)", f.ID, lv.lastKey)
		} else {
			seed = lv.ix.files[0]
			reason = fmt.Sprintf("seed=file %d (wraparound: no min-key after last compacted key %x, restarted at smallest)", seed.ID, lv.lastKey)
		}
	} else {
		seed = lv.ix.files[0]
		reason = fmt.Sprintf("seed=file %d (level never compacted, smallest min-key)", seed.ID)
	}
	if s.isPinned(seed.ID) {
		return nil, "", errLevelConflict
	}
	set, err := s.closeSet(l, []FileMeta{seed})
	if err != nil {
		return nil, "", err
	}
	return set, reason + fmt.Sprintf(", boundary closure grew to %d files", len(set)), nil
}

// overlapPinned 返回第 l 层与区间 in 重叠（含端点相等）的全部文件，
// 任一文件被占用则返回 errLevelConflict。
func (s *Service) overlapPinned(l int, in interval) ([]FileMeta, error) {
	files := s.levels[l].ix.overlap(in.lo, in.hi)
	for _, f := range files {
		if s.isPinned(f.ID) {
			return nil, errLevelConflict
		}
	}
	return files, nil
}

// closeSet 对第 l 层的文件集合做边界闭合：
// 若集合内某文件的最大值恰等于同层不在集合内某文件的最小值，
// 或集合内某文件的最小值恰等于同层不在集合内某文件的最大值，
// 则把该文件纳入，反复闭合到稳定。触碰被占用文件返回 errLevelConflict。
// 返回的集合按 (Smallest, Largest, ID) 升序排列。
func (s *Service) closeSet(l int, seed []FileMeta) ([]FileMeta, error) {
	lv := &s.levels[l]
	inSet := make(map[uint64]bool, len(seed))
	members := make([]FileMeta, 0, len(seed))
	for _, f := range seed {
		if !inSet[f.ID] {
			inSet[f.ID] = true
			members = append(members, f)
		}
	}
	for i := 0; i < len(members); i++ {
		f := members[i]
		// 与 f 端点相接的同层文件：Smallest == f.Largest 或 Largest == f.Smallest。
		neighbors := lv.ix.withMin(f.Largest)
		neighbors = append(neighbors, lv.ix.withMax(f.Smallest)...)
		for _, g := range neighbors {
			if inSet[g.ID] {
				continue
			}
			if s.isPinned(g.ID) {
				return nil, errLevelConflict
			}
			inSet[g.ID] = true
			members = append(members, g)
		}
	}
	sort.Slice(members, func(i, j int) bool { return less(members[i], members[j]) })
	return members, nil
}
