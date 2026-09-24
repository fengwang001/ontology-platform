// Package zone 维护行组级轻量统计（min/max/空值计数）并实现谓词裁剪。
// 空值不进入 min/max；全空行组 HasMinMax=false。
package zone

// Stats 是一个行组的统计。Rows 为组内行数（含空值），Nulls 为空值数。
type Stats[T Ordered] struct {
	Rows      int
	Nulls     int
	HasMinMax bool
	Min, Max  T
}

// Ordered 限定本列式段支持的有序标量类型（标准库可用，避免外部依赖）。
type Ordered interface {
	~int64 | ~string
}

// Builder 逐行累积统计。onlyValid 控制空值不参与 min/max。
type Builder[T Ordered] struct{ s Stats[T] }

// NewBuilder 创建累积器。
func NewBuilder[T Ordered]() *Builder[T] {
	return &Builder[T]{}
}

// Add 加入一行。valid=false 表示空值：只增 Nulls，绝不触碰 min/max。
func (b *Builder[T]) Add(v T, valid bool) {
	b.s.Rows++
	if !valid {
		b.s.Nulls++
		return
	}
	if !b.s.HasMinMax {
		b.s.Min, b.s.Max, b.s.HasMinMax = v, v, true
		return
	}
	if v < b.s.Min {
		b.s.Min = v
	}
	if b.s.Max < v {
		b.s.Max = v
	}
}

// Snapshot 返回当前统计副本。
func (b *Builder[T]) Snapshot() Stats[T] { return b.s }

// Predicate 是可下推谓词。Keep 做行组级裁剪（只漏排、不误排）；
// Match 做行级判定，valid=false（空值）对任何数值谓词都不命中。
type Predicate[T Ordered] interface {
	Keep(s Stats[T]) bool
	Match(v T, valid bool) bool
}

// Eq / Lt / Le / Gt / Ge 为比较谓词。
type Eq[T Ordered] struct{ V T }
type Lt[T Ordered] struct{ V T }
type Le[T Ordered] struct{ V T }
type Gt[T Ordered] struct{ V T }
type Ge[T Ordered] struct{ V T }

func (p Eq[T]) Keep(s Stats[T]) bool {
	return s.HasMinMax && p.V >= s.Min && s.Max >= p.V
}
func (p Eq[T]) Match(v T, ok bool) bool { return ok && v == p.V }

func (p Lt[T]) Keep(s Stats[T]) bool    { return s.HasMinMax && s.Min < p.V }
func (p Lt[T]) Match(v T, ok bool) bool { return ok && v < p.V }

func (p Le[T]) Keep(s Stats[T]) bool    { return s.HasMinMax && s.Min <= p.V }
func (p Le[T]) Match(v T, ok bool) bool { return ok && v <= p.V }

func (p Gt[T]) Keep(s Stats[T]) bool    { return s.HasMinMax && s.Max > p.V }
func (p Gt[T]) Match(v T, ok bool) bool { return ok && v > p.V }

func (p Ge[T]) Keep(s Stats[T]) bool    { return s.HasMinMax && s.Max >= p.V }
func (p Ge[T]) Match(v T, ok bool) bool { return ok && v >= p.V }

// In 命中值集合。
type In[T Ordered] struct{ Set map[T]struct{} }

func (p In[T]) Keep(s Stats[T]) bool {
	if !s.HasMinMax {
		return false
	}
	for v := range p.Set {
		if v >= s.Min && s.Max >= v {
			return true
		}
	}
	return false
}
func (p In[T]) Match(v T, ok bool) bool {
	if !ok {
		return false
	}
	_, hit := p.Set[v]
	return hit
}

// Null / NotNull 为空值判定；它们是唯一能命中空行的谓词。
type Null[T Ordered] struct{}
type NotNull[T Ordered] struct{}

func (Null[T]) Keep(s Stats[T]) bool    { return s.Nulls > 0 }
func (Null[T]) Match(_ T, ok bool) bool { return !ok }

func (NotNull[T]) Keep(s Stats[T]) bool {
	return s.Rows-s.Nulls > 0
}
func (NotNull[T]) Match(_ T, ok bool) bool { return ok }

// And 为合取；所有支都保留行组才保留，行级也需全部命中。
type And[T Ordered] struct{ Parts []Predicate[T] }

func (p And[T]) Keep(s Stats[T]) bool {
	for _, q := range p.Parts {
		if !q.Keep(s) {
			return false
		}
	}
	return true
}
func (p And[T]) Match(v T, ok bool) bool {
	for _, q := range p.Parts {
		if !q.Match(v, ok) {
			return false
		}
	}
	return true
}
