# 本体链接图三态可达性判定器 — 设计说明

## 1. 问题与结果定义

本体图上 `ReachableFrom(start, end, caller)` 必须区分两类权限并返回三个互斥结果：

| 结果 | 判定含义 | 分类理由（内部 `Reason`） |
| --- | --- | --- |
| `Reachable` | 存在至少一条调用者**可见且可遍历**的路径（含零长路径 `start==end`） | `certified_path` |
| `Unreachable` | 两端均可见，且**即使忽略一切权限，图中也根本不存在** start→end 路径 | `no_ground_truth_path` |
| `RestrictedUnknown` | 无法证明可达也无法证明不存在：端点不可见，或唯一候选路径被权限截断 | `invisible_endpoint` / `candidates_truncated` |

另有两类与权限无关的**错误**（不是三态之一）：`invalid_id`（标识格式非法）、
`missing_start`/`missing_end`（图中确实没有该标识）。

关键约束「不可见对象对调用者等同不存在」落在调用契约上：查询结果永远只有三态，
调用者无法通过任何返回区分「不存在」与「存在但不可见」。`missing_*` 错误只用于
服务端/特权管理面（事实层面的对象不存在，与调用者权限无关）；普通调用面可以把它
和 `RestrictedUnknown` 折叠为同一个外部响应。

## 2. 判定算法（`ontology/reachability.go`）

判定次序严格按需求固定：

1. **参数校验**：`start`/`end` 必须匹配 `^[A-Za-z0-9_.\-:]{1,128}$`，否则 `invalid_id`。
2. **事实存在性**：标识不在图中 → `missing_start`/`missing_end`（先查 start 后查 end，
   与调用者有无权限无关；不可见不能把「确实缺失」变成受限未知）。
3. **存在性权限**：调用者对 start 或 end 无 existence 权限 →
   `RestrictedUnknown/invisible_endpoint`。
4. `start == end`：零长路径，直接 `Reachable`。
5. **认证 BFS（certified search）**：只跨过满足「调用者持有该链接类型 traversal 权限
   **且**对边头对象持有 existence 权限」的边。到达 end 即返回 `Reachable`（这是证明，
   不是猜测）。
6. 认证 BFS 未命中时做**地面真值 BFS（shadow search）**：在同一份快照上忽略两类权限，
   从 start 求「全知视角」连通集。
   - 真值集不含 end → 被跳过的边无论授权与否都不可能通向 end（它们所在的整条候选链
     终局也到不了 end），返回 `Unreachable/no_ground_truth_path`。
   - 真值集含 end → 存在一条被权限截断的候选路径，且不存在完全可遍历且终点可见的
     路径，返回 `RestrictedUnknown/candidates_truncated`。

「截断 vs 穷尽」由此被**精确**区分：穷尽是对全图连通性的证明（负证明），截断只在
真值可达、权限不可达时出现。多级截断（边1、边2都没权限）也被正确覆盖，因为真值 BFS
不依赖任何单条边的授权状态。

方向性在写入链接时即物化：单向链接只在 `adj[src]` 放一条边，双向链接在 `adj[src]`
与 `adj[dst]` 各放一条，搜索过程不再判断方向，`(a,b)` 与 `(b,a)` 因此天然可能不对称。

## 3. 并发与串行等价性（`ontology/graph.go`）

- 全部状态装在不可变快照 `snap` 中（对象、链接、按方向展开的邻接表、每调用者的两类
  权限集合、单调递增的 `epoch`）。
- 变更走 copy-on-write：写锁内复制被触及的 map 头（邻接切片只复制被追加对象的那两个
  key），原子发布新快照；读完全无锁（只取一次指针）。
- **查询在入口恰好取一次快照**，之后只与该快照对话。其线性化点就是取快照的时刻：
  排在它之前被接受的授予/撤销可见，排在其后的不可见 —— 任意并发历史都等价于某个
  「查询与权限变更交错」的串行顺序。撤销一旦提交，之后线性化的查询不得再凭该权限
  判可达（测试 `TestSnapshotFixedDuringQuery` 用 tracer 在查询中途暂停并撤销权限，
  验证在途查询保持旧快照、撤销后新开的查询立即看不到权限）。

### 关键取舍

- **COW 快照而非单把长 RLock**：查询期间不持锁，写不被慢查询阻塞；代价是写放大
  O(变更触及的 map 大小)。本项目的图规模下完全可接受；若未来单次变更触及百万级条目，
  可把 map 换成持久化 trie/HAMT 或分片 COW，接口不变。
- **权限默认拒绝（缺席即 deny）**：撤销与「从未授予」语义一致，不需要墓碑标记。
- **traversal 按链接类型授权**（而非按链接实例）：与题述「沿某条链接类型遍历」一致；
  existence 按对象实例授权。
- **度量分两级记账**（见下），而不是试图让「判定截断」本身零代价：负证明本来就需要
  真值信息，强行避免会牺牲 `Unreachable` 的严格性。

### 被放弃的方案

- 「认证 BFS 遇到无权边就直接返回受限未知」：会把**通向无关分量**的边误判为截断，
  破坏 `Unreachable` 的严格语义，弃用。
- 「沿每条被截断的边做 DFS 试探能否到 end」：多级截断时要递归试探、可能重复爆炸，
  且难记账；一次真值 BFS 覆盖全部候选，简洁且可证明完备。
- 「查询持 RLock 直到结束」：实现简单但在途长查询会阻塞授予/撤销，可线性化但
  延迟特性差，且无法干净地做中途撤销的确定性时序测试，弃用。
- 「把 missing/invisible 合并返回以模拟调用者视角」：会让服务端无法表达题述要求的
  事实层判定次序。改为三态 + 两类错误，由外层 API 网关决定向调用者暴露什么。

## 4. 开销与内部度量

`Metrics` 只统计本次查询**实际尝试展开**的对象与链接，分两级：

- 认证级：`ObjectsDequeued`、`LinksInspected`、`LinksBlocked`（无 traversal）、
  `ObjectsInvisible`（边头无 existence）；
- 真值级（仅认证未命中时才发生）：`ShadowObjectsDequeued`、`ShadowLinksInspected`。

认证搜索永远只沿「有权 + 边头可见」的边出队，因此**调用者无权进入的区域连一个边头
都不会被枚举**：向图中追加大量挂在未授权链接类型之后、且对调用者不可见的对象时，
认证级计数保持不变（`TestMetricsDoNotGrowWithForbiddenRegions`：5 个与 5000 个禁区
对象两次查询的认证计数逐字段相等且恒为极小值）。真值 BFS 的代价以 start 在**忽略
权限后的连通分量**为界，与该分量之外的区域无关（`TestMetricsCertifiedFlatWhenUnreachableBehindBlock`
验证一个 2000 节点的无关分量一个都不会被计入）。

度量只通过内部接口 `ReachableFromTraced` 与可选 `Tracer` 暴露给测试/运维，不进入
面向调用者的 `ReachableFrom` 返回值，因此不会成为侧信道。

## 5. 测试与本地验证

包 `ontology` 下：

- `reachability_test.go`
  - 判定次序：非法标识 > 事实缺失 > 不可见；
  - existence 缺失与 traversal 缺失分别产生的不同结果（`invisible_endpoint` vs
    `candidates_truncated`）；
  - 截断/穷尽边界：唯一候选链 `s -a-> m -b-> t` 恰好只缺 b 的 traversal → 受限未知；
    指向 start 无关分量的目标 → 不可达；授权/撤销后结果来回翻转且无缓存；
  - 起点终点互换：单向链接正向可达、反向 `no_ground_truth_path`，双向链接对称；
  - 查询进行中撤销权限的时序边界（在途查询保持快照，之后的查询立即生效）；
  - 8 读 ×4 写 goroutine 并发压测（配合 `-race`）；
  - 两个度量稳定性测试（禁区扩大 1000×，认证计数不变）。
- `diff_test.go`：12 个确定性种子 × 每种子 300 步随机操作（加链接、授予/撤销两类
  权限、查询，2–15 个对象、1–4 种随机方向的链接类型、3 个调用者）。每次查询同时
  交给生产实现与一个**独立编写**的参照模型（`oracleModel`：从原始链接重建邻接、
  独立的权限 BFS + 真值 BFS；另有 `NaiveClassify`，`naive.go` 中独立的递归 DFS
  穷举，两者都不与 `reachableFrom` 共享搜索代码）。三态结果与理由必须一致；每次
  查询的输入、输出、理由、真值可达性、两端可见性、度量逐行写入
  `reachability_queries.jsonl`（路径由测试日志打印），并断言四类结果
  （可达 / 不可达 / 截断未知 / 端点不可见未知）在随机历史中都真实出现。

本地命令：

```bash
go test -race -v ./ontology
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
gofmt -l . && go vet ./...
```

## 6. API 摘要

- 建模：`AddObjectType`、`AddLinkType(name, Unidirectional|Bidirectional)`、
  `AddObject{ID,ObjectType}`、`AddLink{ID,LinkType,Src,Dst}`（未知类型/端点会报错）。
- 权限：`GrantExistence/RevokeExistence(caller, objectID)`、
  `GrantTraversal/RevokeTraversal(caller, linkType)`，默认拒绝。
- 查询：`ReachableFrom(ctx, start, end, caller) (Outcome, error)`；
  内部可验证版本额外返回 `Reason` 与 `Metrics`。
