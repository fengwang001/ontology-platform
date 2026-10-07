package bitemporal

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	errEmptyID     = errors.New("bitemporal: empty id")
	errUnknownType = errors.New("bitemporal: unknown link type")
	errBadCard     = errors.New("bitemporal: invalid cardinality")
	errSymmetric   = errors.New("bitemporal: symmetric link type requires identical endpoint types")
	errBadVersion  = errors.New("bitemporal: rule version timestamps must strictly increase")
	errBadSide     = errors.New("bitemporal: invalid legacy side token")
	errBadTime     = errors.New("bitemporal: record time precedes valid time")
)

// Store 是双时态事实的唯一写入入口与快照持有者。
// 所有历史事实只追加；快照不可变，写入通过原子换版发布。
type Store struct {
	mu sync.RWMutex
	s  *Snapshot
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{s: newSnapshot()}
}

// RegisterObjectType 登记对象类型及其诞生的记录时间。
func (st *Store) RegisterObjectType(id ID, bornRecord int64) error {
	if id == "" {
		return errEmptyID
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if born, ok := st.s.objectBorn[id]; ok && born != bornRecord {
		return fmt.Errorf("bitemporal: object type %q already registered at %d", id, born)
	}
	ns := st.s.clone()
	ns.objectBorn[id] = bornRecord
	st.s = ns
	return nil
}

// RegisterLinkType 登记链接类型及其首个基数约束版本（fromRecord 起生效）。
func (st *Store) RegisterLinkType(lt LinkType, card Cardinality, fromRecord int64) error {
	if lt.ID == "" || lt.SourceType == "" || lt.TargetType == "" {
		return errEmptyID
	}
	if lt.Symmetric && lt.SourceType != lt.TargetType {
		return errSymmetric
	}
	if err := validateCard(card); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, exists := st.s.types[lt.ID]; exists {
		return fmt.Errorf("bitemporal: link type %q already registered", lt.ID)
	}
	rule := LinkTypeRule{LinkType: lt, Card: card, FromRecord: fromRecord}
	ns := st.s.clone()
	ns.types[lt.ID] = &typeKey{lt: lt, rules: []LinkTypeRule{rule}}
	ns.ruleGen++
	st.s = ns
	return nil
}

// PutRule 追加一个基数约束版本；历史版本永不被覆盖。
func (st *Store) PutRule(linkType ID, card Cardinality, fromRecord int64) error {
	if err := validateCard(card); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	tk, ok := st.s.types[linkType]
	if !ok {
		return errUnknownType
	}
	if fromRecord <= tk.rules[len(tk.rules)-1].FromRecord {
		return errBadVersion
	}
	rules := make([]LinkTypeRule, 0, len(tk.rules)+1)
	rules = append(rules, tk.rules...)
	rules = append(rules, LinkTypeRule{LinkType: tk.lt, Card: card, FromRecord: fromRecord})
	ntk := &typeKey{lt: tk.lt, rules: rules}
	ns := st.s.clone()
	ns.types[linkType] = ntk
	ns.ruleGen++
	// 规则变化后该类型的审计索引作废，标记为脏以便读取时重建。
	ns.dirty[linkType] = true
	st.s = ns
	return nil
}

// RecordCreate 以规范化方式记录一条链接创建事实（任一端记录一次即可）。
func (st *Store) RecordCreate(linkType, a, b ID, validAt, recordedAt int64) error {
	return st.record(linkType, a, b, validAt, recordedAt, kindCreate, canonicalSide)
}

// RecordRevoke 以规范化方式记录一条链接撤销事实。
func (st *Store) RecordRevoke(linkType, a, b ID, validAt, recordedAt int64) error {
	return st.record(linkType, a, b, validAt, recordedAt, kindRevoke, canonicalSide)
}

// IngestLegacyHalf 摄入一条按物理方向记录的历史遗留半事实，用于迁移对账。
// side 为 "f"/"r"（非对称）或 "ab"/"ba"（对称）。
func (st *Store) IngestLegacyHalf(linkType, a, b ID, validAt, recordedAt int64, create bool, side string) error {
	kind := kindCreate
	if !create {
		kind = kindRevoke
	}
	return st.record(linkType, a, b, validAt, recordedAt, kind, side)
}

// CurrentSnapshot 返回当前不可变快照（调用方无须持锁）。
func (st *Store) CurrentSnapshot() *Snapshot {
	st.mu.RLock()
	if len(st.s.dirty) == 0 {
		s := st.s
		st.mu.RUnlock()
		return s
	}
	dirty := st.s.dirty
	st.mu.RUnlock()

	st.mu.Lock()
	defer st.mu.Unlock()
	for id := range dirty {
		if !st.s.dirty[id] {
			continue
		}
		if tk := st.s.types[id]; tk != nil {
			st.s.index[id] = buildLinkIndex(tk, st.s.facts[id])
		}
		delete(st.s.dirty, id)
	}
	return st.s
}

const canonicalSide = "c"

func validateCard(c Cardinality) error {
	check := func(cd Card, name string) error {
		if cd.Min < 0 || cd.Max < 0 || (cd.Max != 0 && cd.Min > cd.Max) {
			return fmt.Errorf("%w: %s=%+v", errBadCard, name, cd)
		}
		return nil
	}
	if err := check(c.Forward, "forward"); err != nil {
		return err
	}
	return check(c.Reverse, "reverse")
}

func (st *Store) record(linkType, a, b ID, validAt, recordedAt int64, kind int, side string) error {
	if linkType == "" || a == "" || b == "" {
		return errEmptyID
	}
	if validAt > recordedAt {
		return errBadTime
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	tk, ok := st.s.types[linkType]
	if !ok {
		return errUnknownType
	}
	src, dst, token, err := orient(tk.lt, a, b, side)
	if err != nil {
		return err
	}
	f := linkFact{
		linkType:   linkType,
		src:        src,
		dst:        dst,
		symmetric:  tk.lt.Symmetric,
		kind:       kind,
		validTime:  validAt,
		recordTime: recordedAt,
		origin:     side,
		token:      token,
	}
	ns := st.s.clone()
	ns.facts[linkType] = append(append([]linkFact(nil), st.s.facts[linkType]...), f)
	sort.SliceStable(ns.facts[linkType], func(i, j int) bool {
		return factLess(ns.facts[linkType][i], ns.facts[linkType][j])
	})
	ns.dirty[linkType] = true
	st.s = ns
	return nil
}

// orient 将输入端点规范化为 (src, dst) 与物理方向 token。
func orient(lt LinkType, a, b ID, side string) (ID, ID, string, error) {
	if lt.Symmetric {
		switch side {
		case canonicalSide:
			if a <= b {
				return a, b, "ab", nil
			}
			return b, a, "ab", nil
		case "ab", "ba":
			// 遗留半事实：对象对按无序对规范化，token 保留物理方向。
			if a <= b {
				return a, b, side, nil
			}
			if side == "ab" {
				side = "ba"
			} else {
				side = "ab"
			}
			return b, a, side, nil
		default:
			return "", "", "", errBadSide
		}
	}
	switch side {
	case canonicalSide, "f":
		return a, b, "f", nil
	case "r":
		// 反向记录的遗留事实：物理方向为 b -> a（按源/目标语义）。
		return a, b, "r", nil
	default:
		return "", "", "", errBadSide
	}
}

// factLess 定义同一半事实流上的总顺序：
// (validTime, recordTime, kind) 升序；vt 与 rt 都相同时撤销优先（revoke wins）。
func factLess(x, y linkFact) bool {
	if x.validTime != y.validTime {
		return x.validTime < y.validTime
	}
	if x.recordTime != y.recordTime {
		return x.recordTime < y.recordTime
	}
	if x.kind != y.kind {
		return x.kind < y.kind
	}
	if x.token != y.token {
		return x.token < y.token
	}
	return x.origin < y.origin
}
