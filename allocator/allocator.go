package allocator

import (
	"context"
	stdlog "log"
	"os"
	"sync"
)

// Logger 打印每次操作的输入、输出与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// Allocator 为每个租户按批预留并发放 (密钥版本, 序号)。
type Allocator struct {
	store Store
	log   Logger

	mu      sync.Mutex
	tenants map[string]*tenantState

	// onJoinInflight 为测试钩子：每次有调用者加入进行中的预留时触发。
	onJoinInflight func()
}

type tenantState struct {
	n, b, v int

	mu sync.Mutex
	// reserved 为内存中已预留到的水位。
	reserved Watermark
	// issued 为已发放到的水位：下一个待发放序号为 issued.NextSeq。
	issued Watermark

	// reserveInflight 非 nil 表示已有一次预留正在进行，等待者在 done 上接收广播。
	reserveInflight *reserveCall
}

type reserveCall struct {
	old    Watermark
	target Watermark
	// done 在预留完成后关闭，所有等待者共享同一次结果（广播）。
	done chan struct{}
	res  reserveResult
}

type reserveResult struct {
	status ReserveStatus
	err    error
}

// New 创建分配器，不从持久化层恢复任何租户；log 为 nil 时输出到 stderr。
func New(store Store, log Logger) *Allocator {
	if log == nil {
		log = stdlog.New(os.Stderr, "[allocator] ", stdlog.LstdFlags|stdlog.Lmicroseconds)
	}
	return &Allocator{store: store, log: log, tenants: make(map[string]*tenantState)}
}

// Recover 从持久化层加载全部租户到内存，用于崩溃恢复。
func (a *Allocator) Recover(ctx context.Context) error {
	names, err := a.store.ListTenants(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, name := range names {
		n, b, v, wm, err := a.store.LoadTenant(ctx, name)
		if err != nil {
			return err
		}
		// 崩溃恢复起点：以持久化高水位为准，
		// [0, NextSeq) 中尚未发放的序号全部作废。
		a.tenants[name] = &tenantState{n: n, b: b, v: v, reserved: wm, issued: wm}
		a.log.Printf("recover tenant=%q N=%d B=%d V=%d watermark=(v%d,seq%d) -> 未发放预留序号作废", name, n, b, v, wm.Version, wm.NextSeq)
	}
	return nil
}

// Register 注册新租户，从版本 1、序号 0 起。
func (a *Allocator) Register(ctx context.Context, tenant string, n, b, v int) error {
	// 判定依据：空租户 -> 已存在 -> 参数非法，只报第一个原因。
	if tenant == "" {
		return ErrEmptyTenant
	}
	a.mu.Lock()
	if _, ok := a.tenants[tenant]; ok {
		a.mu.Unlock()
		a.log.Printf("register input={tenant:%q,N:%d,B:%d,V:%d} output=error:%v reason=tenant-exists", tenant, n, b, v, ErrTenantExists)
		return ErrTenantExists
	}
	a.mu.Unlock()
	if n <= 0 || b <= 0 || v <= 0 || b > n {
		a.log.Printf("register input={tenant:%q,N:%d,B:%d,V:%d} output=error:%v reason=invalid-params", tenant, n, b, v, ErrInvalidParam)
		return ErrInvalidParam
	}

	wm := Watermark{Version: 1, NextSeq: 0}
	if err := a.store.RegisterTenant(ctx, tenant, n, b, v, wm); err != nil {
		a.log.Printf("register input={tenant:%q,N:%d,B:%d,V:%d} output=error:%v reason=persist-failed", tenant, n, b, v, err)
		return err
	}
	a.mu.Lock()
	if _, ok := a.tenants[tenant]; !ok {
		a.tenants[tenant] = &tenantState{n: n, b: b, v: v, reserved: wm, issued: wm}
	}
	a.mu.Unlock()
	a.log.Printf("register input={tenant:%q,N:%d,B:%d,V:%d} output=ok start=(v1,seq0) reason=registered", tenant, n, b, v)
	return nil
}

// Allocate 分配下一个 (密钥版本, 序号)。
func (a *Allocator) Allocate(ctx context.Context, tenant string) (version, seq int, err error) {
	// 判定依据顺序：空租户 -> 未注册 -> 版本耗尽；持久化失败另行返回，均可与之区分。
	if tenant == "" {
		return 0, 0, ErrEmptyTenant
	}
	a.mu.Lock()
	st := a.tenants[tenant]
	a.mu.Unlock()
	if st == nil {
		a.log.Printf("allocate input={tenant:%q} output=error:%v reason=tenant-not-found", tenant, ErrTenantNotFound)
		return 0, 0, ErrTenantNotFound
	}

	ver, s, err := st.allocate(ctx, a, tenant)
	if err != nil {
		if err == ErrKeysExhausted {
			a.log.Printf("allocate input={tenant:%q} output=error:%v reason=all-%d-versions-used", tenant, err, st.v)
		} else if err == ErrReserveFailed || err == ErrReserveUnknown {
			a.log.Printf("allocate input={tenant:%q} output=error:%v reason=reservation", tenant, err)
		} else {
			a.log.Printf("allocate input={tenant:%q} output=error:%v reason=persist-failed", tenant, err)
		}
		return 0, 0, err
	}
	wm := st.snapshotReserved()
	a.log.Printf("allocate input={tenant:%q} output=(v%d,seq%d) reserved=(v%d,seq%d) reason=within-reserved-range", tenant, ver, s, wm.Version, wm.NextSeq)
	return ver, s, nil
}

func (st *tenantState) snapshotReserved() Watermark {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.reserved
}

// nextReservationTarget 根据当前预留水位计算下一批目标水位。
// 一批为 B 个序号但不跨版本边界（末批取该版本剩余数）；
// 当前版本已全部预留完时进入下一版本并从 0 起。
func (st *tenantState) nextReservationTarget(cur Watermark) (Watermark, bool) {
	if cur.NextSeq >= st.n {
		if cur.Version >= st.v {
			return Watermark{}, false
		}
		cur = Watermark{Version: cur.Version + 1, NextSeq: 0}
	}
	remaining := st.n - cur.NextSeq
	batch := st.b
	if batch > remaining {
		batch = remaining
	}
	return Watermark{Version: cur.Version, NextSeq: cur.NextSeq + batch}, true
}

func (st *tenantState) allocate(ctx context.Context, a *Allocator, tenant string) (int, int, error) {
	store := a.store
	log_ := a.log
	st.mu.Lock()
	for {
		// 版本用尽判定必须在任何持久化访问之前。
		if st.issued.Version > st.v || (st.issued.Version == st.v && st.issued.NextSeq >= st.n) {
			st.mu.Unlock()
			return 0, 0, ErrKeysExhausted
		}

		// 已预留范围内还有可发放序号（含前一批未知失败作废后跳空的情况）。
		// 注意只能在同一版本内比较 NextSeq；
		// issued 已跨到下一版本而 reserved 还停在上一版本满水位时必须先预留。
		issuable := st.issued.Version < st.reserved.Version ||
			(st.issued.Version == st.reserved.Version && st.issued.NextSeq < st.reserved.NextSeq)
		if issuable {
			ver := st.issued.Version
			seq := st.issued.NextSeq
			if st.issued.Version == st.reserved.Version && seq+1 >= st.n {
				// 当前版本序号正好发完：发放游标推进到下一版本起点。
				st.issued = Watermark{Version: ver + 1, NextSeq: 0}
			} else {
				st.issued.NextSeq++
			}
			st.mu.Unlock()
			return ver, seq, nil
		}

		// 已预留范围发完，需要新预留一批。
		target, ok := st.nextReservationTarget(st.reserved)
		if !ok {
			st.mu.Unlock()
			return 0, 0, ErrKeysExhausted
		}

		// 并发去重：已有从当前水位出发的预留在进行中则共用同一次结果。
		if st.reserveInflight != nil && st.reserveInflight.old == st.reserved {
			call := st.reserveInflight
			if a.onJoinInflight != nil {
				a.onJoinInflight()
			}
			st.mu.Unlock()
			<-call.done
			res := call.res
			log_.Printf("allocate tenant=%q join-inflight reserve old=(v%d,seq%d) target=(v%d,seq%d) status=%d err=%v",
				tenant, call.old.Version, call.old.NextSeq, call.target.Version, call.target.NextSeq, res.status, res.err)
			// 等待者与发起者同命运：预留失败则本次分配直接失败，不做隐式重试。
			if res.err != nil {
				return 0, 0, res.err
			}
			if res.status == ReserveUnknown {
				return 0, 0, ErrReserveUnknown
			}
			if res.status == ReserveFailed {
				return 0, 0, ErrReserveFailed
			}
			// ReserveOK：重新持锁，从新推进的预留范围内发放序号。
			st.mu.Lock()
			continue
		}

		call := reserveCall{
			old:    st.reserved,
			target: target,
			done:   make(chan struct{}),
		}
		st.reserveInflight = &call
		old := call.old
		st.mu.Unlock()

		status, perr := store.CompareAndSwapReserve(ctx, tenant, call.old, call.target)

		var retErr error
		finish := func(res reserveResult) {
			call.res = res
			close(call.done)
		}

		st.mu.Lock()
		inflight := st.reserveInflight
		if inflight != nil && inflight.done == call.done {
			st.reserveInflight = nil
		}
		switch {
		case perr != nil:
			// 传输层错误按「明确未落盘」处理：水位不变，本次分配失败。
			log_.Printf("reserve tenant=%q old=(v%d,seq%d) target=(v%d,seq%d) -> error:%v 按明确失败处理，水位不变",
				tenant, old.Version, old.NextSeq, target.Version, target.NextSeq, perr)
			st.mu.Unlock()
			finish(reserveResult{status: ReserveFailed, err: perr})
			return 0, 0, perr
		case status == ReserveOK:
			st.reserved = target
			log_.Printf("reserve tenant=%q old=(v%d,seq%d) target=(v%d,seq%d) -> OK 内存水位推进",
				tenant, old.Version, old.NextSeq, target.Version, target.NextSeq)
			st.mu.Unlock()
			finish(reserveResult{status: ReserveOK})
		case status == ReserveUnknown:
			// 可能已落盘：该批视为已预留且整批作废，
			// 内存预留与发放游标都跳到 target；本次分配仍失败，
			// 下一次从该批之后重新预留。
			st.reserved = target
			st.issued = target
			log_.Printf("reserve tenant=%q old=(v%d,seq%d) target=(v%d,seq%d) -> UNKNOWN 按已落盘处理，整批作废，issued 跳至 target",
				tenant, old.Version, old.NextSeq, target.Version, target.NextSeq)
			st.mu.Unlock()
			finish(reserveResult{status: ReserveUnknown})
			retErr = ErrReserveUnknown
		default:
			// 明确未落盘：内存与持久化高水位都不变，本次分配失败。
			log_.Printf("reserve tenant=%q old=(v%d,seq%d) target=(v%d,seq%d) -> FAILED 确定未落盘，水位不变",
				tenant, old.Version, old.NextSeq, target.Version, target.NextSeq)
			st.mu.Unlock()
			finish(reserveResult{status: ReserveFailed})
			return 0, 0, ErrReserveFailed
		}
		if retErr != nil {
			return 0, 0, retErr
		}
		// OK：重新持锁循环发放新预留范围内的序号。
		st.mu.Lock()
	}
}
