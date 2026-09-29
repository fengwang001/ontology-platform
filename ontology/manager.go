package ontology

import (
	"context"
	"sync"
)

// Manager 维护当前快照版本与累计统计。
//
// 快照以不可变切片形式整体发布：读者持 RLock 取到的是某一时刻的完整版本，
// 不会读到差分进行中的新旧混合状态；差分计算在读锁下进行，多个差分与所有
// 查询/统计/自检可以真正并发，只有最后的版本提交需要短暂持写锁。
type Manager struct {
	mu         sync.RWMutex
	current    []Entry
	version    int64
	stats      Stats
	maxChanges int
	logger     Logger
}

// NewManager 用初始快照创建管理器。初始快照同样必须合法，否则整体拒绝且
// 不产生管理器实例。MaxChanges <= 0 表示不限制单次变更条数。
func NewManager(initial []Entry, cfg Config) (*Manager, error) {
	if cfg.MaxChanges < 0 {
		return nil, reject(KindInvalidConfig, "maxChanges must be >= 0")
	}
	if err := Validate(initial); err != nil {
		return nil, err
	}
	return &Manager{
		current:    cloneEntries(initial),
		maxChanges: cfg.MaxChanges,
	}, nil
}

// WithLogger 返回带过程日志器的管理器副本配置（必须在使用前设置）。
func (m *Manager) WithLogger(log Logger) *Manager {
	m.mu.Lock()
	m.logger = log
	m.mu.Unlock()
	return m
}

// DiffAndApply 校验新快照、与当前快照归并差分，成功时原子提交新版本。
//
// 任何非法输入（配置、空键、重复键、未排序、变更超限）都整体拒绝：
// 当前快照、版本号与累计统计均保持不变（失败不留痕）。
// 返回本次版本号（提交后）与变更日志。
func (m *Manager) DiffAndApply(ctx context.Context, newSnap []Entry) (int64, []Change, error) {
	if err := Validate(newSnap); err != nil {
		m.logRejection(ctx, err)
		return 0, nil, err
	}
	newCopy := cloneEntries(newSnap)

	// 乐观路径：在读锁下完成全部计算，读者与其他差分互不阻塞。
	m.mu.RLock()
	baseVersion := m.version
	changes, err := MergeDiff(ctx, m.current, newCopy, m.maxChanges, m.logger)
	m.mu.RUnlock()
	if err != nil {
		m.logRejection(ctx, err)
		return 0, nil, err
	}

	m.mu.Lock()
	// 持读锁期间可能已有其他差分提交；基线变化则在写锁下重算后提交。
	if m.version != baseVersion {
		if m.logger != nil {
			m.logger.Logf(ctx, "base version advanced %d -> %d, recomputing under write lock",
				baseVersion, m.version)
		}
		changes, err = MergeDiff(ctx, m.current, newCopy, m.maxChanges, m.logger)
		if err != nil {
			m.mu.Unlock()
			m.logRejection(ctx, err)
			return 0, nil, err
		}
	}
	m.current = newCopy
	m.version++
	var ins, del, upd int64
	for _, c := range changes {
		switch c.Op {
		case OpInsert:
			ins++
		case OpDelete:
			del++
		case OpUpdate:
			upd++
		}
	}
	m.stats.Commits++
	m.stats.Inserts += ins
	m.stats.Deletes += del
	m.stats.Updates += upd
	m.stats.ChangesTotal += int64(len(changes))
	version := m.version
	stats := m.stats
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Logf(ctx, "committed version=%d changes=%d stats=%+v", version, len(changes), stats)
	}
	return version, changes, nil
}

// Snapshot 返回当前快照的完整深拷贝，整份等于提交历史上某一个版本。
func (m *Manager) Snapshot() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneEntries(m.current)
}

// Stats 返回累计统计的拷贝。
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// Version 返回当前版本号，初始为 0，每次成功差分后加一。
func (m *Manager) Version() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.version
}

// SelfCheck 校验内部不变量：快照严格升序且统计口径自洽。
func (m *Manager) SelfCheck() error {
	m.mu.RLock()
	snap := cloneEntries(m.current)
	stats := m.stats
	m.mu.RUnlock()

	if err := Validate(snap); err != nil {
		return err
	}
	if stats.Inserts+stats.Deletes+stats.Updates != stats.ChangesTotal {
		return reject(KindInvalidConfig, "stats invariant broken: op counts do not sum to total")
	}
	return nil
}

func (m *Manager) logRejection(ctx context.Context, cause error) {
	m.mu.RLock()
	log := m.logger
	m.mu.RUnlock()
	if log != nil {
		log.Logf(ctx, "rejected cause=%v (current snapshot and stats unchanged)", cause)
	}
}

func cloneEntries(s []Entry) []Entry {
	if len(s) == 0 {
		return []Entry{}
	}
	out := make([]Entry, len(s))
	copy(out, s)
	return out
}
