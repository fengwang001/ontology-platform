package activate

import (
	"ontology/guard"
	"ontology/roster"
)

// 拒绝结论。roster 侧错误直接复用；activate 侧另加四个哨兵。
var (
	ErrInvalid   = roster.ErrInvalid
	ErrClockBack = roster.ErrClockBack
	ErrUnknown   = roster.ErrUnknown

	ErrLocked      = errLocked{}
	ErrConflict    = errConflict{}
	ErrBatchClosed = errBatchClosed{}
	ErrQuota       = errQuota{}
	ErrState       = errState{}
)

type errLocked struct{}

func (errLocked) Error() string { return "sn is locked" }

type errConflict struct{}

func (errConflict) Error() string { return "fingerprint conflict" }

type errBatchClosed struct{}

func (errBatchClosed) Error() string { return "batch activation window closed" }

type errQuota struct{}

func (errQuota) Error() string { return "tenant quota exhausted" }

type errState struct{}

func (errState) Error() string { return "invalid sn state for operation" }

// Service 编排激活、重置、停用与租户名额。
type Service struct {
	r *roster.Roster
	g *guard.Guard
}

// New 创建服务，m 为阈值（1..10），lk 为锁定基数秒（1..1e6）。
func New(m int, lk int64) (*Service, error) {
	if m < 1 || m > 10 || lk < 1 || lk > 1_000_000 {
		return nil, ErrInvalid
	}
	return &Service{r: roster.New(), g: guard.New(m, lk)}, nil
}

// Roster 暴露底层名单供测试与建租户、登记批次使用。
func (s *Service) Roster() *roster.Roster { return s.r }

// Guard 暴露 guard 供测试断言 e/k/lockUntil。
func (s *Service) Guard() *guard.Guard { return s.g }

// ActivateResult 是一次激活成功的结论。
type ActivateResult struct {
	ID        int64
	Gen       int64
	Replayed  bool
	LockUntil int64 // 当前锁定截止（未锁定为 0）
}

// Activate 按题述三种状态情形处理。
func (s *Service) Activate(sn, fp []byte, now int64) (ActivateResult, error) {
	if !validBytes(sn) || !validBytes(fp) || !validTime(now) {
		return ActivateResult{}, ErrInvalid
	}
	s.r.Lock()
	defer s.r.Unlock()

	// 时钟回退：只读比较，不推进；真正推进只发生在接受点或 ErrConflict。
	if now < s.r.MaxNowLocked() {
		return ActivateResult{}, ErrClockBack
	}
	rec := s.r.BeginOpLocked(sn)
	if rec == nil {
		return ActivateResult{}, ErrUnknown
	}
	// 锁定期任何 Activate 一律拒绝（含正确指纹的幂等重放），不计数、不延长。
	if s.g.Locked(sn, now) {
		snap, _ := s.g.Get(sn)
		return ActivateResult{LockUntil: snap.LockUntil}, ErrLocked
	}

	switch rec.State {
	case roster.Registered:
		// Registered 下任何指纹都不构成冲突、不累计错误。
		if now >= rec.Until {
			return ActivateResult{}, ErrBatchClosed
		}
		used, quota, ok := s.r.QuotaLocked(rec.Tenant)
		if !ok {
			return ActivateResult{}, ErrInvalid
		}
		if used >= quota {
			return ActivateResult{}, ErrQuota
		}
		if err := s.r.AdvanceClockLocked(now); err != nil {
			return ActivateResult{}, err
		}
		id, gen := s.r.SetFirstBindLocked(sn, fp)
		return ActivateResult{ID: id, Gen: gen}, nil

	case roster.Activated:
		if !bytesEqual(rec.FP, fp) {
			// ErrConflict 是唯一改状态的拒绝：推进时钟并更新 e/k/lockUntil。
			if err := s.r.AdvanceClockLocked(now); err != nil {
				return ActivateResult{}, err
			}
			_, lockUntil, _ := s.g.NoteConflict(sn, now)
			return ActivateResult{LockUntil: lockUntil}, ErrConflict
		}
		// 幂等重放：不改任何状态（不清 e、不看截止/名额），但属已接受操作，推进时钟。
		if err := s.r.AdvanceClockLocked(now); err != nil {
			return ActivateResult{}, err
		}
		snap, _ := s.g.Get(sn)
		return ActivateResult{ID: rec.ID, Gen: rec.Gen, Replayed: true, LockUntil: snap.LockUntil}, nil

	default: // ResetPending
		// 重绑新指纹：id 不变、gen+1，不看截止、不占新名额。
		if err := s.r.AdvanceClockLocked(now); err != nil {
			return ActivateResult{}, err
		}
		id, gen, _ := s.r.SetRebindLocked(sn, fp)
		snap, _ := s.g.Get(sn)
		return ActivateResult{ID: id, Gen: gen, LockUntil: snap.LockUntil}, nil
	}
}

// Reset 仅对 Activated 有效：状态变 ResetPending、清指纹、清 e 并解除锁定，保留 k。
func (s *Service) Reset(sn []byte, now int64) error {
	if !validBytes(sn) || !validTime(now) {
		return ErrInvalid
	}
	s.r.Lock()
	defer s.r.Unlock()
	if now < s.r.MaxNowLocked() {
		return ErrClockBack
	}
	rec := s.r.BeginOpLocked(sn)
	if rec == nil {
		return ErrUnknown
	}
	// 管理员操作不受锁定期影响：Reset 本身即解除锁定。
	if rec.State != roster.Activated {
		return ErrState
	}
	if err := s.r.AdvanceClockLocked(now); err != nil {
		return err
	}
	s.r.SetResetLocked(sn)
	s.g.Reset(sn)
	return nil
}

// Deactivate 对 Activated 或 ResetPending 有效：回 Registered、释放名额、清指纹，
// 保留 id/gen；guard 的 e/k 亦保留。再次激活须重新通过截止与名额判定。
func (s *Service) Deactivate(sn []byte, now int64) error {
	if !validBytes(sn) || !validTime(now) {
		return ErrInvalid
	}
	s.r.Lock()
	defer s.r.Unlock()
	if now < s.r.MaxNowLocked() {
		return ErrClockBack
	}
	rec := s.r.BeginOpLocked(sn)
	if rec == nil {
		return ErrUnknown
	}
	if rec.State != roster.Activated && rec.State != roster.ResetPending {
		return ErrState
	}
	if err := s.r.AdvanceClockLocked(now); err != nil {
		return err
	}
	s.r.SetDeactivatedLocked(sn)
	return nil
}

func validBytes(b []byte) bool { return len(b) >= 1 && len(b) <= 64 }

func validTime(t int64) bool { return 0 <= t && t <= 1_000_000_000_000 }

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
