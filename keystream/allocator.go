package keystream

import (
	"context"
	"log"
	"sync"
)

// Allocation 是一次成功分配的结果：（版本，序号）。
type Allocation struct {
	// Version 密钥版本，从 1 开始，至多 V。
	Version int64
	// Seq 版本内序号，从 0 开始，至多 N-1。
	Seq int64
}

// Logger 用于打印输入、输出与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

type tenantState struct {
	n, b, v int64
	// reserved 已预留高水位（跨版本累计槽位）。
	reserved int64
	// issued 已发放/作废位置： issued <= reserved。
	issued int64
	mu     sync.Mutex
	// leader 非空表示当前有一次预留正在进行。
	leader chan struct{}
	// leaderErr 是本次预留的结果，等待者在 leader 关闭后读取。
	leaderErr error
	cond      *sync.Cond
}

// Allocator 为多个租户分配（密钥版本，序号）。
type Allocator struct {
	store  Store
	logger Logger

	mu      sync.Mutex
	tenants map[string]*tenantState
}

// New 创建分配器。恢复已有租户高水位请随后调用 RecoverAll 或 Recover。
func New(store Store, logger Logger) *Allocator {
	if logger == nil {
		logger = log.Default()
	}
	return &Allocator{
		store:   store,
		logger:  logger,
		tenants: make(map[string]*tenantState),
	}
}

// Register 注册租户：版本 1、序号 0 起。
//
// 拒绝顺序（只报第一个原因）：租户为空、租户已存在、
// N/B/V 非正或 B 大于 N。持久化失败与上述校验错误可区分。
func (a *Allocator) Register(ctx context.Context, tenant string, n, b, v int64) error {
	if tenant == "" {
		a.logf("register reject input={tenant:%q,n:%d,b:%d,v:%d} reason=empty_tenant", tenant, n, b, v)
		return ErrEmptyTenant
	}

	a.mu.Lock()
	if _, exists := a.tenants[tenant]; exists {
		a.mu.Unlock()
		a.logf("register reject input={tenant:%q,n:%d,b:%d,v:%d} reason=tenant_exists", tenant, n, b, v)
		return ErrTenantExists
	}
	a.mu.Unlock()

	if n <= 0 || b <= 0 || v <= 0 || b > n {
		a.logf("register reject input={tenant:%q,n:%d,b:%d,v:%d} reason=invalid_config", tenant, n, b, v)
		return ErrInvalidConfig
	}

	rec := TenantRecord{N: n, B: b, V: v, Reserved: 0}
	if err := a.classifyPersist(ctx, "create_tenant", a.store.CreateTenant(ctx, tenant, rec)); err != nil {
		a.logf("register fail input={tenant:%q,n:%d,b:%d,v:%d} output=error reason=persist detail=%v", tenant, n, b, v, err)
		return err
	}

	st := newTenantState(n, b, v)
	a.mu.Lock()
	if _, exists := a.tenants[tenant]; exists {
		// 极端并发下（不应发生：注册前已在互斥下判定）以内存已有为准。
		a.mu.Unlock()
		return ErrTenantExists
	}
	a.tenants[tenant] = st
	a.mu.Unlock()

	a.logf("register ok input={tenant:%q,n:%d,b:%d,v:%d} output={startVersion:1,startSeq:0} reason=created", tenant, n, b, v)
	return nil
}

// Allocate 分配下一个（版本，序号）。
//
// 拒绝顺序（只报第一个原因）：租户为空、租户未注册、版本已用尽。
// 预留导致的持久化失败作为 *PersistError 返回，与上述原因可区分；
// 被拒绝或失败的分配不会发放任何序号。
func (a *Allocator) Allocate(ctx context.Context, tenant string) (Allocation, error) {
	if tenant == "" {
		a.logf("allocate reject input={tenant:%q} reason=empty_tenant", tenant)
		return Allocation{}, ErrEmptyTenant
	}

	a.mu.Lock()
	st := a.tenants[tenant]
	a.mu.Unlock()
	if st == nil {
		a.logf("allocate reject input={tenant:%q} reason=tenant_not_found", tenant)
		return Allocation{}, ErrTenantNotFound
	}

	st.mu.Lock()
	for {
		if st.issued < st.reserved {
			pos := st.issued
			st.issued++
			st.mu.Unlock()
			version, seq := linearToVersionSeq(st.n, pos)
			a.logf("allocate ok input={tenant:%q} output={version:%d,seq:%d,linear:%d} reason=served_from_reserved_window", tenant, version, seq, pos)
			return Allocation{Version: version, Seq: seq}, nil
		}

		if st.reserved >= st.n*st.v {
			st.mu.Unlock()
			a.logf("allocate reject input={tenant:%q} reason=key_exhausted reserved=%d capacity=%d", tenant, st.reserved, st.n*st.v)
			return Allocation{}, ErrKeyExhausted
		}

		leader := st.leader
		if leader == nil {
			// 本协程成为这一轮预留的唯一执行者。
			leader = make(chan struct{})
			st.leader = leader
			st.leaderErr = nil
			st.mu.Unlock()
			err := a.reserveBatch(ctx, tenant, st)
			st.mu.Lock()
			st.leaderErr = err
			st.leader = nil
			close(leader)
			st.cond.Broadcast()
			if err != nil {
				st.mu.Unlock()
				return Allocation{}, err
			}
			// 预留成功：回到循环开头发放序号。
			continue
		}

		// 与正在进行的预留共用同一次预留，等待其完成。
		st.cond.Wait()
		if err := st.leaderErr; err != nil {
			st.mu.Unlock()
			return Allocation{}, err
		}
		// 预留成功则回到循环开头：要么直接发放，要么发起下一轮预留。
	}
}

// reserveBatch 预留下一批（调用时不持有 st.mu）。
//
// 一批为 B 个序号，但不跨版本边界（末批取该版本剩余数）。
// 明确失败：内存与持久层高水位都不变。
// 结果未知：该批视为已预留且整批作废（issued/ reserved 一并推进）。
func (a *Allocator) reserveBatch(ctx context.Context, tenant string, st *tenantState) error {
	st.mu.Lock()
	n, b, v := st.n, st.b, st.v
	start := st.reserved
	st.mu.Unlock()

	if start >= n*v {
		return ErrKeyExhausted
	}

	// 本版本剩余槽位：n - start%n；一批取 min(B, 剩余)，天然不跨版本。
	remainingInVersion := n - start%n
	size := b
	if size > remainingInVersion {
		size = remainingInVersion
	}
	target := start + size

	err := a.classifyPersist(ctx, "save_reserved", a.store.SaveReserved(ctx, tenant, target))

	st.mu.Lock()
	defer st.mu.Unlock()
	if err == nil {
		st.reserved = target
		// issued 保持在 start：批内序号等待逐次发放（只有崩溃后
		// 未发放的预留序号才作废，见 Recover）。
		a.logf("reserve ok input={tenant:%q} output={window:[%d,%d),versionSpanNotCrossed:true} reason=persisted_high_watermark_advanced", tenant, start, target)
		return nil
	}

	var perr *PersistError
	isPersistErr := errorAs(err, &perr)
	if isPersistErr && perr.Unknown {
		// 结果未知：按“可能已落盘”处理，该批视为已预留且作废。
		st.reserved = target
		st.issued = target
		a.logf("reserve fail input={tenant:%q} target=%d output=error reason=unknown_outcome_batch_voided detail=%v", tenant, target, err)
		return err
	}

	// 明确失败（确定未落盘）：内存高水位不变，下一次重试同一批。
	a.logf("reserve fail input={tenant:%q} target=%d output=error reason=definitive_failure_retry_same_batch detail=%v", tenant, target, err)
	return err
}

// Recover 从持久层高水位恢复单个租户，未发放的已预留序号作废。
func (a *Allocator) Recover(ctx context.Context, tenant string) error {
	return a.recoverOne(ctx, tenant)
}

// RecoverAll 从持久层恢复全部租户。
func (a *Allocator) RecoverAll(ctx context.Context) error {
	names, err := a.store.ListTenants(ctx)
	if err != nil {
		return a.classifyPersist(ctx, "list_tenants", err)
	}
	for _, name := range names {
		if err := a.recoverOne(ctx, name); err != nil {
			return err
		}
	}
	a.logf("recover_all ok input=<all> output={tenants:%d} reason=loaded_from_persisted_high_watermarks", len(names))
	return nil
}

func (a *Allocator) recoverOne(ctx context.Context, tenant string) error {
	rec, ok, err := a.store.GetTenant(ctx, tenant)
	if err != nil {
		err = a.classifyPersist(ctx, "get_tenant", err)
		a.logf("recover fail input={tenant:%q} output=error reason=persist detail=%v", tenant, err)
		return err
	}
	if !ok {
		a.logf("recover reject input={tenant:%q} reason=tenant_not_found", tenant)
		return ErrTenantNotFound
	}
	st := newTenantState(rec.N, rec.B, rec.V)
	// 崩溃恢复：从持久层高水位继续，未发放的已预留序号作废。
	st.reserved = rec.Reserved
	st.issued = rec.Reserved
	a.mu.Lock()
	a.tenants[tenant] = st
	a.mu.Unlock()
	version, seq := linearToVersionSeq(rec.N, rec.Reserved)
	a.logf("recover ok input={tenant:%q} output={highWatermark:%d,nextVersion:%d,nextSeq:%d} reason=unissued_reserved_slots_voided", tenant, rec.Reserved, version, seq)
	return nil
}

func (a *Allocator) logf(format string, args ...any) {
	a.logger.Printf(format, args...)
}

func newTenantState(n, b, v int64) *tenantState {
	st := &tenantState{n: n, b: b, v: v}
	st.cond = sync.NewCond(&st.mu)
	return st
}

// linearToVersionSeq 把跨版本累计线性位置换算成（版本，序号）。
//
// pos in [k*n, (k+1)*n) 对应版本 k+1 的序号 pos-k*n。
func linearToVersionSeq(n, pos int64) (version, seq int64) {
	return pos/n + 1, pos % n
}

func errorAs(err error, target **PersistError) bool {
	for {
		if pe, ok := err.(*PersistError); ok {
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
		if err == nil {
			return false
		}
	}
}

// classifyPersist 把持久层返回的原始错误归一化为 *PersistError。
//
// 任何非 PersistError 错误（含 context 取消/超时导致的中断）都无法
// 证明高水位未落盘，一律按“结果未知”处理：宁可作废一批（<= B 个
// 序号），也绝不冒重发序号的风险。
func (a *Allocator) classifyPersist(ctx context.Context, op string, err error) error {
	if err == nil {
		return nil
	}
	var pe *PersistError
	if errorAs(err, &pe) {
		return pe
	}
	return &PersistError{Op: op, Unknown: true, Err: err}
}
