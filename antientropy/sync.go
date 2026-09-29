package antientropy

import (
	"fmt"
	"io"
	"sort"
)

// validateVector 检查向量中不存在负数条目。
func validateVector(v Version) error {
	for src, seq := range v {
		if seq < 0 {
			return fmt.Errorf("%w: source %q has negative seq %d", ErrInvalidVector, src, seq)
		}
	}
	return nil
}

// diffLocked 计算发送给某版本向量目标的最小差集（按 Source、Seq 有序）。
// 仅枚举本副本真正持有日志的来源；目标向量中从未见过的来源不在此处，
// 因此不会凭空产生变更，也不会写入向量。要求持有 r.mu。
func (r *Replica) diffLocked(target Version) []Change {
	sources := make([]string, 0, len(r.idx))
	for src := range r.idx {
		sources = append(sources, src)
	}
	sort.Strings(sources)
	batch := make([]Change, 0)
	for _, src := range sources {
		have := r.idx[src]  // 序号 1..len(have) 连续
		seen := target[src] // 目标向量没有该来源时为 0：不产生向量写入
		for _, c := range have {
			if c.Seq > seen {
				batch = append(batch, c)
			}
		}
	}
	sort.Slice(batch, func(i, j int) bool { return lessChange(batch[i], batch[j]) })
	return batch
}

// prepareApply 校验一整批变更对当前接收方状态是否可原子应用：
// 来源均已注册、每个来源的序号相对接收方已见值严格连续、且发送有序。
// 任何非法情形返回对应错误，且不做任何修改。要求持有 r.mu。
func (r *Replica) prepareApplyLocked(batch []Change) error {
	// 整体顺序：按 (Source, Seq) 升序。
	for i := 1; i < len(batch); i++ {
		if !lessChange(batch[i-1], batch[i]) && batch[i-1] != batch[i] {
			return fmt.Errorf("%w: batch not ordered by (source, seq)", ErrNonContiguousChange)
		}
		if batch[i-1] == batch[i] {
			return fmt.Errorf("%w: duplicate change %s#%d",
				ErrNonContiguousChange, batch[i].Source, batch[i].Seq)
		}
	}
	last := map[string]int{}
	for _, c := range batch {
		if c.Seq <= 0 {
			return fmt.Errorf("%w: non-positive seq %d from %q",
				ErrNonContiguousChange, c.Seq, c.Source)
		}
		if r.reg == nil {
			return fmt.Errorf("%w: missing registry", ErrUnregisteredReplica)
		}
		if _, ok := r.reg.getLocked(c.Source); !ok {
			return fmt.Errorf("%w: change source %q", ErrUnregisteredReplica, c.Source)
		}
		next := r.vector[c.Source] + 1
		if prev, seen := last[c.Source]; seen {
			next = prev + 1
		}
		if c.Seq != next {
			return fmt.Errorf("%w: source %q expects seq %d but got %d",
				ErrNonContiguousChange, c.Source, next, c.Seq)
		}
		last[c.Source] = c.Seq
	}
	return nil
}

// getLocked 不加注册表锁的只读查询，要求调用方持有所查副本自身的锁。
func (r *Registry) getLocked(name string) (*Replica, bool) {
	rep, ok := r.replicas[name]
	return rep, ok
}

// applyBatch 原子应用一整批变更：先完整校验，通过后才一次性写入日志与向量；
// 校验失败不留任何痕迹。
func (r *Replica) applyBatch(batch []Change) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.prepareApplyLocked(batch); err != nil {
		return err
	}
	for _, c := range batch {
		r.vector[c.Source] = c.Seq
		r.idx[c.Source] = append(r.idx[c.Source], c)
		r.insertSorted(c)
	}
	return nil
}

// lockPair 按副本名固定顺序加锁，避免双向/并发同步死锁。
func lockPair(a, b *Replica) func() {
	if a == b {
		a.mu.Lock()
		return a.mu.Unlock
	}
	first, second := a, b
	if a.name > b.name {
		first, second = b, a
	}
	first.mu.Lock()
	second.mu.Lock()
	return func() {
		second.mu.Unlock()
		first.mu.Unlock()
	}
}

// Sync 执行一次 src -> dst 的单向增量反熵同步，返回实际发送的变更数。
// dst 把版本向量发给 src，src 回送最小差集，dst 校验后按序应用。
// 任何校验失败都会整体拒绝：双方日志与向量保持原样（失败不留痕）。
// w 非空时打印每步的向量、差集与判定依据。
func Sync(src, dst *Replica, w io.Writer) (int, error) {
	if src == nil || dst == nil {
		return 0, fmt.Errorf("%w: nil replica", ErrUnregisteredReplica)
	}
	if src.reg == nil || dst.reg == nil || src.reg != dst.reg {
		return 0, fmt.Errorf("%w: replicas do not share a registry", ErrUnregisteredReplica)
	}
	reg := src.reg
	if _, ok := reg.Get(src.name); !ok {
		return 0, fmt.Errorf("%w: source %q", ErrUnregisteredReplica, src.name)
	}
	if _, ok := reg.Get(dst.name); !ok {
		return 0, fmt.Errorf("%w: target %q", ErrUnregisteredReplica, dst.name)
	}

	// 在共同持锁期间完成全部读取、计算与校验，保证失败不留痕、结果可复现。
	unlock := lockPair(src, dst)
	defer unlock()

	target := dst.vector
	if err := validateVector(target); err != nil {
		logf(w, "[sync %s -> %s] REJECT: %v\n", src.name, dst.name, err)
		return 0, err
	}

	batch := src.diffLocked(target)

	logf(w, "[sync %s -> %s] target vector = %s\n", src.name, dst.name, formatVector(target))
	logf(w, "[sync %s -> %s] reason: send every source's changes with seq > target-seen; "+
		"unknown sources in target vector yield no changes and are not stored\n", src.name, dst.name)
	if len(batch) == 0 {
		logf(w, "[sync %s -> %s] diff = <empty>, nothing sent\n", src.name, dst.name)
		return 0, nil
	}

	limit := effectiveLimit(src.maxBatch, dst.maxBatch)
	if limit > 0 && len(batch) > limit {
		err := fmt.Errorf("%w: batch size %d > limit %d", ErrLogLimitExceeded, len(batch), limit)
		logf(w, "[sync %s -> %s] REJECT: %v\n", src.name, dst.name, err)
		return 0, err
	}
	if err := dst.prepareApplyLocked(batch); err != nil {
		logf(w, "[sync %s -> %s] REJECT: %v\n", src.name, dst.name, err)
		return 0, err
	}

	for _, c := range batch {
		logf(w, "[sync %s -> %s] send %s#%d key=%q val=%q ts=%d (target seen %d)\n",
			src.name, dst.name, c.Source, c.Seq, c.Key, c.Value, c.TS, target[c.Source])
	}

	for _, c := range batch {
		dst.vector[c.Source] = c.Seq
		dst.idx[c.Source] = append(dst.idx[c.Source], c)
		dst.insertSorted(c)
	}
	logf(w, "[sync %s -> %s] applied %d changes, new vector = %s\n",
		src.name, dst.name, len(batch), formatVector(dst.vector))
	return len(batch), nil
}

// effectiveLimit 取双方更严格（更小且非零）的批量上限。
func effectiveLimit(a, b int) int {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// formatVector 以 {a:1 b:2} 形式稳定打印版本向量。
func formatVector(v Version) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := "{"
	for i, k := range keys {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:%d", k, v[k])
	}
	return s + "}"
}

// logf 仅在配置了日志输出时打印，保证 w 传 nil 的调用安全。
func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, format, args...)
	}
}
