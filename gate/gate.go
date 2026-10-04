// Package gate 实现数据契约的版本发布闸门：订阅、豁免、发布与恢复。
//
// 所有操作可并发调用，等价于某个串行顺序；被拒绝的操作不改任何状态
// （含时钟、版本号与 Lagging）。拒绝原因按层级只报第一个：
// 参数非法 > 时钟回退 > 主题不存在 > 已存在/订阅者不存在 >
// 版本或字段不存在 > ErrNoChange > ErrIncompatible > ErrBlocked/ErrStillBroken。
package gate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/compat"
	"ontology/schema"
)

// 拒绝原因 sentinel，均可用 errors.Is 区分。
var (
	ErrInvalid            = errors.New("gate: invalid argument")
	ErrClockRegression    = errors.New("gate: clock regression")
	ErrSubjectNotFound    = errors.New("gate: subject not found")
	ErrAlreadyExists      = errors.New("gate: already exists")
	ErrSubscriberNotFound = errors.New("gate: subscriber not found")
	ErrVersionNotFound    = errors.New("gate: version not found")
	ErrFieldNotFound      = errors.New("gate: field not found")
	ErrNoChange           = errors.New("gate: no change")
	ErrIncompatible       = errors.New("gate: incompatible")
	ErrBlocked            = errors.New("gate: blocked")
	ErrStillBroken        = errors.New("gate: still broken")
)

// IncompatibleError 携带模式检查失败的方向与首个违规。
type IncompatibleError struct {
	Direction schema.Mode // Backward 或 Forward（Full 模式先查 Backward）
	Violation compat.Violation
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("%v: %s check: %v", ErrIncompatible, e.Direction, e.Violation)
}

func (e *IncompatibleError) Unwrap() error { return ErrIncompatible }

// Blocker 是一个阻塞发布的订阅者及其首个违规。
type Blocker struct {
	Consumer  string
	Violation compat.Violation
}

// BlockedError 携带全部阻塞者（按 consumer 名字节序升序）。
type BlockedError struct {
	Blockers []Blocker
}

func (e *BlockedError) Error() string {
	parts := make([]string, len(e.Blockers))
	for i, b := range e.Blockers {
		parts[i] = b.Consumer + " (" + b.Violation.String() + ")"
	}
	return fmt.Sprintf("%v: %s", ErrBlocked, strings.Join(parts, ", "))
}

func (e *BlockedError) Unwrap() error { return ErrBlocked }

// StillBrokenError 携带读者视图读 latest 失败的首个违规。
type StillBrokenError struct {
	Violation compat.Violation
}

func (e *StillBrokenError) Error() string {
	return fmt.Sprintf("%v: %v", ErrStillBroken, e.Violation)
}

func (e *StillBrokenError) Unwrap() error { return ErrStillBroken }

// Status 是订阅者的当前状态。
type Status struct {
	Pinned         int  // 当前锚定的版本号
	Lagging        bool // 是否因豁免放行而落后
	LaggingVersion int  // 被标为 Lagging 时的版本号（未 Lagging 时为 0）
}

type subscription struct {
	pinned      int
	fieldSet    map[string]struct{}
	view        []schema.Field // 读者视图：pinned 版本按原序投影到 fieldSet
	hasWaiver   bool
	waivedUntil int64
	lagging     bool
	laggingAt   int
}

type subject struct {
	mode     schema.Mode
	versions [][]schema.Field // versions[i] 即版本 i+1，只追加不修改
	subs     map[string]*subscription
}

// Gate 是发布闸门，持有全部主题与全局时钟。零值不可用，请用 New。
type Gate struct {
	mu       sync.Mutex
	hasNow   bool
	maxNow   int64 // 已接受的最大 now（全局，被拒绝的操作不推进）
	subjects map[string]*subject
}

// New 创建一个空闸门。
func New() *Gate {
	return &Gate{subjects: make(map[string]*subject)}
}

func validNow(now int64) bool {
	return now >= 0 && now <= schema.MaxNow
}

// checkClockLocked 实现"时钟回退"层，须在持锁状态下调用。
func (g *Gate) checkClockLocked(now int64) error {
	if g.hasNow && now < g.maxNow {
		return fmt.Errorf("%w: now=%d < max=%d", ErrClockRegression, now, g.maxNow)
	}
	return nil
}

func (g *Gate) acceptLocked(now int64) {
	if !g.hasNow || now > g.maxNow {
		g.maxNow, g.hasNow = now, true
	}
}

func (g *Gate) subjectLocked(name string) (*subject, error) {
	s, ok := g.subjects[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSubjectNotFound, name)
	}
	return s, nil
}

func cloneFields(fields []schema.Field) []schema.Field {
	out := make([]schema.Field, len(fields))
	copy(out, fields)
	return out
}

func sameFields(a, b []schema.Field) bool {
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

// validateNames 校验订阅/恢复用的字段名列表：1..64 个且不重复。
func validateNames(fields []string) error {
	if len(fields) == 0 || len(fields) > schema.MaxFields {
		return fmt.Errorf("%w: fields count %d", ErrInvalid, len(fields))
	}
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if _, ok := seen[f]; ok {
			return fmt.Errorf("%w: duplicate field %q", ErrInvalid, f)
		}
		seen[f] = struct{}{}
	}
	return nil
}

// project 求版本 ver 投影到 set 的读者视图（保持版本原序）。
func project(ver []schema.Field, set map[string]struct{}) []schema.Field {
	view := make([]schema.Field, 0, len(set))
	for _, f := range ver {
		if _, ok := set[f.Name]; ok {
			view = append(view, f)
		}
	}
	return view
}

// resolveView 校验 fields 是版本 ver 的字段名子集并返回投影视图。
func resolveView(ver []schema.Field, fields []string) ([]schema.Field, map[string]struct{}, error) {
	index := make(map[string]struct{}, len(ver))
	for _, f := range ver {
		index[f.Name] = struct{}{}
	}
	set := make(map[string]struct{}, len(fields))
	for _, name := range fields {
		if _, ok := index[name]; !ok {
			return nil, nil, fmt.Errorf("%w: %q", ErrFieldNotFound, name)
		}
		set[name] = struct{}{}
	}
	return project(ver, set), set, nil
}

// CreateSubject 建主题并登记版本 1。
func (g *Gate) CreateSubject(name string, mode schema.Mode, fields []schema.Field, now int64) (int, error) {
	if name == "" {
		return 0, fmt.Errorf("%w: empty subject", ErrInvalid)
	}
	if !mode.Valid() {
		return 0, fmt.Errorf("%w: unknown mode %d", ErrInvalid, int(mode))
	}
	if !validNow(now) {
		return 0, fmt.Errorf("%w: now %d out of range", ErrInvalid, now)
	}
	if err := schema.ValidateFields(fields); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClockLocked(now); err != nil {
		return 0, err
	}
	if _, ok := g.subjects[name]; ok {
		return 0, fmt.Errorf("%w: subject %q", ErrAlreadyExists, name)
	}
	g.subjects[name] = &subject{
		mode:     mode,
		versions: [][]schema.Field{cloneFields(fields)},
		subs:     make(map[string]*subscription),
	}
	g.acceptLocked(now)
	return 1, nil
}

// Subscribe 登记订阅者；读者视图必须能读 latest，否则 ErrStillBroken。
func (g *Gate) Subscribe(subj, consumer string, pinned int, fields []string, now int64) error {
	if consumer == "" {
		return fmt.Errorf("%w: empty consumer", ErrInvalid)
	}
	if !validNow(now) {
		return fmt.Errorf("%w: now %d out of range", ErrInvalid, now)
	}
	if err := validateNames(fields); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClockLocked(now); err != nil {
		return err
	}
	s, err := g.subjectLocked(subj)
	if err != nil {
		return err
	}
	if _, ok := s.subs[consumer]; ok {
		return fmt.Errorf("%w: subscriber %q", ErrAlreadyExists, consumer)
	}
	if pinned < 1 || pinned > len(s.versions) {
		return fmt.Errorf("%w: pinned %d", ErrVersionNotFound, pinned)
	}
	view, set, err := resolveView(s.versions[pinned-1], fields)
	if err != nil {
		return err
	}
	if v := compat.CanRead(view, s.versions[len(s.versions)-1]); v != nil {
		return &StillBrokenError{Violation: *v}
	}
	s.subs[consumer] = &subscription{pinned: pinned, fieldSet: set, view: view}
	g.acceptLocked(now)
	return nil
}

// Waive 登记豁免：until 须严格大于 now，在 t < until 时有效，重复覆盖。
func (g *Gate) Waive(subj, consumer string, until, now int64) error {
	if consumer == "" {
		return fmt.Errorf("%w: empty consumer", ErrInvalid)
	}
	if !validNow(now) {
		return fmt.Errorf("%w: now %d out of range", ErrInvalid, now)
	}
	if until <= now {
		return fmt.Errorf("%w: until %d <= now %d", ErrInvalid, until, now)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClockLocked(now); err != nil {
		return err
	}
	s, err := g.subjectLocked(subj)
	if err != nil {
		return err
	}
	sub, ok := s.subs[consumer]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSubscriberNotFound, consumer)
	}
	sub.hasWaiver, sub.waivedUntil = true, until
	g.acceptLocked(now)
	return nil
}

// Publish 发布新版本，成功返回版本号；被拒时不占号、不改任何状态。
func (g *Gate) Publish(subj string, fields []schema.Field, now int64) (int, error) {
	if !validNow(now) {
		return 0, fmt.Errorf("%w: now %d out of range", ErrInvalid, now)
	}
	if err := schema.ValidateFields(fields); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClockLocked(now); err != nil {
		return 0, err
	}
	s, err := g.subjectLocked(subj)
	if err != nil {
		return 0, err
	}
	latest := s.versions[len(s.versions)-1]
	// (1) 与 latest 逐字段全同。
	if sameFields(latest, fields) {
		return 0, fmt.Errorf("%w: subject %q version %d", ErrNoChange, subj, len(s.versions))
	}
	// (2) 模式检查只针对 latest。
	if s.mode == schema.Backward || s.mode == schema.Full {
		if v := compat.CanRead(fields, latest); v != nil {
			return 0, &IncompatibleError{Direction: schema.Backward, Violation: *v}
		}
	}
	if s.mode == schema.Forward || s.mode == schema.Full {
		if v := compat.CanRead(latest, fields); v != nil {
			return 0, &IncompatibleError{Direction: schema.Forward, Violation: *v}
		}
	}
	// (3) 订阅者检查：永远是旧读者读新数据，与 mode 无关。
	names := make([]string, 0, len(s.subs))
	for name := range s.subs {
		names = append(names, name)
	}
	sort.Strings(names)
	var blockers []Blocker
	var waived []*subscription
	for _, name := range names {
		sub := s.subs[name]
		if sub.lagging {
			continue
		}
		v := compat.CanRead(sub.view, fields)
		if v == nil {
			continue
		}
		if sub.hasWaiver && now < sub.waivedUntil {
			waived = append(waived, sub)
			continue
		}
		blockers = append(blockers, Blocker{Consumer: name, Violation: *v})
	}
	if len(blockers) > 0 {
		return 0, &BlockedError{Blockers: blockers}
	}
	// (4) 放行：新版本号 = latest+1，违规但被豁免者标为 Lagging。
	version := len(s.versions) + 1
	s.versions = append(s.versions, cloneFields(fields))
	for _, sub := range waived {
		sub.lagging, sub.laggingAt = true, version
	}
	g.acceptLocked(now)
	return version, nil
}

// Advance 把订阅者恢复到版本 to 并换用新的 fields，成功即清除 Lagging 与豁免。
func (g *Gate) Advance(subj, consumer string, to int, fields []string, now int64) error {
	if consumer == "" {
		return fmt.Errorf("%w: empty consumer", ErrInvalid)
	}
	if !validNow(now) {
		return fmt.Errorf("%w: now %d out of range", ErrInvalid, now)
	}
	if err := validateNames(fields); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClockLocked(now); err != nil {
		return err
	}
	s, err := g.subjectLocked(subj)
	if err != nil {
		return err
	}
	sub, ok := s.subs[consumer]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSubscriberNotFound, consumer)
	}
	if to <= sub.pinned || to > len(s.versions) {
		return fmt.Errorf("%w: to %d (pinned %d, latest %d)", ErrVersionNotFound, to, sub.pinned, len(s.versions))
	}
	view, set, err := resolveView(s.versions[to-1], fields)
	if err != nil {
		return err
	}
	if v := compat.CanRead(view, s.versions[len(s.versions)-1]); v != nil {
		return &StillBrokenError{Violation: *v}
	}
	sub.pinned, sub.fieldSet, sub.view = to, set, view
	sub.lagging, sub.laggingAt = false, 0
	sub.hasWaiver, sub.waivedUntil = false, 0
	g.acceptLocked(now)
	return nil
}

// Status 返回订阅者的 pinned、是否 Lagging 及其版本。
func (g *Gate) Status(subj, consumer string) (Status, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, err := g.subjectLocked(subj)
	if err != nil {
		return Status{}, err
	}
	sub, ok := s.subs[consumer]
	if !ok {
		return Status{}, fmt.Errorf("%w: %q", ErrSubscriberNotFound, consumer)
	}
	return Status{Pinned: sub.pinned, Lagging: sub.lagging, LaggingVersion: sub.laggingAt}, nil
}

// Latest 返回主题当前最高版本号。
func (g *Gate) Latest(subj string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, err := g.subjectLocked(subj)
	if err != nil {
		return 0, err
	}
	return len(s.versions), nil
}
