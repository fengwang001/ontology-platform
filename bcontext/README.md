# bcontext：浏览上下文跨源隔离与权限策略求值内核

`package bcontext` 模拟顶层文档与多层嵌入框架组成的浏览上下文树，根据每份文档的
响应头与框架嵌入属性，精确推导跨源隔离状态与逐项能力可用性，并维护独立于树的
弹窗开启者组关系。

## 核心类型

- `OpenerPolicy`：`OpenerUnsafeNone` / `OpenerSameOrigin` / `OpenerSameOriginAllowPopups`。
- `EmbedderPolicy`：`EmbedderUnsafeNone` / `EmbedderRequireCorp` / `EmbedderCredentialless`。
- `DefaultAllowlist`：`DefaultSelf`（仅自身来源）/ `DefaultAll`（全部来源）。
- `Feature{RequiresIsolation, Default}`：能力是否要求隔离与其默认允许列表。
- `Document{Origin, Opener, Embedder, Permissions}`：一次装载的响应头；
  `Permissions` 中出现某能力键即“已声明”（空切片表示排除所有来源），未出现走默认列表。

## 内核操作

| 方法 | 语义 |
| --- | --- |
| `NewKernel(features, logger)` | 注册能力集合与日志器（可为 nil） |
| `NewTopLevel(doc)` | 新建顶层上下文并装载文档 |
| `LoadFrame(parentID, doc, allow)` | 在父框架下嵌入子框架；跨源严格准入在此校验 |
| `Navigate(ctxID, doc)` | 同上下文换文档；父链不变，父导航替换整棵子树 |
| `OpenPopup(openerID, doc)` | 开窗；建立独立于树的开启者组边 |
| `SetFrameAllowlist(ctxID, allow)` | 修改嵌入属性；只对此后装载生效 |
| `Isolated(ctxID)` | 查询装载时冻结的隔离状态，O(1) |
| `Enabled(ctxID, feature)` | 沿父链求值能力，O(到顶层深度) |
| `CrossReference(fromID, toID)` | 跨引用：非同组返回 (false,nil)，断组返回 `ErrOpenerGroupBroken` |

## 求值规则摘要

- 隔离：顶层须 COOP=同源 且 COEP 严格；后代还须顶层隔离且自身 COEP 严格。
- 准入：直接父文档 COEP 严格时，跨源子文档必须同样 COEP 严格；同源不限。
- 能力（子框架三条件）：父可用 ∧ 父嵌入允许列表含子来源 ∧ 子声明未排除自身；
  未声明的嵌入列表按能力默认列表取值（仅自身时跨源不可用、同源可用）。
- 需隔离能力：任一祖先未隔离即不可用，即使权限策略允许。
- 组边：两方向独立；跨源且任一侧 COOP=同源 时相应方向断开；只断不恢复。

## 错误类别与拒绝次序

`ErrInvalidArgument` → `ErrContextNotExist` → `ErrDocumentNotExist` →
`ErrEmbedderMismatch` → `ErrOpenerGroupBroken` → `ErrFeatureUnknown`。
被拒操作不改变树、组关系或任何已推导状态。

## 测试覆盖矩阵

| 测试 | 覆盖点 |
| --- | --- |
| `TestTopLevelIsolationRequirements` | 顶层两条策略逐一缺失 |
| `TestNonIsolatedTopTaintsDescendants` | 后代头齐全但顶层不隔离；父导航替换子树 |
| `TestEmbedAdmission` | 同源/跨源准入差别；拒绝不改变状态 |
| `TestOpenerGrouping` | 三种开启者策略分组、单向断开、断开不恢复 |
| `TestOpenerNavigationResevers` | 开启者后续导航重新断组 |
| `TestFeatureConditionsAndDefaults` | 三条件逐一缺失；两种默认列表的同源/跨源差别 |
| `TestIsolationGatedFeature` | 需隔离能力的顶层/后代 × 隔离/未隔离四组合 |
| `TestNavigationAndAllowlistUpdate` | 导航后重推导而父链不变；允许列表只影响后续装载 |
| `TestRejectionOrder` | 六类错误的区分与拒绝次序 |
| `TestConcurrentSerializability` | 并发导航/查询/改列表下的线性化不变量 |
| `TestNaiveDifferential(Stress)` | 220 条随机操作序列与独立朴素重放模型对照 |
| `BenchmarkIsolated` / `BenchmarkEnabled` | O(1) 与 O(深度) 的可复现实测 |
| `ExampleKernel` | 端到端用法与日志内容 |

## 日志

每个装载、导航、开窗、查询操作都通过 `Logger` 打印输入参数、输出结果与判定依据
（如 `isolated(if required), ancestor-enabled, self-declared ...`）。
生产环境可传入 `nil`/`io.Discard` 关闭。

## 运行

```bash
go test ./...
go test -race -v ./bcontext
go test -run=Naive -v ./bcontext
go test -run=NONE -bench . -benchtime=10000x ./bcontext
```
