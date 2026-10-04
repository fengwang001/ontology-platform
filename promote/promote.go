// Package promote 是多级制品仓库的门面：时钟、权限与晋级闸门编排。
package promote

import (
	"errors"
	"fmt"
	"sync"

	"ontology/attest"
	"ontology/registry"
)

var (
	// ErrInvalidParam 与 registry.ErrInvalidParam 同一哨兵。
	ErrInvalidParam = registry.ErrInvalidParam
	// ErrClockRollback：now 小于已接受的最大 now。
	ErrClockRollback = errors.New("clock rolled back")
	// ErrNoNextStage：from 已是末级。
	ErrNoNextStage = errors.New("no next stage")
	// ErrForbidden：缺少所需（动作, 级名）权限。
	ErrForbidden = errors.New("permission denied")
	// ErrSource：Promote 的源标签不存在或已撤回。
	ErrSource = errors.New("source tag missing or yanked")
	// ErrDwell：驻留时间不足 to.S。
	ErrDwell = errors.New("dwell time not met")
	// ErrProofMissing：required 中存在无有效证明的类型；错误文本含第一个缺失类型。
	ErrProofMissing = errors.New("missing proof")

	ErrNotFound   = registry.ErrNotFound
	ErrTagYanked  = registry.ErrTagYanked
	ErrImmutable  = registry.ErrImmutable
	ErrAliasClash = registry.ErrAliasClash
	ErrReferenced = registry.ErrReferenced
)

// Caller 携带（动作, 级名）权限对集合。
type Caller struct {
	Permissions []registry.Permission
}

// Repo 是多级制品仓库门面。单把互斥锁串行化全部写操作，
// 使并发调用结果等价于某个串行顺序。
type Repo struct {
	mu     sync.Mutex
	reg    *registry.Store
	att    *attest.Store
	nowMax int64
}

// New 按给定顺序构建 2 到 8 级仓库。
func New(stages []registry.Stage) (*Repo, error) {
	reg, err := registry.NewStore(stages)
	if err != nil {
		return nil, err
	}
	return &Repo{reg: reg, att: attest.NewStore()}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func nonEmpty(ss ...string) bool {
	for _, s := range ss {
		if s == "" {
			return false
		}
	}
	return true
}

func (r *Repo) can(c Caller, act registry.Action, stage string) bool {
	for _, p := range c.Permissions {
		if p.Action == act && p.Stage == stage {
			return true
		}
	}
	return false
}

// tick 必须在锁内、参数检查通过后调用；被拒操作不推进时钟。
func (r *Repo) tick(now int64) error {
	if now < r.nowMax {
		return ErrClockRollback
	}
	return nil
}

// Push 只写首级，需要首级的 Push 权限。
func (r *Repo) Push(now int64, name, tag, digest string, caller Caller) error {
	if !validNow(now) || !nonEmpty(name, tag, digest) {
		return ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.tick(now); err != nil {
		return err
	}
	stages := r.reg.Stages()
	if !r.can(caller, registry.Push, stages[0].Name) {
		return ErrForbidden
	}
	if err := r.reg.Place(0, name, tag, digest); err != nil {
		return err
	}
	r.reg.SetFirst(stages[0].Name, name, digest, now)
	r.nowMax = now
	return nil
}

// Attest 提交绑定摘要的证明；exp 须严格大于 now。
func (r *Repo) Attest(now int64, digest, typ, signer string, exp int64) error {
	if !validNow(now) || !nonEmpty(digest, typ, signer) {
		return ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.tick(now); err != nil {
		return err
	}
	if exp <= now {
		return ErrInvalidParam
	}
	r.att.Add(attest.Attestation{Digest: digest, Type: typ, Signer: signer, Exp: exp})
	r.nowMax = now
	return nil
}

// RevokeSigner 撤销签名者；不影响已完成的晋级。
func (r *Repo) RevokeSigner(now int64, signer string) error {
	if !validNow(now) || signer == "" {
		return ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.tick(now); err != nil {
		return err
	}
	r.att.Revoke(signer)
	r.nowMax = now
	return nil
}

// Promote 把 from 级标签指向的摘要逐级晋到紧邻下一级，全部闸门重检。
func (r *Repo) Promote(now int64, name, tag, from string, caller Caller) error {
	if !validNow(now) || !nonEmpty(name, tag, from) {
		return ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	stages := r.reg.Stages()
	fromIdx, ok := r.reg.StageIndex(from)
	if !ok {
		return ErrInvalidParam
	}
	if err := r.tick(now); err != nil {
		return err
	}
	if fromIdx >= len(stages)-1 {
		return ErrNoNextStage
	}
	toIdx := fromIdx + 1
	to := stages[toIdx]
	if !r.can(caller, registry.Promote, to.Name) {
		return ErrForbidden
	}
	d, err := r.reg.Lookup(from, name, tag)
	if err != nil {
		return ErrSource
	}
	first, ok := r.reg.First(from, name, d)
	if !ok {
		return ErrSource
	}
	if now-first < to.S {
		return ErrDwell
	}
	if missing := r.att.MissingRequired(d, to.Required, to.Trusted, now); missing != "" {
		return fmt.Errorf("%w: %s", ErrProofMissing, missing)
	}
	if err := r.reg.Place(toIdx, name, tag, d); err != nil {
		return err
	}
	r.reg.SetFirst(to.Name, name, d, now)
	r.nowMax = now
	return nil
}

// Yank 撤回标签；被别名指向报被引用。
func (r *Repo) Yank(now int64, stage, name, tag string, caller Caller) error {
	if !validNow(now) || !nonEmpty(stage, name, tag) {
		return ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	idx, ok := r.reg.StageIndex(stage)
	if !ok {
		return ErrInvalidParam
	}
	if err := r.tick(now); err != nil {
		return err
	}
	if !r.can(caller, registry.Yank, stage) {
		return ErrForbidden
	}
	if err := r.reg.Yank(idx, name, tag); err != nil {
		return err
	}
	r.nowMax = now
	return nil
}

// SetAlias 建立或改指别名。
func (r *Repo) SetAlias(now int64, stage, name, alias, tag string, caller Caller) error {
	if !validNow(now) || !nonEmpty(stage, name, alias, tag) {
		return ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	idx, ok := r.reg.StageIndex(stage)
	if !ok {
		return ErrInvalidParam
	}
	if err := r.tick(now); err != nil {
		return err
	}
	if !r.can(caller, registry.Alias, stage) {
		return ErrForbidden
	}
	if err := r.reg.SetAlias(idx, name, alias, tag); err != nil {
		return err
	}
	r.nowMax = now
	return nil
}

// Resolve 先查别名再查标签；只读，不校验时钟。
func (r *Repo) Resolve(stage, name, ref string) (string, error) {
	if !nonEmpty(stage, name, ref) {
		return "", ErrInvalidParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.reg.StageIndex(stage); !ok {
		return "", ErrInvalidParam
	}
	return r.reg.Resolve(stage, name, ref)
}

// First 暴露（级,制品,摘要）首次进入时刻（测试/观测用）。
func (r *Repo) First(stage, name, digest string) (int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reg.First(stage, name, digest)
}

// AttScanned 返回上次 Promote 检视的证明条数（性能自证用）。
func (r *Repo) AttScanned() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.att.Scanned()
}

// ResolveTouched 返回上次 Resolve 触碰的记录数（性能自证用）。
func (r *Repo) ResolveTouched() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reg.Touched()
}
