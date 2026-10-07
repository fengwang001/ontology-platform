# 链接基数约束 × 属性级权限联合仲裁子系统 — 设计说明

## 1. 目标与范围

在本体平台上，一条链接（`LinkType(source: ObjectType.property → target: ObjectType.property)`）
的创建/删除结果，由两个相互独立的判定共同决定：

1. **基数账本**：两端「实例 × 链接来源属性」槽位上的现存链接数是否达到声明上限；
2. **属性级权限**：操作者对两端实例上参与链接的来源属性是否具有「可见」权限。

第三个模块（联合仲裁器）按**统一优先级**把两类判定归一化为可区分的错误类别。
本实现是一个无外部依赖的 Go 包（module `ontology`），入口为 `Platform`。

## 2. 模块划分

| 文件 | 职责 |
| --- | --- |
| `model.go` | 值对象与错误类别：`PropertySpec`/`LinkTypeSpec`/`Instance`/`Link`/`Visibility`/`ErrorClass`/`DecisionError` |
| `ledger.go` | **基数账本** `Ledger`：现存链接集合 + 两端槽位计数；只回答「存在/基数是否通过」，完全不感知权限 |
| `permissions.go` | **权限继承与覆盖解析** `Resolver`：类型默认、实例层 actor/role 覆盖、角色包含层级上的最近优先解析 |
| `arbiter.go` | **联合仲裁与错误归一化** `Platform`：持有 schema、`Ledger`、`Resolver`，互斥串行化所有变更 |
| `naive.go` | 独立朴素参考模型 `NaivePlatform`：全量声明、每次完整重遍历角色层级，仅供差分对照 |

## 3. 关键规则与取舍

### 3.1 可见性语义：不可见 = 存在性整体隐藏

- 链接对操作者可见，当且仅当操作者对**两端**来源属性都可见。
- 任一端不可见时，该链接从操作者的全部视图（列表、删除探测）中消失，
  而不是「看到链接存在但属性值打码」。
- 权限收紧只改变读取视图，**不经过账本**：不物理删链接、不释放基数槽位、
  不影响其他仍可见的操作者。放宽后链接立即重新出现。

### 3.2 覆盖模型：实例层覆盖 + 角色层级继承

- 类型层在属性上声明默认可见性（可默认可见也可默认不可见）。
- 实例层覆盖可声明在**操作者自身**（层级距离 0）或任意**角色**上；
  覆盖可放宽也可收紧，且只对沿层级可达该角色的操作者生效。
- 操作者直接属于若干角色；角色间通过 `contains` 形成有向层级
  （`A contains B` ⇒ `B` 是 `A` 的上层，`A` 的成员继承 `B` 的声明）。
- 择优规则：**层级距离最短**的覆盖胜出；距离相等取**声明时间更晚**
  （平台逻辑时钟的单调 `seq`）；没有任何直接/间接覆盖才退回类型默认。

### 3.3 统一仲裁优先级

`CreateLink` 严格按以下次序短路判定，四类错误可相互区分、只报第一个：

1. `invalid_argument`：链接类型/对象类型/属性未声明、实例类型与端点不符、空参等；
2. `permission_denied`：操作者对起点来源属性不可见，否则对终点来源属性不可见；
3. `source_cardinality_exceeded`：起点槽位已达上限；
4. `target_cardinality_exceeded`：终点槽位已达上限。

`DeleteLink` 不做基数判断，次序简化为：

1. `invalid_argument`；
2. `not_found`：**链接确实不存在**与**链接存在但操作者不可见**合并为完全相同的错误类别。

### 3.4 防止存在性侧信道的两个关键顺序

- **重复链接检测放在权限判定之后**。若先报 `already exists`，不可见操作者
  就能用它探测自己本不该感知的链接存在。对可见操作者重复创建仍归一化为
  `invalid_argument`（参数非法）。
- 删除时先判存在性；若存在且操作者不可见，返回与「不存在」字面同一类的
  `ErrNotFound`（消息可不同，类别相同，调用方只按类别分支）。

### 3.5 并发与重放

- `Platform` 用一把 `sync.Mutex` 把所有创建、删除、授权、角色关系变更
  原子化；锁获取顺序即全局串行顺序，每个变更消费单调递增的逻辑时钟值。
  该时钟同时承担「同距离覆盖，声明更晚者胜」的时间戳，因此：
  - 并发请求的最终状态必然等价于某个串行序列的结果；
  - 相同的操作序列重放，链接集合与每个操作者的可见集合完全确定。
- 测试：`concurrency_test.go` 在竞态检测下验证账本不变量
  （现存链接数恒等于两端槽位计数）；`TestReplayDeterminism` 验证重放一致。

### 3.6 解析复杂度与「可验证证明」

需求要求：解析成本不得随系统角色总数/层级整体规模增长，只与操作者到
**最近一次覆盖声明**的层级距离相关。实现分两层兑现：

1. **最近优先、命中即停的逐层 BFS**（`Resolver.findNearest`）：
   从操作者距离 0 开始逐层向外，某一层一旦找到覆盖，立即在该层
   （同层取最大 `seq`）返回，**绝不访问更远层**。因此访问量只取决于
   最近距离 d 内的邻域，与系统内角色总数无关。
2. **按操作者缓存最近覆盖结果**：两次声明变更之间的反复查询为 O(1)
   哈希命中（角色结构变更整体失效、目标上的新覆盖按目标失效）。

可验证方式由实现自行设计，这里选择**让算法直接自证**：
`ResolveTrace` 返回本次解析实际访问的主体数 `traversed`。
`TestResolutionCostDependsOnlyOnNearestDistance` 构造一条 20 层角色链，
最近覆盖固定在距离 3，再向系统注入 1000 个无关角色，断言 `traversed`
保持 4 不变；再用最近覆盖在距离 8 的独立结构断言 `traversed` 增长到 9，
且满足上界 `1 + d`（命中即停）。测试日志中可直接读到这些数字。

## 4. 被放弃的方案

- **把不可见实现为「链接可见、值打码」**：违反「存在性整体隐藏」，且会让
  基数槽位对不可见操作者可观测，放弃。
- **收紧权限时物理删除链接并释放基数**：会改变其他操作者的视图并破坏基数
  语义；需求明确禁止，改为纯读时过滤，账本零感知。
- **删除时对不可见链接返回 permission_denied**：会泄露链接存在，放弃；
  统一归一化为 `not_found`。注意这与创建场景「权限优先于基数」不冲突——
  删除根本不进行基数判定。
- **每次解析全量遍历角色图 + 全表扫描覆盖**：作为朴素模型保留用于差分，
  但不满足复杂度要求，生产路径使用命中即停 BFS + 缓存。
- **完全增量的最近覆盖传播索引**（角色图变更时反向传播更新所有操作者）：
  写入成本高、实现复杂；本场景读多写少，命中即停 BFS 已由 `traversed`
  自证与总规模无关，配合 O(1) 缓存足够，故不引入。
- **覆盖「撤销」原语**：覆盖语义需要只能被更晚声明覆盖（放宽/收紧均可），
  提供撤销会让最近距离的判定依赖墓碑状态，增加重放歧义；统一用「同主体
  同目标的更晚覆盖」表达一切变化。

## 5. API 一览（`Platform`）

- Schema/实例：`DefineObjectType`、`DefineLinkType`、`CreateInstance`
- 权限/角色：`GrantActor`、`GrantRole`、`AddActorRole`、`AddRoleContains`
- 链接：`CreateLink`、`DeleteLink`、`LinkExists`、`LinkVisible`、
  `VisibleLinks`、`AllLinks`、`SourceCount`、`TargetCount`、`Visibility`

错误通过 `*DecisionError` 返回，`err.Class` 为五类归一化类别之一
（创建只用前四类，删除只用 `invalid_argument`/`not_found`）。

## 6. 本地验证方法

需要 Go 1.26+；本机 Go 在 `/usr/local/go/bin`：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath   # 仅当 HOME 缓存目录只读时需要

go run ./cmd/server        # 进程内演示，打印各仲裁结论
go test ./...              # 全量测试（含随机差分）
go test -race -v ./...     # 竞态检测 + 逐条打印
ONTOLOGY_DIFF_VERBOSE=1 go test -run TestDifferentialAgainstNaiveModel -v
go vet ./... && gofmt -l .
```

测试清单与需求的对应：

| 需求点 | 测试 |
| --- | --- |
| 权限不可见优先于基数超限 | `TestErrorOrderPermissionBeforeCardinality` |
| 错误总次序（参数/权限/起点/终点） | `TestErrorOrderInvalidArgFirst`、`TestErrorOrderSourceBeforeTargetCardinality` |
| 收紧后隐藏但不影响他人/基数/物理存在 | `TestRevocationHidesWithoutDeletion` |
| 最近优先 + 同距离最新 | `TestRoleNearestAndLatestWins` |
| 删除不可见 ≡ 删除不存在（同类别） | `TestDeleteInvisibleEqualsNotFound` |
| 成本只与最近距离相关 | `TestResolutionCostDependsOnlyOnNearestDistance` |
| 大量随机序列对照朴素模型并打印输入/输出/依据 | `TestDifferentialAgainstNaiveModel` |
| 并发等价于某个全局串行顺序 / 重放确定 | `TestConcurrentOpsAreSerializable`、`TestReplayDeterminism` |

差分测试默认每种子内部静默、失败时打印尾部轨迹；加 `-v` 或设置
`ONTOLOGY_DIFF_VERBOSE=1` 后逐步打印 `INPUT / OUTPUT real vs naive / 判定依据`
（依据包含两端可见性、链接是否存在、两端槽位占用）。
