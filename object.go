package ontology

import "sync/atomic"

// ObjectType 是对象类型，Parent 为其继承来源（不可变）。
type ObjectType struct {
	Name   string
	Parent *ObjectType
}

// Object 是对象实例。revoked 使用原子标志，供执行期间的后置校验读取。
type Object struct {
	id      string
	type_   *ObjectType
	revoked atomic.Bool
}

func (o *Object) ID() string        { return o.id }
func (o *Object) Type() *ObjectType { return o.type_ }

// Revoked 报告实例是否已被撤销。实现为原子读：
// 分派期在统一锁内读它做一次判定；执行期逻辑自身的后置校验也可无锁再读。
func (o *Object) Revoked() bool { return o.revoked.Load() }

func (o *Object) revoke() { o.revoked.Store(true) }

// ancestorChain 返回自具体类型向上的继承链（含自身）。
func ancestorChain(t *ObjectType) []*ObjectType {
	if t == nil {
		return nil
	}
	chain := []*ObjectType{t}
	for cur := t; cur.Parent != nil; cur = cur.Parent {
		chain = append(chain, cur.Parent)
	}
	return chain
}
