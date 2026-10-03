package policy

import (
	"errors"
	"strings"
	"sync"
)

const (
	ActionGet    = "obj:Get"
	ActionList   = "obj:List"
	ActionPut    = "obj:Put"
	ActionDelete = "obj:Delete"
)

const (
	EffectAllow = "Allow"
	EffectDeny  = "Deny"
)

var (
	ErrNotFound = errors.New("不存在")
	ErrInvalid  = errors.New("参数非法")
	ErrTooMany  = errors.New("过多")
)

const MaxStatements = 200

// actions 固定索引顺序 0=Get 1=List 2=Put 3=Delete。
var actions = []string{ActionGet, ActionList, ActionPut, ActionDelete}

var actionIndex = map[string]int{
	ActionGet: 0, ActionList: 1, ActionPut: 2, ActionDelete: 3,
}

var writes = map[string]bool{ActionPut: true, ActionDelete: true}

// ValidAction 判断动作是否为四个具体动作之一。
func ValidAction(a string) bool {
	_, ok := actionIndex[a]
	return ok
}

// IsWrite 判断具体动作是否为写动作（Put/Delete）。
func IsWrite(a string) bool { return writes[a] }

// Statement 是身份策略或权限边界中的一条语句。
type Statement struct {
	Effect    string
	Actions   []string // 具体动作名或 obj:*
	Resources []string // *、精确串（List 前缀可为空）或 x* 前缀模式
}

// BucketStatement 是桶策略语句，另带主体模式集合（t:X / p:Y / *）。
type BucketStatement struct {
	Statement
	Principals []string
}

// ValidActionPattern 判断动作模式合法性（四个具体名或 obj:*）。
func ValidActionPattern(p string) bool {
	return p == "obj:*" || ValidAction(p)
}

// MatchAction 判断动作模式是否命中具体动作。
func MatchAction(pattern, action string) bool {
	return pattern == "obj:*" || pattern == action
}

// ValidResourcePattern 判断资源模式合法性：
// *、不含 * 的精确串，或以单个结尾 * 的前缀模式 x*（x 可为空）。
func ValidResourcePattern(p string) bool {
	if p == "*" {
		return true
	}
	if strings.Contains(p, "*") {
		return strings.HasSuffix(p, "*") && strings.Count(p, "*") == 1
	}
	return true
}

// MatchResource 判断资源模式是否命中具体资源串（可为空）。
// 例：a/* 不匹配 a 而匹配 a/；* 匹配任意（含空）。
func MatchResource(pattern, resource string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(resource, pattern[:len(pattern)-1])
	default:
		return resource == pattern
	}
}

// ValidPrincipalPattern 判断桶策略主体模式合法性：t:X / p:Y / *，X、Y 非空。
func ValidPrincipalPattern(p string) bool {
	if p == "*" {
		return true
	}
	if strings.HasPrefix(p, "t:") || strings.HasPrefix(p, "p:") {
		return len(p) >= 3
	}
	return false
}

// MatchPrincipal 判断主体模式是否适用于 (p, t)。
func MatchPrincipal(pattern, p, t string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasPrefix(pattern, "t:"):
		return pattern[2:] == t
	default: // p:Y
		return pattern[2:] == p
	}
}

func validateStatement(s Statement) bool {
	if s.Effect != EffectAllow && s.Effect != EffectDeny {
		return false
	}
	if len(s.Actions) == 0 || len(s.Resources) == 0 {
		return false
	}
	for _, a := range s.Actions {
		if !ValidActionPattern(a) {
			return false
		}
	}
	for _, r := range s.Resources {
		if !ValidResourcePattern(r) {
			return false
		}
	}
	return true
}

func actionMask(patterns []string) int {
	mask := 0
	for _, pat := range patterns {
		if pat == "obj:*" {
			return 1<<4 - 1
		}
		if ai, ok := actionIndex[pat]; ok {
			mask |= 1 << ai
		}
	}
	return mask
}

// entry 是编译后的一条语句；actMask 标记它能匹配哪些具体动作（按动作类别预建索引）。
type entry struct {
	effect     string
	actMask    int
	resources  []string
	principals []string // 仅桶策略条目非空
}

func (e *entry) matchAction(action string) bool {
	ai, ok := actionIndex[action]
	return ok && e.actMask&(1<<ai) != 0
}

func (e *entry) matchResource(resource string) bool {
	for _, r := range e.resources {
		if MatchResource(r, resource) {
			return true
		}
	}
	return false
}

func (e *entry) matchPrincipal(p, t string) bool {
	for _, pat := range e.principals {
		if MatchPrincipal(pat, p, t) {
			return true
		}
	}
	return false
}

// Effect 返回条目效果。
func (e *entry) Effect() string { return e.effect }

// Hit 判断条目在给定资源（身份/边界条目）上是否命中。
func (e *entry) Hit(resource string) bool {
	return e.matchResource(resource)
}

// HitBucket 判断桶策略条目在 (p, t, resource) 上是否命中（须主体模式适用）。
func (e *entry) HitBucket(p, t, resource string) bool {
	return e.matchPrincipal(p, t) && e.matchResource(resource)
}

type compiledList struct {
	set     bool // 仅边界使用：区分未设置与空列表
	entries []entry
}

type snapshot struct {
	epoch     int
	identity  map[string][]entry // p -> entries
	boundary  map[string]compiledList
	bucketPol map[string][]entry // bucket -> entries
}

// Resolver 由 tenant 包适配，使 policy 包不反向依赖 tenant。
type Resolver interface {
	PrincipalExists(p string) bool
	BucketExists(bucket string) bool
}

// Store 保存整份不可变快照；成功的 Set 使纪元加 1，失败不改策略也不耗纪元。
type Store struct {
	mu   sync.RWMutex
	snap snapshot
	res  Resolver
}

func NewStore(res Resolver) *Store {
	return &Store{
		snap: snapshot{
			identity:  map[string][]entry{},
			boundary:  map[string]compiledList{},
			bucketPol: map[string][]entry{},
		},
		res: res,
	}
}

// Epoch 返回当前策略纪元（初始 0，尚无成功替换）。
func (s *Store) Epoch() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.epoch
}

func compile(stmts []Statement) []entry {
	entries := make([]entry, len(stmts))
	for i, st := range stmts {
		entries[i] = entry{
			effect:    st.Effect,
			actMask:   actionMask(st.Actions),
			resources: st.Resources,
		}
	}
	return entries
}

// SetIdentityPolicy 整份替换主体身份策略。
func (s *Store) SetIdentityPolicy(p string, stmts []Statement) error {
	if !s.res.PrincipalExists(p) {
		return ErrNotFound
	}
	if len(stmts) > MaxStatements {
		return ErrTooMany
	}
	for _, st := range stmts {
		if !validateStatement(st) {
			return ErrInvalid
		}
	}
	entries := compile(stmts)
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.snap
	ns.identity = cloneEntriesMap(s.snap.identity)
	ns.identity[p] = entries
	ns.epoch++
	s.snap = ns
	return nil
}

// SetBoundary 整份替换权限边界；空列表表示什么都不允许（区别于未设置）。
func (s *Store) SetBoundary(p string, stmts []Statement) error {
	if !s.res.PrincipalExists(p) {
		return ErrNotFound
	}
	if len(stmts) > MaxStatements {
		return ErrTooMany
	}
	for _, st := range stmts {
		if !validateStatement(st) {
			return ErrInvalid
		}
	}
	entries := compile(stmts)
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.snap
	ns.boundary = cloneBoundaryMap(s.snap.boundary)
	ns.boundary[p] = compiledList{set: true, entries: entries}
	ns.epoch++
	s.snap = ns
	return nil
}

// SetBucketPolicy 整份替换桶策略。
func (s *Store) SetBucketPolicy(bucket string, stmts []BucketStatement) error {
	if !s.res.BucketExists(bucket) {
		return ErrNotFound
	}
	if len(stmts) > MaxStatements {
		return ErrTooMany
	}
	for _, st := range stmts {
		if !validateStatement(st.Statement) {
			return ErrInvalid
		}
		if len(st.Principals) == 0 {
			return ErrInvalid
		}
		for _, pat := range st.Principals {
			if !ValidPrincipalPattern(pat) {
				return ErrInvalid
			}
		}
	}
	entries := make([]entry, len(stmts))
	for i, st := range stmts {
		entries[i] = entry{
			effect:     st.Effect,
			actMask:    actionMask(st.Actions),
			resources:  st.Resources,
			principals: st.Principals,
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.snap
	ns.bucketPol = cloneEntriesMap(s.snap.bucketPol)
	ns.bucketPol[bucket] = entries
	ns.epoch++
	s.snap = ns
	return nil
}

func cloneEntriesMap(m map[string][]entry) map[string][]entry {
	out := make(map[string][]entry, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneBoundaryMap(m map[string]compiledList) map[string]compiledList {
	out := make(map[string]compiledList, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Eval 是某一纪元完整策略集合上的判定视图，生命周期内不会看到半新半旧。
type Eval struct {
	snap   snapshot
	action string
	probes int
}

// Snapshot 返回固定纪元上的评估视图与该纪元。
func (s *Store) Snapshot(action string) (*Eval, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &Eval{snap: s.snap, action: action}, s.snap.epoch
}

// Probes 返回本次判定扫描的、动作模式能匹配所查动作的语句条数（非导出计数器的只读出口）。
func (e *Eval) Probes() int { return e.probes }

func (e *Eval) applicable(entries []entry) []entry {
	out := make([]entry, 0)
	for _, en := range entries {
		if en.matchAction(e.action) {
			e.probes++
			out = append(out, en)
		}
	}
	return out
}

// Identity 返回身份策略中动作能匹配的条目（顺序与设置顺序一致）。
func (e *Eval) Identity(p string) []entry {
	return e.applicable(e.snap.identity[p])
}

// Boundary 返回边界中动作能匹配的条目，以及是否设置了边界。
func (e *Eval) Boundary(p string) ([]entry, bool) {
	cl, ok := e.snap.boundary[p]
	if !ok || !cl.set {
		return nil, false
	}
	return e.applicable(cl.entries), true
}

// Bucket 返回桶策略中动作能匹配的条目（主体模式适配与资源命中由调用方按 HitBucket 判定）。
func (e *Eval) Bucket(bucket string) []entry {
	return e.applicable(e.snap.bucketPol[bucket])
}
