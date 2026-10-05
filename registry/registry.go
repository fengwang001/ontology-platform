// Package registry 实现多级制品仓库的核心状态：阶段配置、标签、
// 撤回墓碑、别名、首次进入时刻、单调时钟与调用者权限。
package registry

import (
	"errors"
	"fmt"
	"sync"
)

// 哨兵错误，按拒绝次序排列，调用方用 errors.Is 区分。
var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrClockRewind      = errors.New("clock rewind")
	ErrNoNextStage      = errors.New("no next stage")
	ErrPermissionDenied = errors.New("permission denied")
	ErrSourceTag        = errors.New("source tag missing or withdrawn")
	ErrReferenced       = errors.New("tag referenced by alias")
	ErrDwell            = errors.New("dwell time not satisfied")
	ErrAttestation      = errors.New("attestation missing")
	ErrAliasConflict    = errors.New("alias conflict")
	ErrYanked           = errors.New("tag yanked")
	ErrImmutable        = errors.New("tag immutable")
	ErrTagNotFound      = errors.New("tag not found")
)

const (
	// MaxNow 是 now 的合法上界（含）。
	MaxNow = int64(1_000_000_000_000)
	maxS   = int64(1_000_000_000)
)

// Action 是权限对中的动作。
type Action int

const (
	Push Action = iota
	Promote
	Yank
	Alias
)

func (a Action) String() string {
	switch a {
	case Push:
		return "Push"
	case Promote:
		return "Promote"
	case Yank:
		return "Yank"
	case Alias:
		return "Alias"
	}
	return "?"
}

// Permission 是（动作, 级名）权限对。
type Permission struct {
	Action Action
	Stage  string
}

// Caller 是调用者携带的权限对集合。
type Caller map[Permission]bool

// Has 报告调用者是否持有某级上的某动作权限。
func (c Caller) Has(a Action, stage string) bool {
	return c[Permission{Action: a, Stage: stage}]
}

// Stage 描述一级：S、Required、Trusted 是进入该级的条件。
type Stage struct {
	Name      string
	Immutable bool
	S         int64
	Required  []string
	Trusted   []string
}

type artKey struct {
	stage string
	name  string
}

type firstKey struct {
	stage  string
	name   string
	digest string
}

type stage struct {
	spec    Stage
	trusted map[string]bool
}

// Repo 是制品仓库。所有方法可并发调用，结果等价于某个串行顺序。
type Repo struct {
	sync.Mutex
	stages  []stage
	index   map[string]int
	tags    map[artKey]map[string]string
	aliases map[artKey]map[string]string
	tombs   map[artKey]map[string]bool
	first   map[firstKey]int64
	maxNow  int64
	scanned int
}

// New 按给定顺序建立 2 到 8 级。首级 S 须为 0 且 Required 为空。
func New(stages []Stage) (*Repo, error) {
	if len(stages) < 2 || len(stages) > 8 {
		return nil, fmt.Errorf("%w: need 2..8 stages, got %d", ErrInvalidArgument, len(stages))
	}
	r := &Repo{
		index:   make(map[string]int, len(stages)),
		tags:    make(map[artKey]map[string]string),
		aliases: make(map[artKey]map[string]string),
		tombs:   make(map[artKey]map[string]bool),
		first:   make(map[firstKey]int64),
	}
	for i, s := range stages {
		if s.Name == "" {
			return nil, fmt.Errorf("%w: empty stage name at %d", ErrInvalidArgument, i)
		}
		if _, dup := r.index[s.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate stage %q", ErrInvalidArgument, s.Name)
		}
		if s.S < 0 || s.S > maxS {
			return nil, fmt.Errorf("%w: stage %q S=%d out of range", ErrInvalidArgument, s.Name, s.S)
		}
		if len(s.Required) > 8 {
			return nil, fmt.Errorf("%w: stage %q has %d required types", ErrInvalidArgument, s.Name, len(s.Required))
		}
		seen := make(map[string]bool, len(s.Required))
		for _, t := range s.Required {
			if t == "" || seen[t] {
				return nil, fmt.Errorf("%w: stage %q bad required type %q", ErrInvalidArgument, s.Name, t)
			}
			seen[t] = true
		}
		if i == 0 && (s.S != 0 || len(s.Required) != 0) {
			return nil, fmt.Errorf("%w: first stage %q must have S=0 and no required", ErrInvalidArgument, s.Name)
		}
		trusted := make(map[string]bool, len(s.Trusted))
		for _, signer := range s.Trusted {
			trusted[signer] = true
		}
		r.index[s.Name] = i
		r.stages = append(r.stages, stage{spec: s, trusted: trusted})
	}
	return r, nil
}

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// CheckClockLocked 校验时钟不回退。调用方须持锁。
func (r *Repo) CheckClockLocked(now int64) error {
	if now < r.maxNow {
		return fmt.Errorf("%w: now=%d < max=%d", ErrClockRewind, now, r.maxNow)
	}
	return nil
}

// CommitClockLocked 在操作成功时推进时钟。调用方须持锁。
func (r *Repo) CommitClockLocked(now int64) {
	if now > r.maxNow {
		r.maxNow = now
	}
}

// StageIndexLocked 返回级名下标，不存在返回 -1。调用方须持锁。
func (r *Repo) StageIndexLocked(name string) int {
	if i, ok := r.index[name]; ok {
		return i
	}
	return -1
}

// NumStages 返回级数。
func (r *Repo) NumStages() int { return len(r.stages) }

// StageAtLocked 返回第 i 级的配置。调用方须持锁。
func (r *Repo) StageAtLocked(i int) Stage { return r.stages[i].spec }

// TrustedAtLocked 返回第 i 级的受信签名者集合。调用方须持锁。
func (r *Repo) TrustedAtLocked(i int) map[string]bool { return r.stages[i].trusted }

// FirstLocked 返回摘要首次进入某级的时刻。调用方须持锁。
func (r *Repo) FirstLocked(stageName, name, digest string) (int64, bool) {
	t, ok := r.first[firstKey{stageName, name, digest}]
	return t, ok
}

// SourceDigestLocked 返回源标签当前指向的摘要；标签不存在或已撤回报 ErrSourceTag。
func (r *Repo) SourceDigestLocked(stageName, name, tag string) (string, error) {
	d, ok := r.tags[artKey{stageName, name}][tag]
	if !ok {
		return "", fmt.Errorf("%w: %s/%s@%s", ErrSourceTag, stageName, name, tag)
	}
	return d, nil
}

// LandLocked 按落标签规则把 tag 指向 digest，Push 与 Promote 共用。
// now 用于在首次进入时记录 first。调用方须持锁。
// 顺序：别名冲突 > 标签已撤回 > 标签不可变。调用方须持锁。
func (r *Repo) LandLocked(now int64, stageName, name, tag, digest string) error {
	key := artKey{stageName, name}
	if _, ok := r.aliases[key][tag]; ok {
		return fmt.Errorf("%w: %s/%s alias %q exists", ErrAliasConflict, stageName, name, tag)
	}
	if r.tombs[key][tag] {
		return fmt.Errorf("%w: %s/%s tag %q", ErrYanked, stageName, name, tag)
	}
	idx := r.index[stageName]
	if cur, ok := r.tags[key][tag]; ok {
		if cur == digest {
			return nil // 幂等成功
		}
		if r.stages[idx].spec.Immutable {
			return fmt.Errorf("%w: %s/%s tag %q", ErrImmutable, stageName, name, tag)
		}
	}
	if r.tags[key] == nil {
		r.tags[key] = make(map[string]string)
	}
	r.tags[key][tag] = digest
	fk := firstKey{stageName, name, digest}
	if _, ok := r.first[fk]; !ok {
		r.first[fk] = now
	}
	return nil
}

// Push 只在首级落标签，需首级 Push 权限。
func (r *Repo) Push(now int64, name, tag, digest string, c Caller) error {
	if !validNow(now) || name == "" || tag == "" || digest == "" {
		return fmt.Errorf("%w: Push(now=%d, name=%q, tag=%q, digest=%q)", ErrInvalidArgument, now, name, tag, digest)
	}
	r.Lock()
	defer r.Unlock()
	if err := r.CheckClockLocked(now); err != nil {
		return err
	}
	first := r.stages[0].spec.Name
	if !c.Has(Push, first) {
		return fmt.Errorf("%w: Push on %q", ErrPermissionDenied, first)
	}
	if err := r.LandLocked(now, first, name, tag, digest); err != nil {
		return err
	}
	r.CommitClockLocked(now)
	return nil
}

// Yank 撤回标签：被别名指向时报被引用；不可变级留墓碑，可变级直接删除。
func (r *Repo) Yank(now int64, stageName, name, tag string, c Caller) error {
	if !validNow(now) || name == "" || tag == "" {
		return fmt.Errorf("%w: Yank(now=%d, name=%q, tag=%q)", ErrInvalidArgument, now, name, tag)
	}
	r.Lock()
	defer r.Unlock()
	idx := r.StageIndexLocked(stageName)
	if idx < 0 {
		return fmt.Errorf("%w: unknown stage %q", ErrInvalidArgument, stageName)
	}
	if err := r.CheckClockLocked(now); err != nil {
		return err
	}
	if !c.Has(Yank, stageName) {
		return fmt.Errorf("%w: Yank on %q", ErrPermissionDenied, stageName)
	}
	key := artKey{stageName, name}
	if _, ok := r.tags[key][tag]; !ok {
		if r.tombs[key][tag] {
			return fmt.Errorf("%w: %s/%s tag %q", ErrYanked, stageName, name, tag)
		}
		return fmt.Errorf("%w: %s/%s tag %q", ErrTagNotFound, stageName, name, tag)
	}
	for alias, target := range r.aliases[key] {
		if target == tag {
			return fmt.Errorf("%w: alias %q points at %q", ErrReferenced, alias, tag)
		}
	}
	delete(r.tags[key], tag)
	if r.stages[idx].spec.Immutable {
		if r.tombs[key] == nil {
			r.tombs[key] = make(map[string]bool)
		}
		r.tombs[key][tag] = true
	}
	r.CommitClockLocked(now)
	return nil
}

// SetAlias 建立或改指别名；tag 须存在且未撤回；alias 不得与现存标签或墓碑同名。
func (r *Repo) SetAlias(now int64, stageName, name, alias, tag string, c Caller) error {
	if !validNow(now) || name == "" || alias == "" || tag == "" {
		return fmt.Errorf("%w: SetAlias(now=%d, name=%q, alias=%q, tag=%q)", ErrInvalidArgument, now, name, alias, tag)
	}
	r.Lock()
	defer r.Unlock()
	if r.StageIndexLocked(stageName) < 0 {
		return fmt.Errorf("%w: unknown stage %q", ErrInvalidArgument, stageName)
	}
	if err := r.CheckClockLocked(now); err != nil {
		return err
	}
	if !c.Has(Alias, stageName) {
		return fmt.Errorf("%w: Alias on %q", ErrPermissionDenied, stageName)
	}
	key := artKey{stageName, name}
	if _, ok := r.tags[key][tag]; !ok {
		if r.tombs[key][tag] {
			return fmt.Errorf("%w: %s/%s tag %q", ErrYanked, stageName, name, tag)
		}
		return fmt.Errorf("%w: %s/%s tag %q", ErrTagNotFound, stageName, name, tag)
	}
	if _, ok := r.tags[key][alias]; ok {
		return fmt.Errorf("%w: %s/%s tag %q exists", ErrAliasConflict, stageName, name, alias)
	}
	if r.tombs[key][alias] {
		return fmt.Errorf("%w: %s/%s tombstone %q exists", ErrAliasConflict, stageName, name, alias)
	}
	if r.aliases[key] == nil {
		r.aliases[key] = make(map[string]string)
	}
	r.aliases[key][alias] = tag
	r.CommitClockLocked(now)
	return nil
}

// Resolve 先查别名再查标签，返回摘要；已撤回与不存在分别报
// ErrYanked 与 ErrTagNotFound。最多触碰 2 条记录。
func (r *Repo) Resolve(stageName, name, ref string) (string, error) {
	r.Lock()
	defer r.Unlock()
	if r.StageIndexLocked(stageName) < 0 {
		return "", fmt.Errorf("%w: unknown stage %q", ErrInvalidArgument, stageName)
	}
	key := artKey{stageName, name}
	if target, ok := r.aliases[key][ref]; ok {
		r.scanned++
		ref = target
	}
	if d, ok := r.tags[key][ref]; ok {
		r.scanned++
		return d, nil
	}
	if r.tombs[key][ref] {
		r.scanned++
		return "", fmt.Errorf("%w: %s/%s ref %q", ErrYanked, stageName, name, ref)
	}
	return "", fmt.Errorf("%w: %s/%s ref %q", ErrTagNotFound, stageName, name, ref)
}
