// Package registry 保存多级制品仓库的阶段定义、标签、撤回墓碑与别名。
package registry

import "errors"

// 哨兵错误：调用方用 errors.Is 区分拒绝原因。promote 包在此之上补充
// ErrClock / ErrNoNext / ErrForbidden / ErrSourceMissing / ErrDwell / ErrProof。
var (
	ErrInvalidParam = errors.New("invalid parameter")
	ErrReferenced   = errors.New("tag is referenced by an alias")
	ErrAliasClash   = errors.New("alias conflicts with existing tag or alias")
	ErrTagYanked    = errors.New("tag was yanked")
	ErrImmutable    = errors.New("tag is immutable")
	ErrNotFound     = errors.New("tag not found")
)

// Action 是调用者可携带的权限动作。
type Action string

const (
	Push    Action = "Push"
	Promote Action = "Promote"
	Yank    Action = "Yank"
	Alias   Action = "Alias"
)

// Permission 是（动作, 级名）权限对。
type Permission struct {
	Action Action
	Stage  string
}

// Stage 描述一级：名字、是否不可变、驻留秒数 S、所需证明类型与受信签名者。
type Stage struct {
	Name      string
	Immutable bool
	S         int64
	Required  []string
	Trusted   map[string]bool
}

type entryKind int

const (
	kindTag entryKind = iota
	kindTomb
)

type entry struct {
	kind   entryKind
	digest string
}

type key struct {
	stage string
	name  string
	tag   string
}

type aliasKey struct {
	stage string
	name  string
	alias string
}

type firstKey struct {
	stage  string
	name   string
	digest string
}

// Store 保存阶段定义以及全部标签、墓碑、别名和首次进入时刻。
// 本包不加锁：调用方（promote.Repo）负责串行化。
type Store struct {
	stages  []Stage
	index   map[string]int
	entries map[key]entry
	aliases map[aliasKey]string
	first   map[firstKey]int64

	// touched 仅为自证 Resolve 触碰记录数：别名一次 + 标签一次，至多 2。
	touched int
}

// NewStore 校验阶段配置并构建空仓库。
func NewStore(stages []Stage) (*Store, error) {
	if len(stages) < 2 || len(stages) > 8 {
		return nil, ErrInvalidParam
	}
	idx := make(map[string]int, len(stages))
	seenReqTypes := make(map[string]bool)
	for i, st := range stages {
		if st.Name == "" {
			return nil, ErrInvalidParam
		}
		if _, dup := idx[st.Name]; dup {
			return nil, ErrInvalidParam
		}
		if st.S < 0 || st.S > 1_000_000_000 {
			return nil, ErrInvalidParam
		}
		if len(st.Required) > 8 {
			return nil, ErrInvalidParam
		}
		seenReqTypes = make(map[string]bool)
		for _, t := range st.Required {
			if t == "" || seenReqTypes[t] {
				return nil, ErrInvalidParam
			}
			seenReqTypes[t] = true
		}
		if st.Trusted == nil {
			return nil, ErrInvalidParam
		}
		if i == 0 && (st.S != 0 || len(st.Required) != 0) {
			return nil, ErrInvalidParam
		}
		idx[st.Name] = i
	}
	return &Store{
		stages:  stages,
		index:   idx,
		entries: make(map[key]entry),
		aliases: make(map[aliasKey]string),
		first:   make(map[firstKey]int64),
	}, nil
}

// Stages 返回按顺序排列的阶段定义。
func (s *Store) Stages() []Stage { return s.stages }

// StageIndex 返回级名对应的序号；不存在返回 -1,false。
func (s *Store) StageIndex(stage string) (int, bool) {
	i, ok := s.index[stage]
	if !ok {
		return -1, false
	}
	return i, true
}

// Place 按落标签规则写入（Push 与 Promote 相同）。
// 检查顺序：别名冲突 > 标签已撤回 > 标签不可变。
func (s *Store) Place(stageIdx int, name, tag, digest string) error {
	st := s.stages[stageIdx]
	if _, clash := s.aliases[aliasKey{st.Name, name, tag}]; clash {
		return ErrAliasClash
	}
	k := key{st.Name, name, tag}
	cur, exists := s.entries[k]
	if exists && cur.kind == kindTomb {
		return ErrTagYanked
	}
	if exists && st.Immutable && cur.digest != digest {
		return ErrImmutable
	}
	s.entries[k] = entry{kindTag, digest}
	return nil
}

// Lookup 返回标签当前摘要；墓碑返回 ErrTagYanked，不存在返回 ErrNotFound。
func (s *Store) Lookup(stage, name, tag string) (string, error) {
	e, ok := s.entries[key{stage, name, tag}]
	if !ok {
		return "", ErrNotFound
	}
	if e.kind == kindTomb {
		return "", ErrTagYanked
	}
	return e.digest, nil
}

// First 返回（级,制品,摘要）首次进入时刻。
func (s *Store) First(stage, name, digest string) (int64, bool) {
	v, ok := s.first[firstKey{stage, name, digest}]
	return v, ok
}

// SetFirst 仅在不存在时记录首次进入时刻；重复进入不刷新。
func (s *Store) SetFirst(stage, name, digest string, now int64) {
	fk := firstKey{stage, name, digest}
	if _, ok := s.first[fk]; !ok {
		s.first[fk] = now
	}
}

// Yank 撤回标签：被别名指向报 ErrReferenced；不可变留墓碑，可变直接删除。
// 不存在返回 ErrNotFound，已撤回返回 ErrTagYanked。
func (s *Store) Yank(stageIdx int, name, tag string) error {
	st := s.stages[stageIdx]
	k := key{st.Name, name, tag}
	e, ok := s.entries[k]
	if !ok {
		return ErrNotFound
	}
	if e.kind == kindTomb {
		return ErrTagYanked
	}
	for ak, target := range s.aliases {
		if ak.stage == st.Name && ak.name == name && target == tag {
			return ErrReferenced
		}
	}
	if st.Immutable {
		s.entries[k] = entry{kindTomb, e.digest}
	} else {
		delete(s.entries, k)
	}
	return nil
}

// SetAlias 建立或改指别名。tag 须存活；alias 与现存标签/墓碑或别名同名报冲突。
func (s *Store) SetAlias(stageIdx int, name, alias, tag string) error {
	st := s.stages[stageIdx]
	if _, err := s.Lookup(st.Name, name, tag); err != nil {
		return err
	}
	ak := aliasKey{st.Name, name, alias}
	if _, exists := s.aliases[ak]; !exists {
		if _, clash := s.entries[key{st.Name, name, alias}]; clash {
			return ErrAliasClash
		}
	}
	s.aliases[ak] = tag
	return nil
}

// Resolve 先查别名再查标签，返回摘要。触碰记录至多 2（touched 自证）。
func (s *Store) Resolve(stage, name, ref string) (string, error) {
	s.touched = 0
	tag := ref
	if t, ok := s.aliases[aliasKey{stage, name, ref}]; ok {
		tag = t
	}
	s.touched = 1
	d, err := s.Lookup(stage, name, tag)
	s.touched = 2
	return d, err
}

// Touched 返回上次 Resolve 触碰的记录数（测试自证用）。
func (s *Store) Touched() int { return s.touched }
