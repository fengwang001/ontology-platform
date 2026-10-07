package ontology

import (
	"fmt"
	"reflect"
)

// FieldType 声明受支持的字段类型。
type FieldType string

const (
	TypeInt     FieldType = "int"
	TypeFloat   FieldType = "float"
	TypeString  FieldType = "string"
	TypeBoolean FieldType = "boolean"
)

// FieldSchema 描述单个字段：类型、是否必填。
type FieldSchema struct {
	Type     FieldType
	Required bool
}

// AggregateDef 声明一个派生聚合值。
// 派生值在每条记录应用后按列表顺序增量更新，
// 钩子通过 ScopedView.Aggregate 读取到的恒为「当前前缀」的确定值。
type AggregateDef struct {
	Name   string
	Field  string
	Method AggregateMethod
}

// AggregateMethod 是聚合算子。
type AggregateMethod string

const (
	// AggSum 求和。
	AggSum AggregateMethod = "sum"
	// AggCount 记录条数。
	AggCount AggregateMethod = "count"
)

// Record 是一条待创建或待更新的实例记录。
// PK 为实例主键；Fields 为字段名到字段值的映射。
type Record struct {
	PK     string
	Fields map[string]Value
}

// ObjectType 描述一个对象类型：字段 schema、聚合定义与钩子注册表。
type ObjectType struct {
	Name       string
	Fields     map[string]FieldSchema
	Aggregates []AggregateDef
	Pre        PreHook
	Post       PostHook
}

// validateSchema 做字段类型与必填校验。
// 校验失败属于参数非法（KindParamInvalid），原因码 field_type_mismatch。
func (ot *ObjectType) validateSchema(rec Record) *HookError {
	for name, schema := range ot.Fields {
		val, ok := rec.Fields[name]
		if !ok {
			if schema.Required {
				return paramError(-1, "missing_field",
					fmt.Sprintf("field %q is required", name))
			}
			continue
		}
		if err := checkType(name, val, schema.Type); err != nil {
			return err
		}
	}
	for name := range rec.Fields {
		if _, ok := ot.Fields[name]; !ok {
			return paramError(-1, "unknown_field",
				fmt.Sprintf("field %q is not declared on type %q", name, ot.Name))
		}
	}
	return nil
}

func checkType(name string, val Value, want FieldType) *HookError {
	switch want {
	case TypeInt:
		if _, ok := val.(int64); !ok {
			return typeMismatch(name, want, val)
		}
	case TypeFloat:
		if _, ok := val.(float64); !ok {
			return typeMismatch(name, want, val)
		}
	case TypeString:
		if _, ok := val.(string); !ok {
			return typeMismatch(name, want, val)
		}
	case TypeBoolean:
		if _, ok := val.(bool); !ok {
			return typeMismatch(name, want, val)
		}
	default:
		return paramError(-1, "unknown_type", "unknown field type "+string(want))
	}
	return nil
}

func typeMismatch(name string, want FieldType, got Value) *HookError {
	return paramError(-1, "field_type_mismatch",
		fmt.Sprintf("field %q expects %s but got %s",
			name, want, reflect.TypeOf(got)))
}

// Semantics 声明整批导入的整体语义，二选一。
type Semantics string

const (
	// SemAllOrNothing 全有或全无语义。
	SemAllOrNothing Semantics = "ALL_OR_NOTHING"
	// SemBestEffort 尽力而为语义。
	SemBestEffort Semantics = "BEST_EFFORT"
)

// HookView 是钩子查询可见状态的只读接口。
// 生产实现（ScopedView）与朴素参照模型（naiveView）均实现它，
// 从而同一批钩子可被两者直接复用。
type HookView interface {
	Get(pk string) (Instance, bool)
	Exists(pk string) bool
	Field(pk, field string) (Value, bool)
	Aggregate(name string) (float64, bool)
	Count() float64
}

// PreHook 是每个对象类型注册的前置校验钩子。
// 它在单条记录被应用前触发；view 严格只包含：
//   - 已提交状态，以及
//   - 本批次中排在该记录之前、且已通过前置钩子的记录效果。
//
// 返回非 nil 即表示该记录被拒绝。
type PreHook func(ctx *HookContext, rec Record, view HookView) *HookError

// PostHook 是批次级后置校验钩子，仅全有或全无语义下触发一次。
type PostHook func(ctx *HookContext, view HookView) *HookError

// HookContext 携带钩子运行期信息（聚合值读取、访问度量等）。
type HookContext struct {
	// TypeName 为当前钩子所属对象类型。
	TypeName string
	// Index 为当前记录在输入列表中的下标。
	Index int
	// Probes 记录本钩子可见性解析的底层探测次数，用于复杂度验证。
	Probes int
}

// Registry 登记所有对象类型。
type Registry struct {
	types map[string]*ObjectType
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{types: map[string]*ObjectType{}}
}

// Register 登记一个对象类型，返回注册表自身以便链式调用。
func (r *Registry) Register(ot *ObjectType) *Registry {
	if ot.Fields == nil {
		ot.Fields = map[string]FieldSchema{}
	}
	r.types[ot.Name] = ot
	return r
}

// Lookup 查询已注册的对象类型。
func (r *Registry) Lookup(name string) (*ObjectType, bool) {
	ot, ok := r.types[name]
	return ot, ok
}
