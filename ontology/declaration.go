package ontology

import "fmt"

// declaration 是"对象类型版本声明与兼容性判定"模块。
//
// 它持有当前对象类型的迁移声明（每个属性的对应关系）、声明修订序号，
// 以及哪些对应关系已经对已回填实例生效（冻结）。
// 对该结构的一切访问都必须在 Store 的互斥锁内进行。
type declaration struct {
	objectType string
	from       Version
	to         Version
	// started 标记迁移是否已经发起。
	started bool
	// active 是当前生效的属性对应关系，以属性名为键。
	active map[AttrName]Mapping
	// revision 是声明修订序号，StartMigration 成功与每次合法修订成功后递增。
	// 实例记录自己转换时所用的修订序号，用于判断"是否已被抢先回填"。
	revision int64
	// frozen 记录已经对至少一个已回填实例生效过的对应关系，不得再修改。
	frozen map[AttrName]bool
}

func newDeclaration(objectType string, from, to Version) *declaration {
	return &declaration{
		objectType: objectType,
		from:       from,
		to:         to,
		active:     make(map[AttrName]Mapping),
		frozen:     make(map[AttrName]bool),
	}
}

// invalidf 构造参数非法错误，与 ErrNotFound 必须可用 errors.Is 区分。
func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
}

// validateAndApply 校验一批对应关系修订并在通过时原子应用：
// 全部通过才生效，任一条非法则整体拒绝、状态不变。
//
// 判定次序：
//  1. 同一批次内同一属性出现多次（含矛盾声明，例如同时 keep 与 drop）；
//  2. 修改已经对已回填实例生效（冻结）的对应关系；
//  3. 对应关系本身非法（未知 Kind；KindAdd 缺省值未给出）。
func (d *declaration) validateAndApply(mappings []Mapping) error {
	batch := make(map[AttrName]Mapping, len(mappings))
	for _, m := range mappings {
		if m.Attr == "" {
			return invalidf("mapping has empty attribute name")
		}
		if _, dup := batch[m.Attr]; dup {
			return invalidf("conflicting mappings for attribute %q: more than one declaration in the same request", m.Attr)
		}
		batch[m.Attr] = m
	}

	for attr := range batch {
		if d.frozen[attr] {
			return invalidf("mapping for attribute %q has already taken effect on backfilled instances and must not be modified", attr)
		}
	}

	for _, m := range mappings {
		switch m.Kind {
		case KindKeep, KindDrop:
		case KindAdd:
			if !m.Default.Set {
				return invalidf("added attribute %q must declare a default value", m.Attr)
			}
		default:
			return invalidf("mapping for attribute %q has unknown kind %d", m.Attr, m.Kind)
		}
	}

	for _, m := range mappings {
		d.active[m.Attr] = m
	}
	d.revision++
	return nil
}

// start 发起迁移；重复发起被视为参数非法。
func (d *declaration) start(m Migration) error {
	if d.started {
		return invalidf("migration for object type %q is already in progress", m.ObjectType)
	}
	if m.ObjectType != d.objectType {
		return invalidf("migration object type %q does not match %q", m.ObjectType, d.objectType)
	}
	if m.From != d.from || m.To != d.to {
		return invalidf("migration version pair %d->%d does not match %d->%d", m.From, m.To, d.from, d.to)
	}
	if err := d.validateAndApply(m.Mappings); err != nil {
		return err
	}
	d.started = true
	return nil
}

// mapping 返回某属性当前生效的对应关系；未声明时 ok 为 false。
func (d *declaration) mapping(attr AttrName) (Mapping, bool) {
	m, ok := d.active[attr]
	return m, ok
}

// freeze 在一次回填/转换真正应用后，冻结所使用的全部对应关系。
// 一旦冻结，后续修订再触碰这些属性即被拒绝。
func (d *declaration) freeze(used []AttrName) {
	for _, attr := range used {
		d.frozen[attr] = true
	}
}
