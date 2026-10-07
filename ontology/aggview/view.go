package aggview

import (
	"fmt"
	"math/big"
)

// Source 声明一种被聚合对象类型及其归属链接与数值属性。
type Source struct {
	ObjectType ID
	LinkType   ID
	PropType   ID
	Policy     Policy
}

// ViewDef 是聚合视图声明。
type ViewDef struct {
	Name      ID
	GroupType ID
	Sources   []Source
}

type sourceDef struct {
	src Source
}

type view struct {
	def       ViewDef
	srcByObj  map[ID]*sourceDef // 对象类型 -> 声明
	srcByLink map[ID]*sourceDef // 归属链接类型 -> 声明
	// agg[group][objType] 聚合；按 source 分别保存，使多对象类型互不影响。
	agg map[ID]map[ID]*Aggregate
	// versions[member][objType] 成员归属版本，用于并发归属冲突检测。
	versions map[ID]map[ID]int64
}

func validateDef(d ViewDef) error {
	if d.Name == "" {
		return fmt.Errorf("aggview: view name must not be empty")
	}
	if d.GroupType == "" {
		return fmt.Errorf("aggview: view %q: group type must not be empty", d.Name)
	}
	if len(d.Sources) == 0 {
		return fmt.Errorf("aggview: view %q: at least one source required", d.Name)
	}
	seenObj := map[ID]bool{}
	seenLink := map[ID]bool{}
	for _, src := range d.Sources {
		if src.ObjectType == "" || src.LinkType == "" || src.PropType == "" {
			return fmt.Errorf("aggview: view %q: source fields must not be empty", d.Name)
		}
		if src.ObjectType == d.GroupType {
			return fmt.Errorf("aggview: view %q: source object type %q must differ from group type",
				d.Name, src.ObjectType)
		}
		if src.Policy != PolicyFull && src.Policy != PolicyEvenShare {
			return fmt.Errorf("aggview: view %q: source %q: multi-group contribution policy must be explicit",
				d.Name, src.ObjectType)
		}
		if seenObj[src.ObjectType] {
			return fmt.Errorf("aggview: view %q: duplicate source object type %q", d.Name, src.ObjectType)
		}
		if seenLink[src.LinkType] {
			return fmt.Errorf("aggview: view %q: duplicate source link type %q", d.Name, src.LinkType)
		}
		seenObj[src.ObjectType] = true
		seenLink[src.LinkType] = true
	}
	return nil
}

func newView(d ViewDef) *view {
	v := &view{
		def:       d,
		srcByObj:  map[ID]*sourceDef{},
		srcByLink: map[ID]*sourceDef{},
		agg:       map[ID]map[ID]*Aggregate{},
		versions:  map[ID]map[ID]int64{},
	}
	for i := range d.Sources {
		sd := &sourceDef{src: d.Sources[i]}
		v.srcByObj[sd.src.ObjectType] = sd
		v.srcByLink[sd.src.LinkType] = sd
	}
	return v
}

// contribution 计算给定值在 k 个分组归属下，单个分组获得的贡献。
// 值不存在时贡献恒为零（且由调用方保证不增加实例计数）。
// PolicyFull: v/k；PolicyEvenShare: v/k。
func (sd *sourceDef) contribution(v Value, k int) *big.Rat {
	if !v.Present || k <= 0 {
		return big.NewRat(0, 1)
	}
	switch sd.src.Policy {
	case PolicyFull:
		return new(big.Rat).Set(v.Rat)
	default: // PolicyEvenShare
		return new(big.Rat).Quo(v.Rat, big.NewRat(int64(k), 1))
	}
}

func (v *view) bucket(group, objType ID) *Aggregate {
	byType, ok := v.agg[group]
	if !ok {
		byType = map[ID]*Aggregate{}
		v.agg[group] = byType
	}
	a, ok := byType[objType]
	if !ok {
		a = &Aggregate{Sum: big.NewRat(0, 1)}
		byType[objType] = a
	}
	return a
}

// addTo 把贡献 c 应用到分组桶；counted 控制实例计数（值存在才计数）。
func (v *view) addTo(group, objType ID, c *big.Rat, deltaCount int) {
	a := v.bucket(group, objType)
	a.Sum.Add(a.Sum, c)
	a.Count += deltaCount
}

// query 汇总一个分组下所有 source 类型的桶。
func (v *view) query(group ID) Aggregate {
	out := Aggregate{Sum: big.NewRat(0, 1)}
	for _, a := range v.agg[group] {
		out.Sum.Add(out.Sum, a.Sum)
		out.Count += a.Count
	}
	return out
}

func (v *view) version(member, objType ID) int64 {
	return v.versions[member][objType]
}

func (v *view) bumpVersion(member, objType ID) int64 {
	byObj, ok := v.versions[member]
	if !ok {
		byObj = map[ID]int64{}
		v.versions[member] = byObj
	}
	byObj[objType]++
	return byObj[objType]
}
