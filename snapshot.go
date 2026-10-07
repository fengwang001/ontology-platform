package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Property 描述对象类型的一个属性。ID 是跨快照稳定的身份标识，
// Key 是可变的对外名称（重命名只改变 Key，不改变 ID）。
type Property struct {
	ID   string       `json:"id"`
	Key  string       `json:"key"`
	Type PropertyType `json:"type"`
}

// ObjectType 描述一个对象类型。PrimaryKey 是主键属性的 ID，
// 实例身份由 (TypeID, 主键取值) 共同决定。
type ObjectType struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	PrimaryKey string              `json:"primaryKey"`
	Props      map[string]Property `json:"props"`
}

// LinkType 描述一个链接类型及其两端的对象类型。
type LinkType struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// Schema 是一份快照的结构定义。
type Schema struct {
	Types     map[string]ObjectType `json:"types"`
	LinkTypes map[string]LinkType   `json:"linkTypes"`
}

// ObjectRef 指向一个对象实例：(对象类型, 主键取值)。
type ObjectRef struct {
	TypeID string `json:"type"`
	PK     Value  `json:"pk"`
}

func (r ObjectRef) key() string { return r.TypeID + "\x1f" + r.PK.key() }

// Object 是一个对象实例。Values 以属性 ID（而非 Key）为索引，
// 因此属性重命名不会触碰任何实例数据。
type Object struct {
	TypeID string           `json:"type"`
	PK     Value            `json:"pk"`
	Values map[string]Value `json:"values"`
	ModRev uint64           `json:"modRev"`
}

func (o Object) key() string { return ObjectRef{TypeID: o.TypeID, PK: o.PK}.key() }

// Link 是一个链接实例，身份由 (链接类型, 源端点, 目标端点) 决定。
type Link struct {
	TypeID string    `json:"type"`
	Source ObjectRef `json:"source"`
	Target ObjectRef `json:"target"`
	ModRev uint64    `json:"modRev"`
}

func (l Link) key() string {
	return l.TypeID + "\x1f" + l.Source.key() + "\x1f" + l.Target.key()
}

// Tombstone 记录一次删除发生的修订号，使差异比对无需扫描
// 未变化的实体即可发现删除。Link 区分对象墓碑与链接墓碑。
type Tombstone struct {
	Link   bool   `json:"link"`
	ModRev uint64 `json:"modRev"`
}

// Snapshot 是本体平台在某一时点的不可变快照。
// Build 之后不得再修改；Builder 的写时复制保证这一约定。
type Snapshot struct {
	Lineage  string               `json:"lineage"`
	Revision uint64               `json:"revision"`
	Schema   Schema               `json:"schema"`
	Objects  map[string]Object    `json:"objects"`
	Links    map[string]Link      `json:"links"`
	Tombs    map[string]Tombstone `json:"tombstones"`
}

// Fingerprint 返回快照内容的规范哈希，可用于校验比对过程未修改输入。
func Fingerprint(s *Snapshot) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ValidateSnapshot 校验单份快照的结构完整性（错误类别一：
// 快照结构性损坏）。只检查快照内部自洽，不涉及跨快照判断。
func ValidateSnapshot(s *Snapshot) error {
	if s == nil {
		return fmt.Errorf("snapshot is nil")
	}
	if s.Lineage == "" {
		return fmt.Errorf("lineage is empty")
	}
	for id, t := range s.Schema.Types {
		if t.ID != id {
			return fmt.Errorf("object type %q: id mismatch with map key", id)
		}
		pk, ok := t.Props[t.PrimaryKey]
		if !ok {
			return fmt.Errorf("object type %q: primary key property %q missing", id, t.PrimaryKey)
		}
		if !pk.Type.Valid() {
			return fmt.Errorf("object type %q: primary key property %q has invalid type %q", id, pk.ID, pk.Type)
		}
		keys := map[string]bool{}
		for pid, p := range t.Props {
			if p.ID != pid {
				return fmt.Errorf("object type %q: property %q id mismatch with map key", id, pid)
			}
			if !p.Type.Valid() {
				return fmt.Errorf("object type %q: property %q has invalid type %q", id, pid, p.Type)
			}
			if keys[p.Key] {
				return fmt.Errorf("object type %q: duplicate property key %q", id, p.Key)
			}
			keys[p.Key] = true
		}
	}
	for id, lt := range s.Schema.LinkTypes {
		if lt.ID != id {
			return fmt.Errorf("link type %q: id mismatch with map key", id)
		}
		if _, ok := s.Schema.Types[lt.Source]; !ok {
			return fmt.Errorf("link type %q: source object type %q missing", id, lt.Source)
		}
		if _, ok := s.Schema.Types[lt.Target]; !ok {
			return fmt.Errorf("link type %q: target object type %q missing", id, lt.Target)
		}
	}
	for key, o := range s.Objects {
		if o.key() != key {
			return fmt.Errorf("object %q: key mismatch", key)
		}
		if o.ModRev > s.Revision {
			return fmt.Errorf("object %q: modRev %d exceeds snapshot revision %d", key, o.ModRev, s.Revision)
		}
		t, ok := s.Schema.Types[o.TypeID]
		if !ok {
			return fmt.Errorf("object %q: unknown object type %q", key, o.TypeID)
		}
		pkProp := t.Props[t.PrimaryKey]
		if !o.PK.ValidFor(pkProp.Type) {
			return fmt.Errorf("object %q: primary key value invalid for type %q", key, pkProp.Type)
		}
		pkVal, ok := o.Values[t.PrimaryKey]
		if !ok || !EqualValues(pkVal, o.PK) {
			return fmt.Errorf("object %q: primary key value missing or inconsistent in values", key)
		}
		for pid, v := range o.Values {
			p, ok := t.Props[pid]
			if !ok {
				return fmt.Errorf("object %q: value for unknown property %q", key, pid)
			}
			if !v.ValidFor(p.Type) {
				return fmt.Errorf("object %q: value of property %q invalid for type %q", key, pid, p.Type)
			}
		}
	}
	for key, l := range s.Links {
		if l.key() != key {
			return fmt.Errorf("link %q: key mismatch", key)
		}
		if l.ModRev > s.Revision {
			return fmt.Errorf("link %q: modRev %d exceeds snapshot revision %d", key, l.ModRev, s.Revision)
		}
		lt, ok := s.Schema.LinkTypes[l.TypeID]
		if !ok {
			return fmt.Errorf("link %q: unknown link type %q", key, l.TypeID)
		}
		if l.Source.TypeID != lt.Source || l.Target.TypeID != lt.Target {
			return fmt.Errorf("link %q: endpoint types do not match link type %q", key, l.TypeID)
		}
		if _, ok := s.Objects[l.Source.key()]; !ok {
			return fmt.Errorf("link %q: source endpoint %q missing", key, l.Source.key())
		}
		if _, ok := s.Objects[l.Target.key()]; !ok {
			return fmt.Errorf("link %q: target endpoint %q missing", key, l.Target.key())
		}
	}
	for key, tb := range s.Tombs {
		if tb.ModRev > s.Revision {
			return fmt.Errorf("tombstone %q: modRev %d exceeds snapshot revision %d", key, tb.ModRev, s.Revision)
		}
		if tb.Link {
			if _, ok := s.Links[key]; ok {
				return fmt.Errorf("tombstone %q: link still present", key)
			}
		} else {
			if _, ok := s.Objects[key]; ok {
				return fmt.Errorf("tombstone %q: object still present", key)
			}
		}
	}
	return nil
}
