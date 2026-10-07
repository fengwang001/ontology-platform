# 使用指南：读取时裁决模块

模块位于 `ontology/` 包，零外部依赖。核心装配顺序：

1. 定义 `ObjectType`（属性名 + `DeclaredType`）并 `RegisterType`；
2. 登记行级策略 `RegisterRowPolicy` 与属性级策略 `RegisterPropertyPolicy`；
3. 通过 `Store.Put` 放入实例（`Values` + 权威的 `Present` 集合）；
4. 用 `NewAdjudicator(cfg, catalog, store, audit)` 得到并发安全裁决器；
5. 调用 `Read(subject, type, id)` / `Write(subject, type, id, values)`。

## 配置

```go
cfg := ontology.Config{
    RowMode:      ontology.DenyOverrides, // 或 ontology.AllowOverrides
    PropertyMode: ontology.DenyOverrides,
    DefaultRow:   ontology.EffectDeny,    // 无行策略命中时
    DefaultRead:  ontology.EffectDeny,    // 无属性读策略命中时
    DefaultWrite: ontology.EffectDeny,    // 无属性写策略命中时
    WriteMode:    ontology.WriteReject,   // 或 ontology.WriteDrop
}
```

行级与属性级的合并模式分别声明；互斥结论同时命中时，结论由对应模式唯一
确定。

## 读取结果的四种属性状态

| 状态 | 含义 | `Fields` 中是否有键 |
| --- | --- | --- |
| `present` | 声明、已存值、可读（值可能被遮蔽） | 有 |
| `absent_unreadable` | 声明但主体不可读 | 无 |
| `absent_not_stored` | 声明但实例未存该值 | 无 |
| `absent_not_declared` | 属性名不属于该类型 | 无 |

用 `view.Status[name]` 判断已声明属性，用 `view.StatusFor(name)` 额外区分
“未声明”。零值（`0`、`""`、`false`）只要已存储且可读就会显式出现在
`Fields` 中。

## 错误类别（`DecisionError.Kind`）

| Kind | 含义 |
| --- | --- |
| `row_invisible` | 行级拒绝；不存在的实例与被隐藏实例返回同一种错误 |
| `property_not_writable` | `reject` 模式下被写属性不可写 |
| `mask_type_violation` | 遮蔽输出违反声明类型（读或写） |
| `unknown_property` | 写入了未声明属性（请求形状错误，先于策略裁决） |
| `value_type_violation` | 提交值本身违反声明类型（先于策略裁决） |

策略裁决三类错误的固定汇报优先级：
`row_invisible` > `property_not_writable` > `mask_type_violation`。

## 写入语义

- `WriteReject`：任一被写属性不可写 → 整笔拒绝，无任何字段、版本、时间
  变化。
- `WriteDrop`：不可写字段进入 `WriteResult.Dropped`，其余字段写入；只有
  确有字段被接受才推进版本与最后写入时间。
- 被遮蔽的写入值在落库前应用遮蔽函数并再次做类型校验。
- 裁决对属性名排序处理，结论与请求中字段排列顺序无关。

## 审计

`NewAuditLogger(io.Writer)` 接收一个写入端，每次读/写输出一行 JSON：
`op`、`subject`、`object_type`、`instance_id`、`input`、`output`、
`error`、以及 `basis`（命中的行策略 ID、逐属性命中的属性策略 ID、两种合并
模式、行级结论、`touched_policies`）。传 `nil` 只保留计数不落盘。

## 可观测的开销证明

`view.Basis.Touched`（读取）或 `WriteResult.Basis.Touched`（写入）等于该
主体在该类型上的候选策略数，与已登记策略总数、实例总数无关。
`overhead_test.go` 在噪声策略/实例扩大数百倍时断言该计数保持常数。

## 朴素参照

`NewNaiveAdjudicator(adj)` 提供忽略索引、线性扫描全部策略的参照实现，
接口与生产裁决器相同。随机差分测试（`naive_diff_test.go`）在大量随机
策略与操作序列上逐项对照两者。

完整可运行示例见 `ontology/example_test.go`，设计取舍见 `DESIGN.md`。
