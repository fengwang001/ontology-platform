package shallow

import "context"

// deepenPlan 是一次深化在本地暂存区中累积的结果；
// 只有全部拉取成功后才整体并入仓库状态，任何失败都直接丢弃暂存区，
// 因而边界、对象集合、引用与序号都保持原状。
type deepenPlan struct {
	commits map[CommitID]Commit
	blobs   map[BlobID]Blob
	// frontier 中是新视图包含的全部提交（原有可遍历提交 + 新拉取提交）。
	frontier map[CommitID]struct{}
	// boundary 为可选的显式边界集合（时刻深化按停止条件给出）；
	// 为 nil 时按「frontier 中提交至少一个父不在 frontier」重算。
	boundary map[CommitID]struct{}
}

func newPlan() *deepenPlan {
	return &deepenPlan{
		commits:  map[CommitID]Commit{},
		blobs:    map[BlobID]Blob{},
		frontier: map[CommitID]struct{}{},
	}
}

// Deepen 按深度深化：从每个引用出发沿父关系每路径最多包含 depth 个提交。
func (r *Repo) Deepen(ctx context.Context, depth int) error {
	if depth <= 0 {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkRefsLocked(); err != nil {
		return err
	}
	// 目标深度不大于任一引用的已持有深度：零副作用成功。
	// 已持有深度 = 从引用到边界（含边界）的最浅层数；无界为无限。
	if r.isDepthCoveredLocked(depth) {
		return nil
	}

	plan := newPlan()
	for _, tip := range r.state.refs {
		plan.frontier[tip] = struct{}{}
	}
	level := 1
	current := r.refTipsLocked()
	for _, id := range current {
		if _, err := r.ensureCommitLocked(ctx, plan, id); err != nil {
			return err
		}
	}
	for level < depth && len(current) > 0 {
		var next []CommitID
		for _, id := range current {
			cm, err := r.ensureCommitLocked(ctx, plan, id)
			if err != nil {
				return err
			}
			for _, p := range cm.Parents {
				if _, seen := plan.frontier[p]; seen {
					continue
				}
				plan.frontier[p] = struct{}{}
				next = append(next, p)
			}
		}
		for _, id := range next {
			if _, err := r.ensureCommitLocked(ctx, plan, id); err != nil {
				return err
			}
		}
		current = next
		level++
	}
	r.commitPlanLocked(plan)
	return nil
}

// DeepenSince 按时刻深化：包含创建时刻不早于 t 的祖先，
// 每条路径遇到更早的提交即独立停止且该提交不包含。
func (r *Repo) DeepenSince(ctx context.Context, t int64) error {
	if t < 0 {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkRefsLocked(); err != nil {
		return err
	}

	plan := newPlan()
	type frame struct {
		id CommitID
	}
	var stack []frame
	enqueued := map[CommitID]struct{}{}
	for _, tip := range r.refTipsLocked() {
		stack = append(stack, frame{id: tip})
		enqueued[tip] = struct{}{}
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		cm, err := r.ensureCommitLocked(ctx, plan, f.id)
		if err != nil {
			return err
		}
		if cm.CreatedAt < t {
			// 路径独立停止：该提交不包含（从未进入 frontier），也不向上展开。
			continue
		}
		plan.frontier[f.id] = struct{}{}
		for _, p := range cm.Parents {
			if _, seen := enqueued[p]; seen {
				continue
			}
			enqueued[p] = struct{}{}
			stack = append(stack, frame{id: p})
		}
	}
	// 时刻深化的边界：视图内提交若有父不在视图（该路径在该父处因时刻停止）。
	plan.boundary = map[CommitID]struct{}{}
	for id := range plan.frontier {
		cm := r.commitInLocked(plan, id)
		for _, p := range cm.Parents {
			if _, inView := plan.frontier[p]; !inView {
				plan.boundary[id] = struct{}{}
				break
			}
		}
	}
	r.commitPlanLocked(plan)
	return nil
}

// Unshallow 拉取全部祖先，边界变为空集。
func (r *Repo) Unshallow(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkRefsLocked(); err != nil {
		return err
	}
	if len(r.state.boundary) == 0 {
		return nil
	}

	plan := newPlan()
	var queue []CommitID
	for _, tip := range r.refTipsLocked() {
		plan.frontier[tip] = struct{}{}
		queue = append(queue, tip)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		cm, err := r.ensureCommitLocked(ctx, plan, id)
		if err != nil {
			return err
		}
		for _, p := range cm.Parents {
			if _, seen := plan.frontier[p]; seen {
				continue
			}
			plan.frontier[p] = struct{}{}
			queue = append(queue, p)
		}
	}
	r.commitPlanLocked(plan)
	return nil
}

// refTipsLocked 返回去重后的引用尖端列表。
func (r *Repo) refTipsLocked() []CommitID {
	seen := map[CommitID]struct{}{}
	var tips []CommitID
	for _, tip := range r.state.refs {
		if _, ok := seen[tip]; ok {
			continue
		}
		seen[tip] = struct{}{}
		tips = append(tips, tip)
	}
	return tips
}

// checkRefsLocked 按错误次序校验：引用不存在优先于远端错误与状态非法。
func (r *Repo) checkRefsLocked() error {
	for _, tip := range r.state.refs {
		if _, ok := r.state.commits[tip]; !ok {
			return ErrRefNotFound
		}
	}
	return nil
}

// ensureCommitLocked 保证 id 的提交数据在「本地或暂存区」可用，
// 必要时以提交为单位原子拉取：内容对象全部到位或全部丢弃。
func (r *Repo) ensureCommitLocked(ctx context.Context, plan *deepenPlan, id CommitID) (Commit, error) {
	if cm := r.commitInLocked(plan, id); cm.ID != "" {
		return cm, nil
	}
	cm, blobs, err := r.remote.FetchCommit(ctx, id)
	if err != nil {
		return Commit{}, err // ErrRemoteMissing / ErrRemoteFetch 原样上抛
	}
	if cm.ID != id {
		return Commit{}, ErrRemoteMissing
	}
	plan.commits[id] = cm
	for _, b := range blobs {
		plan.blobs[b.ID] = b
	}
	return cm, nil
}

// commitInLocked 在本地或暂存区查找提交数据（不触发拉取）。
func (r *Repo) commitInLocked(plan *deepenPlan, id CommitID) Commit {
	if cm, ok := r.state.commits[id]; ok {
		// 本地提交即便恰好是某边界父，也直接使用其本地数据；
		// 是否跨越浅边界完全由 frontier 的推进结果决定。
		return cm
	}
	if cm, ok := plan.commits[id]; ok {
		return cm
	}
	return Commit{}
}

// commitPlanLocked 将暂存区整体并入：先复制状态（失败路径根本不会走到这里），
// 再按「新视图中存在且至少一个父不存在」重算边界。
func (r *Repo) commitPlanLocked(plan *deepenPlan) {
	next := r.state.clone()
	changed := false
	for id := range plan.frontier {
		cm, ok := plan.commits[id]
		if !ok {
			continue // 本地已有提交不需要从暂存区并入
		}
		if _, ok := next.commits[id]; ok {
			continue
		}
		next.commits[id] = cm
		changed = true
		for _, b := range cm.Blobs {
			next.blobRef[b]++
		}
	}
	// 只有被纳入新视图的提交所引用的内容对象才落地；
	// 时刻深化路径上被停止的提交不包含，其内容对象也一并丢弃。
	for id := range plan.frontier {
		cm, ok := plan.commits[id]
		if !ok {
			continue
		}
		for _, bid := range cm.Blobs {
			if b, ok := plan.blobs[bid]; ok {
				if _, exist := next.blobs[bid]; !exist {
					next.blobs[bid] = b
				}
			}
		}
	}

	// 新边界：优先用显式边界（时刻深化）；否则取新视图中至少有一个父
	// 不在新视图的提交。深化只能推进边界。
	newBoundary := map[CommitID]struct{}{}
	if plan.boundary != nil {
		for id := range plan.boundary {
			if _, ok := next.commits[id]; ok {
				newBoundary[id] = struct{}{}
			}
		}
	} else {
		for id := range plan.frontier {
			cm, ok := next.commits[id]
			if !ok {
				continue
			}
			for _, p := range cm.Parents {
				if _, inView := plan.frontier[p]; !inView {
					newBoundary[id] = struct{}{}
					break
				}
			}
		}
	}
	// 边界只能向远端推进、绝不能回收：遍历未触及的路径上旧边界继续生效。
	// 凡旧边界提交仍有父不在新视图中，就保留为边界（父在本地但不在视图
	// 同样算截断，与「边界父碰巧在本地仍视为不存在」一致）。
	for id := range next.boundary {
		if _, ok := next.commits[id]; !ok {
			continue
		}
		cm := next.commits[id]
		need := false
		for _, p := range cm.Parents {
			if _, inView := plan.frontier[p]; !inView {
				need = true
				break
			}
		}
		if need {
			newBoundary[id] = struct{}{}
		}
	}
	if len(newBoundary) != len(next.boundary) {
		changed = true
	} else {
		for id := range newBoundary {
			if _, ok := next.boundary[id]; !ok {
				changed = true
				break
			}
		}
	}
	next.boundary = newBoundary
	if changed {
		r.state = next
		r.reach = nil
		r.opSeq++
	}
}

// isDepthCoveredLocked 判断 depth 是否已被当前所有引用的已持有深度覆盖。
func (r *Repo) isDepthCoveredLocked(depth int) bool {
	if len(r.state.refs) == 0 {
		return true
	}
	for _, tip := range r.refTipsLocked() {
		// BFS 求引用到任一浅边界提交（含边界）的最小层数；
		// 若遍历结束都未遇到边界（例如已是根提交），则该引用持有深度为无限。
		dist := map[CommitID]int{tip: 1}
		queue := []CommitID{tip}
		held := -1
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if _, b := r.state.boundary[id]; b {
				held = dist[id]
				break
			}
			cm := r.state.commits[id]
			for _, p := range cm.Parents {
				if _, seen := dist[p]; seen {
					continue
				}
				dist[p] = dist[id] + 1
				queue = append(queue, p)
			}
		}
		if held >= 0 && held < depth {
			return false
		}
	}
	return true
}
