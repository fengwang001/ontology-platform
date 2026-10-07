# 字段演进兼容性校验 — 设计说明

## 1. 目标与范围

判断对象类型一次字段定义变更（或一批变更）能否在**不迁移既有实例数据**
的前提下，被新版本的读写路径安全接受。实现位于 `ontology/` 包，纯 Go、
无外部依赖。

## 2. 变更分类（唯一、互斥）

`Classify(fc)` 对一次单字段变更按固定顺序判定，保证结果唯一：

| 顺序 | 条件 | 类别 |
| --- | --- | --- |
| 1 | `Old==nil, New!=nil`，`HasDefault` | `add-with-default` |
| 2 | `Old==nil, New!=nil`，无默认值 | `add-without-default` |
| 3 | `Old!=nil, New==nil` | `remove-field` |
| 4 | 类型名不同 | `change-type` |
| 5 | 同类型，新约束是旧约束的严格收紧 | `tighten-constraint` |
| 6 | 同类型，新约束是旧约束的严格放宽 | `loosen-constraint` |
| 7 | 约束等价（仅元数据变化） | `no-effective-change` |
| 8 | 既含收紧又含放宽（约束不可比） | `mixed-constraint` |

关键点：

- “类型变了”与“约束变了”不同时计数——类型差异优先归 `change-type`，
  类别之间不重叠。
- 题目要求的六类全覆盖。`mixed-constraint` 是第六类之外的**显式哨兵**：
  例如枚举 `{a,b,c}` → `{a,d}`，同时删掉 `c`（收紧）加入 `d`（放宽），
  无法唯一归入收紧/放宽；单列类别并按**收紧语义**扫描全部存活取值。
- `no-effective-change` 也是哨兵：取值集合不变时仅默认值/可缺失性/描述
  的差异恒兼容（引用方语义仍会单独检查）。

“收紧/放宽”由 `Constraint` 接口自身声明（`TighteningOf`/`LooseningOf`），
内置 `rangeConstraint`（上下界移动方向）、`enumConstraint`（合法值集合
严格子集）、`anyConstraint`（最松）三种实现。

## 3. 各类别的兼容性规则

- **新增带默认值**：默认值必须自身合法（过类型与约束）；既有实例读路径
  隐式取默认值，不需要扫描。默认值非法 → `constraint-value-violated`。
- **新增不带默认值**：
  - `AllowMissing=true` → 兼容，既有实例保持缺失；
  - 提供 `BackfillRule` 且对**每个**存活实例都能产出合法取值 → 兼容，
    提交时在同一临界区内回填；
  - 否则 → `missing-required`。只有一个实例回填失败也整体拒绝。
- **收紧**：扫描全部存活实例，任一既有取值不满足新约束 →
  `constraint-value-violated`，违规实例 ID 全部记录进依据。
- **放宽**：恒兼容，**不扫描实例**（依据 `loosen-no-scan`）。
- **mixed**：按收紧处理（扫描）。
- **改变类型**：每个既有取值必须同时满足
  1. `newType.CanReinterpret(oldType, value)`——无损、精确、由类型自身
     声明，禁止近似比较；
  2. 重解释结果满足新字段约束。
  两者分别失败时分别给出 `irreversible-type-change` 与
  `constraint-value-violated`，可在同一变更项上同时出现。
- **删除字段**：取值侧恒兼容（旧值在存储中保留但新路径不可见），兼容性
  完全由引用方决定。

### 无损重解释的取舍

内置四类 `int / float / string / bool`，声明如下（均为精确判定）：

- `int→string` 用十进制精确表示；`string→int` 必须 `ParseInt` 成功，
  `"1.0"` 不接受；
- `float→int` 仅当小数部分为零（`math.Modf`），不做四舍五入；
- `string→float` 除解析成功外还要求 `FormatFloat('g')` 往返一致，拒绝
  溢出与非规范表示；
- `bool→string` 为 `"true"/"false"`，反向仅接受这两个字符串，`"1"` 不
  近似接受。

判定不依赖任何 epsilon/相似度；`Value` 内部只存 Go 标量，比较即相等比较。

## 4. 外部引用方语义漂移

链接类型（链接判定）与动作（前置/后置条件）通过 `Reference` 表达依赖。
判定函数签名为 `Predicate(EffectiveView, *FieldSemantics)`：既可以依赖
实例取值，也可以依赖字段**语义本身**（取值域、默认值、类型）。

对每个引用方、每个存活实例，比较：

- 旧语义：`OldPredicate(旧投影视图, fc.Old)`；
- 新语义：链接类型可用 `NewJudgment` 提供迁移后的判定，动作默认沿用同
  一函数对 `fc.New` 求值；未提供迁移判定时，任何差异都算漂移。

任一实例上两个布尔结果不同 → `reference-semantic-drift`。该检查与取值类
检查互相独立，因此“取值兼容但引用漂移”的变更照样被整体拒绝。

## 5. 多类不兼容的完整暴露

`IncompatKind` 是位掩码（`constraint-value-violated |
irreversible-type-change | missing-required | reference-semantic-drift`）。
判定对一个变更项的四类检查全部执行并累加命中位，不做“命中即返回”。
打包提交再对各项做并集，因此一次提交触发的全部类别都会出现在
`BatchReport.Reasons` 中。

## 6. 打包提交的原子性

`ObjectType.Submit(changes)`：

1. 持锁；把每个变更的 `Old` 绑定到当前已提交定义；
2. `Checker.CheckBatch` 只读判定；
3. 任一项不兼容 → 直接返回 `*RejectError`，字段表与实例数据均未触碰
   （零状态变化，测试核对版本号、字段定义、实例数据三个层面）；
4. 全部通过后在同一临界区替换字段表、版本号 +1、执行必要的回填。

不存在“兼容项先生效”的中间状态。

### 乐观并发：`SubmitCAS(expectedVersion, changes)`

并发提交仅靠一把锁串行化**执行**是不够的：调用方持过期 `Old` 快照时，
可能把一次本应为放宽链下一步的提交变成“收紧回旧上界”。因此 CAS 同时
校验：

- 当前版本号必须等于 `expectedVersion`；
- 调用方给出的每个 `Old` 必须与当前已提交定义 `sameDef` 精确一致。

冲突返回 `ErrVersionConflict`，调用方重读定义后重试。`Submit` 是
`expectedVersion=-1` 的便捷封装。

## 7. 可串行化

`SubmitCAS` 与 `WriteInstance` 共用 `ObjectType.mu`，因此任意交织都
等价于把提交与写入按某一全序逐个执行。每次 `WriteInstance`：

- 在锁内一次性读取当前完整字段定义并校验、写入，返回该写入锁定的版本号；
- 不会观察到“两次提交之间”的混合定义。

`TestConcurrentSubmitsAndWritesLinearizable` 用 4 个提交者（CAS 驱动
16 步上界链）与 8 个写入者并发交织，再用一个独立的朴素串行模型核对：
每个接受写入的版本号都对应一个确定存在的已提交版本、取值在该版本下
合法、最终字段定义与实例数据均可由某个串行顺序解释。

## 8. 扫描开销与历史总量无关

`InstanceStore` 只持有存活实例 map；`Tombstone`/`MigrateOut` 立即
`delete`。`Scan` 每次回调对应一个存活实例，访问量 ≤ `LiveCount()`。
`ScannedSinceReset()` 暴露实际访问计数。`TestScanBoundedByLiveCount`
构造 500 个历史实例（全部删除/迁移）+ 7 个存活实例，验证一次收紧判定
的扫描计数恰好为 7，与历史总量 500 无关。

代价与取舍：这是“只存存活集”的直接结果；历史审计不放在实例主存储，
需要历史回放时应由独立的审计/归档存储承担，不进入兼容性判定路径。

## 9. 审计

`MemoryAuditLog`（实现 `AuditSink`）逐条记录字段、类别、变更描述、
是否兼容、命中原因与每条检查依据（含违规实例 ID），供事后核对。生产中
可替换为落库/落日志的实现。

## 10. 被放弃的方案

1. **异步双写/影子读校验**：在新版本后台慢慢校验实例。放弃原因：题目要求
   提交时给出确定结论，且不允许部分生效；异步方案存在长期不一致窗口。
2. **放宽时也全量扫描以“顺便核对数据质量”**：放弃，因为放宽在数学上恒
   兼容，扫描会引入与历史规模相关的不必要开销（题目明确要求放宽免检查）。
3. **类型转换用集中式转换函数表**：改为由 `DataType` 自身声明
   `CanReinterpret`，因为“无损”是类型自身语义的一部分，新增类型不应
   修改中心表。
4. **引用漂移只比较字段取值**：初版签名是 `Predicate(view)`，但“取值域
   放宽导致链接是否成立翻转”无法表达。改为同时传入 `*FieldSemantics`，
   这是开发中通过失败测试暴露并修正的设计点。
5. **只靠互斥锁保证并发正确**：锁只保证执行原子，不保证提交意图基于新鲜
   快照；`SubmitCAS` 的版本号 + `Old` 精确匹配双前置条件是必要补充。
6. **把不可比约束强行归为收紧或放宽**：会破坏分类互斥性；单列
   `mixed-constraint` 并按收紧处理。

## 11. 本地验证

```bash
# 环境无 go 时
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

go test -race -v ./...
go test -coverprofile=cov.out ./... && go tool cover -func=cov.out
go vet ./... && gofmt -l .
```

测试覆盖（`ontology/*_test.go`）：

- 六类分类的唯一性与互斥（含枚举收紧/放宽/不可比）；
- 新增带默认/不带默认（允许缺失、完整回填、单个实例回填失败）；
- 收紧的“一个违规即拒绝”与违规删除后放行；放宽免扫描；
- 类型变更无损边界（`"1.0"`/`"1"`、`1.5`/`2.0`、bool 精确往返等）；
- 引用漂移否决本来取值兼容的变更、引用方迁移判定后放行；
- 同一变更项三/四类不兼容同时返回；打包部分不兼容整体拒绝且零状态变化；
- 扫描量 = 存活数，与历史总量无关；
- 并发提交/写入交织与朴素串行模型对照（`-race` 下重复多轮）；
- 审计记录包含变更、依据、结论。
