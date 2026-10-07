// Package migration 负责对象类型版本迁移声明的表示、校验与兼容性判定。
//
// 一次迁移声明由若干「属性对应关系」(Mapping)组成,每个属性恰好属于三种
// 对应方式之一:保留(Retain)、新增且带默认值(AddDefault)、废弃(Deprecate)。
// 声明不允许自相矛盾(同一属性同时声明为保留与废弃等)。
//
// 声明支持在存量实例尚未全部回填时再次变更(追加新的对应关系,或修改尚未
// 对任何已回填实例生效的对应关系);但不允许修改已经生效过的对应关系。
//
// 关键设计:每次变更都会把全部历史对应关系折叠为一份「合成映射」,
// 因此现算新版本视图的开销只与实例自身的属性数量相关,
// 与声明累积的历史变更次数无关(见 ForwardViewStats 与对应测试)。
package migration

import (
	"errors"
	"fmt"
	"reflect"
)

// ErrInvalidMapping 表示迁移声明参数非法(自相矛盾、修改已生效的对应关系等)。
var ErrInvalidMapping = errors.New("invalid migration mapping")

// Action 描述某个属性在新旧两个版本之间的对应方式。
type Action int

const (
	// ActionRetain 保留:属性在两个版本中都存在,回填时原值携带。
	ActionRetain Action = iota
	// ActionAddDefault 新增:属性只存在于新版本,回填时写入默认值。
	ActionAddDefault
	// ActionDeprecate 废弃:属性只存在于旧版本,回填时丢弃。
	ActionDeprecate
)

func (a Action) String() string {
	switch a {
	case ActionRetain:
		return "retain"
	case ActionAddDefault:
		return "add-default"
	case ActionDeprecate:
		return "deprecate"
	default:
		return fmt.Sprintf("action(%d)", int(a))
	}
}

// Mapping 是单个属性在两个版本之间的对应关系声明。
type Mapping struct {
	Property string
	Action   Action
	// Default 仅在 Action 为 ActionAddDefault 时有效,且必须非 nil。
	Default any
}

func (m Mapping) String() string {
	if m.Action == ActionAddDefault {
		return fmt.Sprintf("%s:%s(default=%v)", m.Property, m.Action, m.Default)
	}
	return fmt.Sprintf("%s:%s", m.Property, m.Action)
}

func (m Mapping) valid() error {
	if m.Property == "" {
		return fmt.Errorf("%w: property name must not be empty", ErrInvalidMapping)
	}
	switch m.Action {
	case ActionAddDefault:
		if m.Default == nil {
			return fmt.Errorf("%w: property %q declared add-default requires a non-nil default", ErrInvalidMapping, m.Property)
		}
	case ActionRetain, ActionDeprecate:
		if m.Default != nil {
			return fmt.Errorf("%w: property %q declared %s must not carry a default", ErrInvalidMapping, m.Property, m.Action)
		}
	default:
		return fmt.Errorf("%w: property %q has unknown action %d", ErrInvalidMapping, m.Property, int(m.Action))
	}
	return nil
}

func sameMapping(a, b Mapping) bool {
	return a.Action == b.Action && reflect.DeepEqual(a.Default, b.Default)
}

// oldRule 是旧版本属性在合成映射中的规则。
type oldRule struct {
	deprecated bool
}

// ViewStats 记录一次视图现算的开销,用于验证开销上界。
type ViewStats struct {
	// ScannedProps 是扫描的实例自身属性个数。
	ScannedProps int
	// AppliedDefaults 是应用的新增属性默认值个数(即新视图新增的属性数)。
	AppliedDefaults int
}

// Ops 返回本次视图现算的总操作数,只与实例自身属性数量相关。
func (s ViewStats) Ops() int { return s.ScannedProps + s.AppliedDefaults }

// Declaration 是对象类型从旧版本到新版本的迁移声明。
//
// 内部始终维护一份合成映射:oldRules(旧版本属性的保留/废弃规则)与
// added(新版本新增属性及其默认值)。无论历史上追加过多少次变更,
// 视图现算都只查这两张平坦的表。
//
// Declaration 自身不加锁,并发串行化由上层(读写路由与仲裁模块)保证。
type Declaration struct {
	oldRules map[string]oldRule
	added    map[string]any
	// effective 记录已经对至少一个已回填实例生效过的对应关系(按属性名索引),
	// 这些对应关系不允许再被修改。
	effective map[string]bool
}

// NewDeclaration 返回一个空的迁移声明(等价于恒等迁移:未声明的旧属性默认保留)。
func NewDeclaration() *Declaration {
	return &Declaration{
		oldRules:  make(map[string]oldRule),
		added:     make(map[string]any),
		effective: make(map[string]bool),
	}
}

// Amend 变更迁移声明:可以追加新的对应关系,或替换尚未生效的对应关系;
// 修改已经生效的对应关系、或引入自相矛盾的对应关系会被拒绝。
//
// 校验全部通过后才统一应用,被拒绝的 Amend 不会改变声明的任何状态。
func (d *Declaration) Amend(batch []Mapping) error {
	if len(batch) == 0 {
		return fmt.Errorf("%w: empty amendment batch", ErrInvalidMapping)
	}
	// 第一步:批次内自检,同一属性出现两次且不一致即自相矛盾。
	deduped := make([]Mapping, 0, len(batch))
	seen := make(map[string]Mapping, len(batch))
	for _, m := range batch {
		if err := m.valid(); err != nil {
			return err
		}
		if prev, ok := seen[m.Property]; ok {
			if !sameMapping(prev, m) {
				return fmt.Errorf("%w: property %q declared both %s and %s in one batch",
					ErrInvalidMapping, m.Property, prev.Action, m.Action)
			}
			continue
		}
		seen[m.Property] = m
		deduped = append(deduped, m)
	}
	// 第二步:与现有声明比对,全部合法才允许应用。
	for _, m := range deduped {
		if d.conflicts(m) && d.effective[m.Property] {
			return fmt.Errorf("%w: property %q mapping already took effect on backfilled instances and cannot be modified",
				ErrInvalidMapping, m.Property)
		}
	}
	// 第三步:应用,把新对应关系折叠进合成映射。
	for _, m := range deduped {
		delete(d.oldRules, m.Property)
		delete(d.added, m.Property)
		switch m.Action {
		case ActionRetain:
			d.oldRules[m.Property] = oldRule{deprecated: false}
		case ActionDeprecate:
			d.oldRules[m.Property] = oldRule{deprecated: true}
		case ActionAddDefault:
			d.added[m.Property] = m.Default
		}
	}
	return nil
}

// conflicts 报告 m 与现有声明中同属性的对应关系是否不一致。
func (d *Declaration) conflicts(m Mapping) bool {
	switch m.Action {
	case ActionRetain:
		if r, ok := d.oldRules[m.Property]; ok {
			return r.deprecated
		}
		_, ok := d.added[m.Property]
		return ok
	case ActionDeprecate:
		if r, ok := d.oldRules[m.Property]; ok {
			return !r.deprecated
		}
		_, ok := d.added[m.Property]
		return ok
	case ActionAddDefault:
		if _, ok := d.oldRules[m.Property]; ok {
			return true
		}
		def, ok := d.added[m.Property]
		return ok && !reflect.DeepEqual(def, m.Default)
	default:
		return false
	}
}

// MarkEffective 在一次回填(含等价于回填的写入)实际应用后调用,
// 把本次真正用到的对应关系标记为已生效:新增属性的默认值总会被应用,
// 旧版本属性的保留/废弃规则在该属性真实出现于旧数据中时才被应用。
func (d *Declaration) MarkEffective(oldData map[string]any) {
	for p := range d.added {
		d.effective[p] = true
	}
	for p := range oldData {
		if _, ok := d.oldRules[p]; ok {
			d.effective[p] = true
		}
	}
}

// IsDeprecated 报告属性是否被声明为废弃(两个版本下都不可写)。
func (d *Declaration) IsDeprecated(property string) bool {
	r, ok := d.oldRules[property]
	return ok && r.deprecated
}

// IsAdded 报告属性是否被声明为新版本新增(旧版本下不可写)。
func (d *Declaration) IsAdded(property string) bool {
	_, ok := d.added[property]
	return ok
}

// ForwardView 按合成映射把旧版本数据现算为新版本视图:
// 废弃属性被丢弃,新增属性写入默认值,其余属性(显式保留或未声明)原样携带。
//
// 开销为 O(实例属性数 + 新增属性数),与历史变更次数无关。
// 返回的视图是新 map,不会别名入参。
func (d *Declaration) ForwardView(oldData map[string]any) map[string]any {
	view, _ := d.ForwardViewStats(oldData)
	return view
}

// ForwardViewStats 与 ForwardView 相同,但额外返回本次现算的开销统计,
// 用于以可验证的方式证明开销与历史变更次数无关。
func (d *Declaration) ForwardViewStats(oldData map[string]any) (map[string]any, ViewStats) {
	stats := ViewStats{ScannedProps: len(oldData), AppliedDefaults: len(d.added)}
	view := make(map[string]any, len(oldData)+len(d.added))
	for p, v := range oldData {
		if r, ok := d.oldRules[p]; ok && r.deprecated {
			continue
		}
		view[p] = v
	}
	for p, def := range d.added {
		view[p] = def
	}
	return view, stats
}

// ReverseView 按合成映射把新版本数据现算为旧版本视图:
// 新增属性被移除,其余属性原样携带(废弃属性在新版本中已不存在)。
// 开销为 O(实例属性数)。返回的视图是新 map,不会别名入参。
func (d *Declaration) ReverseView(newData map[string]any) map[string]any {
	view := make(map[string]any, len(newData))
	for p, v := range newData {
		if _, ok := d.added[p]; ok {
			continue
		}
		view[p] = v
	}
	return view
}
