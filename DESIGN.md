# 批量导入属性级权限守门器 · 设计说明

## 问题回顾

本体平台的批量导入一次携带多条记录，每条记录涉及多个属性字段，执行主体
对不同字段拥有不同的写权限。导入前必须统一声明两种模式之一：

- **原子模式（atomic）**：记录内任一字段不可写，则整条记录失败，不写入任何字段。
- **宽松模式（lenient）**：不可写字段被跳过，其余字段写入；跳过后须重新判定
  该记录对对象类型的必需属性约束。

## 关键设计取舍

### 1. 权限快照在发起时刻物化

`Execute` 进入临界区后第一件事是调用 `snapshotPermissionsLocked`，把当前全部
权限条目物化为以 `(主体, 类型, 属性)` 为键的哈希索引（`PermissionSnapshot`）。
此后整批所有记录的权限判定只读快照，不读 live 权限表。因此执行期间发生的权限
变更（包括本批尚未处理到的记录）对本次判定完全不可见。

取舍：快照是全量拷贝，成本为 O(权限条目总数)，发生在每批一次。备选方案是
“按主体过滤的增量快照”或“写时复制权限表”，前者在主体权限条目占比小时更省，
后者实现复杂且需要不可变数据结构。考虑到快照成本摊销到整批、且实现可审计性
最强，选择全量物化。

### 2. 单互斥锁实现可串行化

`Store` 持有一把 `sync.Mutex`，`Execute` 全程持锁。批内记录逐条处理、批与批
之间互斥，因此“并发批次等价于某个全局串行顺序”由构造直接成立，无需额外证明。

被放弃的方案：

- **记录级/对象级细粒度锁**：能提高并发度，但要证明“等价于某个串行顺序”需要
  引入依赖图或两阶段锁，复杂度与出错风险显著上升；批量导入是吞吐不敏感、
  正确性敏感的路径，不值得。
- **MVCC + 乐观重试**：读多写少场景有优势，但冲突重试会让“一条记录失败不影响
  其他记录”的语义变得含糊（重试可能改变判定所见的权限快照时刻），放弃。

### 3. 判定顺序硬编码为规范优先级

整批级：模式参数缺失或非法 → 发起主体不存在（命中即整批拒绝，不处理任何记录）。
记录级：类型不存在 → 语义不匹配 → 字段权限（按模式整条拒绝或逐字段跳过）→
跳过后必需属性约束。每条记录独立走完整条判定链，失败记录不触碰存储，
天然满足“批内不传染”。

### 4. 必需属性约束的二次判定

宽松模式跳过字段后，对每个必需属性判定其“生效值”是否存在：

- 该属性在记录字段中且未被跳过 → 满足；
- 更新语义且对象已有该属性的旧值 → 沿用旧值，满足；
- 创建语义无旧值可沿用 → 缺失即失败；
- 否则缺失，记录失败并整体回退（未写入任何字段——写入发生在全部判定通过之后，
  因此“回退”事实上是“尚未写入”，无需补偿日志）。

### 5. 与规模无关的权限判定

单条记录的权限判定只对该记录的字段逐一做哈希查询，每次查询计一次
`PermissionSnapshot.accesses`。测试 `TestPermissionLookupCountIndependentOfScale`
直接断言：访问次数 == 进入权限判定阶段的字段总数，与批内记录数、该类型历史
权限条目总数（10 / 1k / 100k 三档）均无关。这是可机器验证的性能证明，
而非仅靠复杂度论证。

### 6. 判定日志

`Logger` 接口接收结构化 `DecisionLog{Scope, Index, Input, Output, Basis}`，
每条记录恰好产生一条日志，批级拒绝各产生一条。测试断言每条记录都有且仅有
一条非空日志。生产环境可接 `log/slog` 适配器。

## 错误类别

| 类别 | 层级 | 含义 |
| --- | --- | --- |
| `batch_invalid_param` | 整批 | 模式缺失/非法，或主体不存在 |
| `type_not_found` | 记录 | 对象类型不存在 |
| `semantic_mismatch` | 记录 | 创建/更新语义与对象存在性不符 |
| `permission_denied` | 记录 | 原子模式下存在不可写字段 |
| `required_constraint` | 记录 | 跳过后必需属性缺失且无旧值沿用 |

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin   # 如 go 不在 PATH

go build ./...
go vet ./...
gofmt -l .                            # 无输出即格式正确
go test ./ontology/ -count=1          # 全量单测
go test ./ontology/ -count=1 -race    # 竞态检测（含并发批次用例）
go test ./ontology/ -run TestAgainstNaiveModel -count=1 -v
```

测试与规范的对应关系：

- `TestSameInputDifferentResultAcrossModes`：两种模式相同输入产生不同结果；
- `TestLenientRequiredConstraintNegative` / `...PositiveWithOldValue`：宽松模式
  必需属性二次判定的正反两面；
- `TestCreateVsUpdateOldValueReuse`：创建/更新语义下旧值沿用的差异；
- `TestPermissionSnapshotImmuneToMidRunChanges`：执行期间收回权限，已开始批次
  仍按发起时刻快照判定（通过测试钩子在处理完第一条记录后变更权限）；
- `TestAgainstNaiveModel`：300 个随机种子下，与独立实现的朴素模型
  （线性扫描权限、独立状态机）逐条结果与最终状态完全一致；
- `TestPermissionLookupCountIndependentOfScale`：性能要求的机器验证；
- `TestConcurrentBatchesSerializable`：并发批次结果与串行执行一致（`-race` 下运行）；
- `TestDecisionLogCoversEveryRecord`：每次判定的输入/输出/依据日志完整。
