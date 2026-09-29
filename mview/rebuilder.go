// Package mview 提供带检查点的物化视图幂等重建器。
//
// 视图由一个有序源序列按序计数派生（值 -> 出现次数）。重建过程按固定
// 块大小分块推进，每完成一块写入一次检查点；崩溃只可能发生在块之间，
// 恢复后从上一检查点继续。重建期间旧视图保持可读，重建结果写入不可见
// 的影子副本，全部处理完并提交时才原子切换并递增代数。
package mview

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 判定具体原因。
var (
	// ErrInvalidChunkSize 表示块大小非法（必须 >= 1），或恢复时传入的
	// 块大小与检查点中保存的不一致。
	ErrInvalidChunkSize = errors.New("mview: invalid chunk size")
	// ErrRebuildInProgress 表示已有重建在进行，不能再次开始。
	ErrRebuildInProgress = errors.New("mview: rebuild already in progress")
	// ErrNoRebuild 表示当前没有进行中的重建，却执行了步进/提交/崩溃。
	ErrNoRebuild = errors.New("mview: no rebuild in progress")
	// ErrRebuildIncomplete 表示尚未处理完全部源元素就尝试提交。
	ErrRebuildIncomplete = errors.New("mview: rebuild is not complete")
)

// View 是某一代物化视图的只读快照：值 -> 出现次数。
// 返回的 map 是副本，调用方可自由修改。
type View map[string]int

func cloneView(v View) View {
	if v == nil {
		return nil
	}
	c := make(View, len(v))
	for key, count := range v {
		c[key] = count
	}
	return c
}

// Status 描述重建器某一时刻的可观测状态，供查询与测试日志使用。
type Status struct {
	// Generation 是当前已提交视图的代数，首个视图为第 1 代。
	Generation int64
	// View 是当前已提交视图的副本。
	View View
	// Rebuilding 表示是否存在进行中的重建。
	Rebuilding bool
	// ChunkSize 是进行中重建使用的块大小；无重建时为 0。
	ChunkSize int
	// Processed 是影子重建已处理的源元素数（含检查点内的进度）。
	Processed int
	// Shadow 是影子副本的内容；未重建时为 nil。
	Shadow View
}

// Rebuilder 是带检查点的幂等物化视图重建器。零值不可用，须用 New 创建。
// 所有方法均为并发安全；读操作在重建期间只会看到某个完整提交边界。
type Rebuilder struct {
	mu sync.RWMutex

	// 已提交状态：读操作（Snapshot）在任何时刻只引用这一对字段，
	// 因此重建期间读到的必然是某个完整提交边界。
	view       View
	generation int64

	// 进行中重建的影子状态。rebuilding 为 false 时其余字段均无意义。
	rebuilding bool
	crashed    bool
	source     []string
	chunkSize  int
	processed  int
	shadow     View
}

// New 创建重建器，并基于 source 同步物化第 1 代视图。
func New(source []string) *Rebuilder {
	r := &Rebuilder{
		view:       Replay(source),
		generation: 1,
	}
	return r
}

// Replay 从头重放有序源序列，生成与重建结果应当一致的视图。
func Replay(source []string) View {
	v := make(View)
	for _, key := range source {
		v[key]++
	}
	return v
}

// BeginRebuild 基于新源序列开始一次分块重建。
// 若当前已有重建则返回 ErrRebuildInProgress；chunkSize < 1 返回
// ErrInvalidChunkSize。失败不改变任何状态。
func (r *Rebuilder) BeginRebuild(source []string, chunkSize int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if chunkSize < 1 {
		return ErrInvalidChunkSize
	}
	if r.rebuilding {
		// 含崩溃后待恢复的重建：Begin 不是恢复手段，只能继续步进/提交，
		// 或再次 Crash 后放弃（语义上崩溃不取消重建）。
		return ErrRebuildInProgress
	}

	src := append([]string(nil), source...)
	r.rebuilding = true
	r.crashed = false
	r.source = src
	r.chunkSize = chunkSize
	r.processed = 0
	r.shadow = make(View)
	return nil
}

// Step 处理下一块并在成功后写入检查点。
// 若全部块已处理完则返回 false（检查点保持不变，仍需 Commit）。
// 没有进行中的重建时返回 ErrNoRebuild。
func (r *Rebuilder) Step() (advanced bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.rebuilding {
		return false, ErrNoRebuild
	}

	if r.crashed {
		// 恢复：从上一检查点继续。当前未完成块已随崩溃丢弃，
		// processed/shadow 都停在上一块边界，直接取下一块即可。
		r.crashed = false
	}

	if r.processed >= len(r.source) {
		// 全部块均已处理并落检查点，重复步进不改变状态。
		return false, nil
	}

	end := r.processed + r.chunkSize
	if end > len(r.source) {
		end = len(r.source)
	}
	// 先在局部缓冲中处理整块；仅当整块成功后才写入影子副本与进度，
	// 模拟“检查点只在块之间落盘”：崩溃时本块不会留下半成品。
	delta := make(View, end-r.processed)
	for i := r.processed; i < end; i++ {
		delta[r.source[i]]++
	}
	for key, count := range delta {
		r.shadow[key] += count
	}
	r.processed = end
	return true, nil
}

// Commit 在全部源元素处理完后原子切换到影子视图并递增代数。
// 未重建返回 ErrNoRebuild；未处理完返回 ErrRebuildIncomplete。
// 失败不改变任何状态（进行中的重建仍然保留，可继续步进或崩溃恢复）。
func (r *Rebuilder) Commit() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.rebuilding {
		return ErrNoRebuild
	}
	if r.crashed || r.processed < len(r.source) {
		// 未处理完（含崩溃后尚未恢复）必须整体拒绝，状态保持不变。
		return ErrRebuildIncomplete
	}

	// 一次加锁内完成指针切换与代数递增，对并发读者表现为原子操作：
	// 读者要么看到旧视图与旧代数，要么看到新视图与新代数。
	r.view = r.shadow
	r.generation++
	r.rebuilding = false
	r.crashed = false
	r.source = nil
	r.chunkSize = 0
	r.processed = 0
	r.shadow = nil
	return nil
}

// Crash 模拟在块之间发生崩溃：丢弃当前未完成块之后的全部易失进度，
// 重建标记保留为“待恢复”，旧视图与代数不变。
// 没有进行中的重建时返回 ErrNoRebuild。
func (r *Rebuilder) Crash() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.rebuilding {
		return ErrNoRebuild
	}

	// 块之间崩溃：最近一次成功 Step 已落检查点（processed/shadow
	// 本身就是检查点状态），因此没有额外可丢的进度；标记崩溃后，
	// 在恢复前拒绝提交，Step 将从上一检查点继续。
	r.crashed = true
	return nil
}

// Snapshot 返回当前已提交视图的副本与代数。重建期间返回旧视图。
func (r *Rebuilder) Snapshot() (view View, generation int64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneView(r.view), r.generation
}

// Status 返回重建器当前状态的完整副本（含影子内容），用于测试与日志。
func (r *Rebuilder) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Status{
		Generation: r.generation,
		View:       cloneView(r.view),
		Rebuilding: r.rebuilding,
		ChunkSize:  r.chunkSize,
		Processed:  r.processed,
		Shadow:     cloneView(r.shadow),
	}
}
