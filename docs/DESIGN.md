# 脱敏与可见性策略冲突裁决模块 — 设计说明

包路径：`ontology/policy`（Go，无第三方依赖）。入口为 `policy.Registry`。

## 1. 模型与判定流程

- **ObjectType / AttrType / Instance**：对象类型声明属性名、标量类型（string/int/float/bool）与取值约束（`MaxLen`、`Min`/`Max`、`Allowed` 枚举）。实例保存各属性的**原始值**。
- **VisibilityPolicy**：对 `(对象类型, 主体, 属性)` 给出 `Allow` 结论，可携带 `Predicate{CondAttr, Op, Value}`。谓词**只在原始值**上求值（支持 eq/ne/lt/le/gt/ge），条件属性自身的脱敏策略对判定完全不可见，原始条件值也不会因此进入呈现结果。
- **MaskingPolicy**：同一三元组上登记脱敏强度（weak<medium<strong）与派生规则（redact/hash/mask/const/copyDerived）。
- **Render(inst, subject)** 一次呈现严格按四个阶段顺序裁决，前一阶段失败即整体中止并返回可区分的类型化错误：

| 阶段 | 错误 `Kind` | 范围 |
|---|---|---|
| 1. 引用校验 | `MissingReference` | 未知对象类型/属性/谓词条件属性/copy 来源 |
| 2. 可见性裁决 | `VisibilityConflict` | 同一属性同时存在命中的 allow 与 deny |
| 3. 循环检测 | `MaskingCycle` | 最终生效的 copyDerived 规则构成环 |
| 4. 派生与类型校验 | `TypeViolation` | **仅单属性**，隔离汇报，不影响其他属性/其他主体 |

## 2. 关键取舍

1. **默认拒绝（default-deny）**：某属性没有任何命中的允许策略时即不可见。这是安全默认值；单独命中 deny 与"没有任何策略"都呈现为缺失，而只有 allow 与 deny **同时命中**才判为不可调和冲突。这样"显式拒绝"与"默认不可见"不会被误报为冲突。
2. **脱敏强度合并 = 取最大强度**。强度相同但规则不同需要确定结果：采用固定的**保护性偏序** redact > hash > mask > const > copyDerived（越不可逆/越不携带原文越优先），再以**策略 ID 字典序**兜底。因此合并结果对登记顺序不敏感（见 `TestRegistrationOrderIndependence`）。被放弃的方案：①"后来者优先"——随登记顺序变化，直接违背需求；②"强度相同即冲突报错"——拒绝给出确定呈现结果，也违背需求。
3. **拒绝压倒一切脱敏**：被拒属性不参与脱敏合并、不进入派生、也不参与循环检测；脱敏绝不能让本应不可见的属性以派生形态出现。
4. **copyDerived 依赖的是"最终呈现值"**：来源属性被拒绝、不存在或自身派生违约时，本属性派生输入为空；若因此不满足本属性的非空/类型约束，按单属性 `TypeViolation` 隔离处理，而不是让拒绝传播成整请求错误（来源属性真不存在属于阶段 1 的 `MissingReference`）。
5. **循环在阶段 3 对"合并后的最终规则图"检测**，使用三色 DFS 并将环规范化为从最小属性名开始的闭合路径，保证报告唯一、可复现，且不会进入不终止求值。被放弃方案：惰性求值时靠递归深度上限报错——错误不确定、信息不可复现。
6. **引用错误惰性到求值阶段汇报**：结构合法性（重复 ID、未知规则、坏谓词运算符）在 `Apply` 时拒绝整批变更；而"策略引用了未知属性/对象"按主体在 Render 的阶段 1 报 `MissingReference`，从而保证四类错误的固定优先顺序在同一请求内可判定。
7. **审计只记录已提交（committed）呈现**：阶段 1–3 中止的请求不写任何审计记录；阶段 4 的单属性违约仍属成功提交，会记录结果与逐属性错误。审计条目包含原始输入、最终输出、命中策略依据（`AttrBasis`）。

## 3. 原子性与并发

- 策略集整体存放在不可变 `snapshot` 中；`Apply` 复制输入构建新快照，在写锁内一次性替换指针，版本号自增。
- `Render` 只在读锁内取快照指针，随后无锁求值。一次请求永远只看到一个完整版本，不可能出现"部分旧、部分新"。
- 互斥锁 + 不可变快照给出**严格串行化（strict serializability）**：登记/变更/呈现的任意并发历史都等价于某个串行顺序。`TestConcurrentSerializabilityAndAtomicity` 在高频并发变更下断言呈现值只能是两种合法终态之一。

## 4. 考察开销与可观测证明

- 快照为每个 `(对象类型, 主体)` 维护策略二级索引；一次 Render 只扫描该键下的可见性与脱敏策略，与系统登记的策略总量无关，时间复杂度为 O(该对象类型属性数 + 相关策略数 + 派生边数)。
- 不依赖实现细节的证明：`Registry` 暴露公开计数器 `ExaminedVisibilityPolicies()/ExaminedMaskingPolicies()` 与总量 `TotalVisibilityPolicies()/TotalMaskingPolicies()`。
  `TestExaminationCostIndependentOfTotalPolicies` 注册仅 1 条相关策略但分别附带 20 与 10000 条无关策略，断言每次 Render 考察的策略数恒为 1/1。任何外部调用方都可复现该观测。

## 5. 朴素参照实现与差分测试

- `policy/naive.go` 是**独立维护**的朴素实现：线性扫描全部策略、Kahn 式消减法检测循环、显式递归备忘派生，刻意与生产引擎的数据结构和算法不同。
- `TestDifferentialAgainstNaive` 用固定种子随机生成 4000 组策略集与实例（覆盖悬空引用、allow/deny 冲突、copy 链与环、各类型的违约常量、全部标量类型与谓词），对三个主体逐项比对：错误类别（及规范化环）、最终呈现 map、逐属性错误集合。

## 6. 被放弃的其他方案

- 以布尔表达式/优先级数字让 allow 与 deny 自动调和：语义隐晦且无法满足"无法调和的直接冲突必须显式汇报"，故保留显式冲突错误。
- 在登记期即时求值所有可能实例：实例值未知，不可行；可见性必须在请求期按原始值判定。
- 全局单锁包住整个求值：实现简单但 Render 之间也被串行化；当前方案仅取快照指针时加读锁，求值完全并行。

## 7. 本地验证

```bash
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath   # 仅当 HOME 缓存只读时需要
go test ./...
go test -race -v ./policy
go vet ./...
gofmt -l .
go run ./cmd/demo
```

预期：全部用例通过，`policy` 包覆盖率约 88%+；demo 打印两个主体的呈现结果、原子变更后的新版本，以及完整 JSON 审计日志。
