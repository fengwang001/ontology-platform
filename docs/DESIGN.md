# 读取时裁决模块设计说明

本模块位于 `ontology/` 包，为单一对象类型提供行级可见性、属性级读遮蔽与
写路径强制检查。所有裁决在读取/写入的当下完成，策略从不物化为派生数据。

## 1. 核心模型

- `ObjectType`：有序属性声明（`PropertyDecl{Name, Type}`）。类型限定为
  `int64 / float64(非 NaN) / string / bool`，由 `DataType.CheckValue`
  统一校验。原始值与遮蔽值使用同一份类型契约。
- `Instance`：仅保存原始值。`rawCell{present, value}` 对每个属性槽给出
  唯一内部编码：
  - 槽存在且 `present=true`、值恰为零值 → **存在的零值**；
  - 槽存在但 `present=false`（或建实例时未提供）→ **真正缺失（raw NULL）**；
  - 属性未在类型上声明 → **属性不存在**。
  三者在存储层就是不同的数据形态，不依赖任何哨兵值。
- `Subject`：`{ID, Groups}`，策略只按主体/组匹配，不看原始属性。

## 2. 外部可观察的三态区分

`ReadResult` 用三个互不相交的集合同时呈现三态，不依赖“空值”语义：

- `View[name]`：可读且原始值存在 → 呈现值（原始或遮蔽）；零值照常出现。
- `Absent`：可读但原始值缺失（raw NULL）——“字段有读权限但没有值”。
- `Redacted`：属性存在但不可读——只暴露“字段存在”，绝不暴露任何值
  （原始值与遮蔽值都不暴露）。
- 未声明属性：请求投影时直接返回 `ErrUnknownProperty`，不出现在任何集合。

因此“存在的零值 / 缺失 / 不存在”在内部结构（cell 编码）与外部响应
（View / Absent / 报错）上都有唯一且稳定的表示。

## 3. 行级裁决

- 行规则 `RowRule{Selector, Predicates[], Effect}`：选择器命中且全部谓词
  为真才算命中；谓词之间是合取。
- **谓词只在原始值上求值。** 求值发生在任何遮蔽/读权限判断之前，因此
  谓词可引用主体不可读的属性；该属性随后仍只进 `Redacted`，值不外泄。
  缺失原始值只满足 `!=`，其余比较恒假。
- 行合并模式 `AllowOverrides / DenyOverrides` 在 `RowPolicySet.Mode`
  上单独声明；无任何规则命中时默认拒绝。

## 4. 属性级裁决（与行级模式相互独立）

- 属性规则 `PropRule` 用 `HasRead/HasWrite` 显式声明作用域，读结论与
  写结论分别累计、分别按 `PropPolicySet.Mode` 合并；读规则不会隐式拒绝
  写，反之亦然。两套合并模式独立配置，绝不混用。
- 互斥 allow/deny 的唯一裁决：同模式下冲突结论由该模式唯一决定
  （allow-overrides → allow，deny-overrides → deny），不存在未定义态。
- 遮蔽规则（`Mask != nil`）命中即表示“允许读遮蔽值”。多条遮蔽规则同时
  命中时，**取规则 ID 最小者**，使结果与登记顺序、map 遍历顺序无关。
  写遮蔽 `WriteMask` 同理。

## 5. 写路径

固定的错误优先级（跨本次请求的所有目标属性，与属性排列顺序无关）：

1. `ErrInstanceNotFound`：实例不存在 **或** 行级不可见。两者对外是同一
   不透明错误，无法据此探测隐藏实例是否存在；日志内部保留裁决依据。
2. `ErrPropertyNotWritable`：实例可见，但在 `WriteRejectAll` 模式下
   至少一个目标属性不可写。
3. `ErrMaskedTypeViolation`：写遮蔽函数报错，或其派生值不满足声明类型。
4. `ErrInvalidValueType`：无遮蔽时，提供的原始值本身不满足声明类型。

`ErrUnknownProperty` 作为输入校验在可见性之后、属性写裁决之前给出。

两种互斥写模式：

- `WriteRejectAll`：任一不可写属性 → 整体拒绝。
- `WriteDrop`：不可写属性进入 `WriteResult.Dropped` 被静默丢弃，其余
  正常写入；`Applied/Dropped` 按声明顺序返回。

任何拒绝都在提交前返回：值、`Version`、`LastWriteID` 均不变。提交通过
`store.mutate` 在单键写锁内一次性完成（拷贝 map 后整体替换），随后才
自增版本/写入序号。

## 6. 遮蔽类型冲突

读取时对每个被遮蔽属性运行 `Mask`，并用 `DataType.CheckValue` 校验结果。
失败（函数报错或违约）返回 `ErrMaskedTypeViolation`，且不返回违约视图
（`View=nil, Conflict!=nil`）。冲突按投影属性粒度报告：只投影未涉及的
属性时读取成功，冲突不波及其他读取请求。

## 7. 并发与线性化

- 每个实例 ID 一把 `sync.RWMutex`：读裁决取 RLock（同实例多读不互斥），
  写取 Lock；在锁内对实例做快照克隆，再在锁外计算派生视图。
- 被拒绝的写不进入提交临界区，因此在任何等价串行序中都不产生可观察
  状态变化；并发调用整体等价于某个串行交错。
- 键锁 map 由自身互斥保护，键锁只增不删，杜绝“锁被移除后仍被持有”。

## 8. 单次开销只与命中策略数相关

注册策略时建立候选索引：

- 行规则按 `subject ID / group / match-all` 分桶；
- 属性规则同样分桶。

一次调用只合并“选择器可能命中该主体”的候选（按组/用户去重），再对这些
候选评估谓词/合并。`DecisionTrace.RowRulesEvaluated /
PropRulesEvaluated` 以公开可观测字段返回实际求值条数，与总登记策略数、
系统实例总数无关。`TestComplexityBoundedByHittingRules` 在 500 条噪声
策略 + 1000 个额外实例下证明计数恒为真正命中主体的 2 条行规则 / 1 条
属性规则。证据不依赖内部实现，只读返回值中的 trace 字段即可复现。

## 9. 日志

每次调用生成一条 `CallRecord`：`seq/kind/subject/instance`、完整输入
`InputJSON`、最终输出 `OutputJSON`、错误类别，以及裁决依据 `Trace`
（合并模式、命中规则 ID、每个属性 raw/masked/deny 结论、求值计数）。
提供内存 `SliceLogger`、行式 `JSONLogger` 与 `MultiLogger`。规则 ID 在
trace 中排序输出，保证日志稳定、可与参照实现逐字段对照。

## 10. 关键取舍与被放弃方案

- **放弃“物化遮蔽列 / 异步重写”**：会在策略变更后产生陈旧派生值，破坏
  “同一主体同一时刻两次读取完全一致”。改为纯读取时裁决 + 仅存原始值。
- **放弃用零值表达缺失**：Go 零值与真实零值无法区分。采用显式
  `present` 位与 `Absent/Redacted` 双集合。
- **放弃 map 遍历序作为遮蔽规则决胜顺序**：不稳定。改为规则 ID 最小者
  胜出，并在差分测试中用打乱登记顺序验证。
- **放弃把不可见实例报成专门错误**：会泄露存在性。对外统一为
  `ErrInstanceNotFound`（与不存在同形态），细节仅留在受信日志。
- **放弃全局单锁存储**：简单但同实例并发读被写阻塞。改为每实例读写锁；
  朴素参照实现仍用全局全量扫描，二者在差分测试中等价。
- **错误优先级“按属性遇到顺序报”被放弃**：多属性请求会随顺序变化。
  改为先收集全部目标属性的结论，再按固定全局优先级（行不可见 >
  属性不可写 > 遮蔽类型违约 > 原始值类型违约）汇报。
- 遮蔽函数只接收 `(subject, 该属性原始值)`，不接收其他属性，避免经遮蔽
  函数发生侧向信息泄露。

## 11. 本地验证

```bash
# 需要 Go 1.26+；本仓库环境使用 /tmp/goshim/go（自带 GOCACHE/GOPATH）
/tmp/goshim/go test -race ./...
/tmp/goshim/go test -run TestDifferentialAgainstNaive -v ./ontology
/tmp/goshim/go test -coverprofile=coverage.out ./... && /tmp/goshim/go tool cover -func=coverage.out
/usr/local/go/bin/gofmt -l .
/tmp/goshim/go vet ./...
```

- 边界组合：`boundary_test.go`、`boundary2_test.go`（互斥策略、遮蔽违约、
  三态区分、两种写模式、顺序无关）。
- 并发：`concurrency_test.go`（线性化与拒绝无副作用，`-race` 下运行）。
- 复杂度：同文件中的 trace 计数证明。
- 差分：`naive_*_test.go`（朴素参照）+ `fuzzgen_test.go` /
  `differential_test.go`（140 个随机策略集合 × 50 个随机操作序列，
  逐字段对照读结果、写结果与管理员视角的原始状态快照）。
