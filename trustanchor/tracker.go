// Package trustanchor 实现 RFC 5011 风格的信任锚自动更新跟踪器。
package trustanchor

import "sync"

// 参数取值范围常量。
const (
	MaxHold    = int64(1_000_000_000) // H 与 R 的上界（秒）
	MaxNow     = int64(1_000_000_000_000_000)
	MaxTracked = 1000 // Kmax 上界
	MaxAppear  = 100  // M 上界
	MaxKeySet  = 64   // 单次观测密钥集项数上界
)

// State 是被跟踪密钥的状态。
type State int

const (
	StateUntracked State = iota // 未跟踪
	StateAddPend                // 待加入
	StateValid                  // 已信任
	StateMissing                // 缺失
	StateRevoked                // 已吊销
	StateBanned                 // 永久封禁（不占 Kmax）
)

func (s State) String() string {
	switch s {
	case StateUntracked:
		return "UNTRACKED"
	case StateAddPend:
		return "ADDPEND"
	case StateValid:
		return "VALID"
	case StateMissing:
		return "MISSING"
	case StateRevoked:
		return "REVOKED"
	case StateBanned:
		return "BANNED"
	}
	return "UNKNOWN"
}

// KeyStatus 是 State 查询的返回结果。
type KeyStatus struct {
	State State
	Since int64 // ADDPEND / MISSING 的计时起点
	At    int64 // REVOKED 的吊销时刻
	Cnt   int   // ADDPEND 的连续出现次数
}

// KeyItem 是密钥集中的一项。
type KeyItem struct {
	ID      []byte
	Revoked bool
}

// Config 是跟踪器构造参数。
type Config struct {
	AddHold    int64    // H：加入保持期，0..1e9 秒
	Retain     int64    // R：吊销与缺失保留期，1..1e9 秒
	MaxTracked int      // Kmax：跟踪密钥数上限，1..1000
	MinAppear  int      // M：晋升所需连续出现次数，1..100
	Quorum     int      // Q：签名法定数，1..Kmax
	Anchors    [][]byte // 初始信任锚，Q..Kmax 个互不相同的非空标识
}

type keyState struct {
	state State
	since int64
	at    int64
	cnt   int
}

// Tracker 是并发安全的信任锚跟踪器。
type Tracker struct {
	mu        sync.Mutex
	cfg       Config
	keys      map[string]*keyState // 被跟踪密钥（不含 BANNED）
	banned    map[string]struct{}
	maxNow    int64 // 此前被接受观测的最大 now
	scanCount int   // 非导出计数器：最近一次 Observe 处理的密钥项数
	scanBound int   // 对应的界：密钥集项数 + 观测前被跟踪数（不含 BANNED）
}

// NewTracker 校验构造参数；非法则以 ErrInvalidConfig 整体拒绝。
func NewTracker(cfg Config) (*Tracker, error) {
	if cfg.AddHold < 0 || cfg.AddHold > MaxHold {
		return nil, newError(ErrInvalidConfig, "AddHold (H) 须在 0..1e9 秒内")
	}
	if cfg.Retain < 1 || cfg.Retain > MaxHold {
		return nil, newError(ErrInvalidConfig, "Retain (R) 须在 1..1e9 秒内")
	}
	if cfg.MaxTracked < 1 || cfg.MaxTracked > MaxTracked {
		return nil, newError(ErrInvalidConfig, "MaxTracked (Kmax) 须在 1..1000 内")
	}
	if cfg.MinAppear < 1 || cfg.MinAppear > MaxAppear {
		return nil, newError(ErrInvalidConfig, "MinAppear (M) 须在 1..100 内")
	}
	if cfg.Quorum < 1 || cfg.Quorum > cfg.MaxTracked {
		return nil, newError(ErrInvalidConfig, "Quorum (Q) 须在 1..Kmax 内")
	}
	if len(cfg.Anchors) < cfg.Quorum || len(cfg.Anchors) > cfg.MaxTracked {
		e := newError(ErrInvalidConfig, "初始锚个数须在 Q..Kmax 内")
		e.Have, e.Need = len(cfg.Anchors), cfg.Quorum
		return nil, e
	}
	t := &Tracker{
		cfg:    cfg,
		keys:   make(map[string]*keyState, len(cfg.Anchors)),
		banned: make(map[string]struct{}),
	}
	for i, id := range cfg.Anchors {
		if len(id) == 0 {
			e := newError(ErrInvalidConfig, "初始锚标识不能为空")
			e.Index = i
			return nil, e
		}
		k := string(id)
		if _, dup := t.keys[k]; dup {
			e := newError(ErrInvalidConfig, "初始锚标识重复")
			e.Index, e.ID = i, k
			return nil, e
		}
		t.keys[k] = &keyState{state: StateValid}
	}
	return t, nil
}

// validateArgs 校验 Observe 参数（第一关）。
func validateArgs(keySet []KeyItem, signers [][]byte, now int64) error {
	if now < 0 || now > MaxNow {
		e := newError(ErrInvalidArgument, "now 须在 0..1e15 内")
		e.Now = now
		return e
	}
	if len(keySet) < 1 || len(keySet) > MaxKeySet {
		e := newError(ErrInvalidArgument, "密钥集项数须在 1..64 内")
		e.Have = len(keySet)
		return e
	}
	seen := make(map[string]struct{}, len(keySet))
	for i, item := range keySet {
		if len(item.ID) == 0 {
			e := newError(ErrInvalidArgument, "密钥集标识不能为空")
			e.Index = i
			return e
		}
		k := string(item.ID)
		if _, dup := seen[k]; dup {
			e := newError(ErrInvalidArgument, "密钥集标识重复")
			e.Index, e.ID = i, k
			return e
		}
		seen[k] = struct{}{}
	}
	for i, s := range signers {
		if len(s) == 0 {
			e := newError(ErrInvalidArgument, "签名者标识不能为空")
			e.Index = i
			return e
		}
	}
	return nil
}

// Observe 处理一次密钥集观测；被拒绝时不改变任何状态与时钟。
//
// 拒绝次序（只报第一个）：参数非法、时钟回退、签名者不可信、锁死、超限。
// 通过前两关后在副本上依次执行三步处理，全部完成并通过检查才整体提交。
func (t *Tracker) Observe(keySet []KeyItem, signers [][]byte, now int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := validateArgs(keySet, signers, now); err != nil {
		return err
	}
	if now < t.maxNow {
		e := newError(ErrClockRollback, "now 小于此前被接受观测的最大 now")
		e.Now, e.MaxNow = now, t.maxNow
		return e
	}
	// 签名者按观测开始前的状态判定：去重后 VALID 个数须不小于 Q。
	trusted := 0
	seenSigner := make(map[string]struct{}, len(signers))
	for _, s := range signers {
		k := string(s)
		if _, dup := seenSigner[k]; dup {
			continue
		}
		seenSigner[k] = struct{}{}
		if ks, ok := t.keys[k]; ok && ks.state == StateValid {
			trusted++
		}
	}
	if trusted < t.cfg.Quorum {
		e := newError(ErrUntrustedSigners, "去重后 VALID 签名者个数小于 Q")
		e.Have, e.Need = trusted, t.cfg.Quorum
		return e
	}

	// 在副本上处理，提交或丢弃。
	keys := make(map[string]*keyState, len(t.keys))
	for k, v := range t.keys {
		c := *v
		keys[k] = &c
	}
	banned := make(map[string]struct{}, len(t.banned))
	for k := range t.banned {
		banned[k] = struct{}{}
	}
	t.scanCount = 0
	t.scanBound = len(keySet) + len(t.keys)

	// 第一步：被跟踪而不在密钥集中的密钥。
	inSet := make(map[string]struct{}, len(keySet))
	for _, item := range keySet {
		inSet[string(item.ID)] = struct{}{}
	}
	for k, ks := range keys {
		t.scanCount++
		if _, present := inSet[k]; present {
			continue
		}
		switch ks.state {
		case StateValid:
			ks.state, ks.since = StateMissing, now
		case StateAddPend:
			delete(keys, k) // 计时丢失
		}
	}

	// 第二步：处理密钥集中的每一项。
	for _, item := range keySet {
		t.scanCount++
		k := string(item.ID)
		if _, isBanned := banned[k]; isBanned {
			continue // BANNED 标识整项忽略
		}
		ks, tracked := keys[k]
		if item.Revoked {
			switch {
			case tracked && (ks.state == StateValid || ks.state == StateMissing):
				ks.state, ks.at = StateRevoked, now
			case tracked && ks.state == StateAddPend:
				delete(keys, k)
			}
			// REVOKED 不刷新 at；未跟踪者忽略。
			continue
		}
		switch {
		case !tracked:
			keys[k] = &keyState{state: StateAddPend, since: now, cnt: 1}
		case ks.state == StateAddPend:
			ks.cnt++ // since 不刷新
		case ks.state == StateMissing:
			ks.state, ks.since = StateValid, 0 // since 清除
		}
		// VALID 不变；REVOKED 不变（不会重新被信任）。
	}

	// 第三步（同一 now）：晋升、封禁与缺失过期。
	for k, ks := range keys {
		switch ks.state {
		case StateAddPend:
			if now >= ks.since+t.cfg.AddHold && ks.cnt >= t.cfg.MinAppear {
				ks.state, ks.since, ks.cnt = StateValid, 0, 0
			}
		case StateRevoked:
			if now >= ks.at+t.cfg.Retain {
				delete(keys, k)
				banned[k] = struct{}{} // BANNED 永久
			}
		case StateMissing:
			if now >= ks.since+t.cfg.Retain {
				delete(keys, k) // 变未跟踪，不进 BANNED
			}
		}
	}

	// 锁死检查先于超限检查。
	validCount := 0
	for _, ks := range keys {
		if ks.state == StateValid {
			validCount++
		}
	}
	if validCount < t.cfg.Quorum {
		e := newError(ErrLockup, "处理后 VALID 个数小于 Q")
		e.Have, e.Need = validCount, t.cfg.Quorum
		return e
	}
	if len(keys) > t.cfg.MaxTracked {
		e := newError(ErrOverflow, "处理后被跟踪密钥数超过 Kmax")
		e.Have, e.Need = len(keys), t.cfg.MaxTracked
		return e
	}

	// 整体提交。
	t.keys = keys
	t.banned = banned
	t.maxNow = now
	return nil
}

// State 返回标识当前的状态与计时信息。
func (t *Tracker) State(id []byte) KeyStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := string(id)
	if _, banned := t.banned[k]; banned {
		return KeyStatus{State: StateBanned}
	}
	if ks, ok := t.keys[k]; ok {
		return KeyStatus{State: ks.state, Since: ks.since, At: ks.at, Cnt: ks.cnt}
	}
	return KeyStatus{State: StateUntracked}
}
