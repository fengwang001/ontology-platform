# 使用指南：读取时裁决

```go
import ont "ontology/ontology"
```

## 1. 声明对象类型

```go
ot, err := ont.NewObjectType("Person", []ont.PropertyDecl{
    {Name: "id", Type: ont.TypeInt},
    {Name: "ssn", Type: ont.TypeString},
})
```

## 2. 登记行级与属性级策略（合并模式各自独立）

```go
rows := ont.RowPolicySet{
    Mode: ont.AllowOverrides, // 或 ont.DenyOverrides
    Rules: []ont.RowRule{{
        ID:       "clerk-row",
        Selector: ont.SubjectSelector{Groups: []string{"clerk"}},
        Predicates: []ont.Predicate{
            {Property: "id", Op: ont.CmpGe, Value: int64(0)}, // 原始值上求值
        },
        Effect: ont.EffectAllow,
    }},
}
props := ont.PropPolicySet{
    Mode: ont.DenyOverrides,
    Rules: []ont.PropRule{
        // 可读原始值
        {ID: "r-id", Property: "id", Selector: ont.SubjectSelector{MatchAll: true},
         HasRead: true, Readable: true},
        // 只读遮蔽值
        {ID: "r-ssn", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true},
         Mask: func(_ ont.Subject, raw ont.RawValue) (ont.RawValue, error) {
             return "***", nil // 输出必须满足 string 类型契约
         }},
        // 可写（写遮蔽示例：派生值需满足声明类型）
        {ID: "w-id", Property: "id", Selector: ont.SubjectSelector{Users: []string{"alice"}},
         HasWrite: true, Writable: true},
    },
}
```

## 3. 构造引擎、装入实例

```go
logger := &ont.SliceLogger{}
eng, err := ont.NewEngine(ont.Config{
    Type:      ot,
    Rows:      rows,
    Props:     props,
    WriteMode: ont.WriteRejectAll, // 或 ont.WriteDrop
    Logger:    logger,
})
inst, _ := ot.NewInstance("p1", map[string]ont.RawValue{"id": int64(42), "ssn": "123"})
_ = eng.AddInstance(inst)
```

省略的属性保存为“真正缺失”（raw NULL），不会被零值填充。

## 4. 读取

```go
res, derr := eng.Read(
    ont.Subject{ID: "alice", Groups: []string{"clerk"}},
    "p1",
    nil, // nil=全部声明属性；也可传投影，顺序与重复不影响结果
)
```

- 不存在或不可见：`derr.Kind == ont.ErrInstanceNotFound`（同一不透明错误）。
- 成功：`res.View`（可读且有值）、`res.Absent`（可读但缺失）、
  `res.Redacted`（存在但不可读，只给字段名）。
- 遮蔽值违约：`derr.Kind == ont.ErrMaskedTypeViolation`，不返回违约视图；
  只投影其他属性的读取不受影响。
- `res.Trace` 给出命中规则、raw/masked 结论与求值条数。

## 5. 写入

```go
out, derr := eng.Write(sub, "p1", map[string]ont.RawValue{"id": int64(7)})
```

错误固定优先级：不可见（`ErrInstanceNotFound`）> 属性不可写
（`ErrPropertyNotWritable`）> 遮蔽类型违约（`ErrMaskedTypeViolation`）>
原始值类型违约（`ErrInvalidValueType`）。被拒绝时版本/最后写入时间/值都
不变；`WriteDrop` 模式下不可写字段出现在 `out.Dropped`，其余在
`out.Applied`。
