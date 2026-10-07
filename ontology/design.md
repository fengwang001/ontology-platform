# 权限沿链接类型图传播与覆盖网关 — 设计说明

## 1. 要解决的问题

主体（subject）对某个对象类型的显式授权（允许/否定）可以沿**带方向**、且
声明了传播能力的链接类型向外扩散到另一端的对象类型。目标类型可以用覆盖
规则替换或阻断传播。链接图可能成环，必须在配置生效前整体拒绝。

代码入口：`ontology.Gateway`（`ontology/gateway.go`）。

## 2. 核心概念与精确定义

- **链接类型** `LinkType{Name, From, To, PropagationDepth}`：有向边。
  - `PropagationDepth == 0`：不参与传播，仅承载本类型直接授权；该边也**不
    参与**传播环路检测。
  - `PropagationDepth > 0`：参与传播。跳过时预算更新为
    `remaining' = min(remaining, depth) - 1`。
  - 越界（`< 0` 或 `> MaxPropagationDepth`）为配置错误
    `invalid_depth`。
- **剩余深度的边界语义**：token 以平台上限作为初始预算；每次跨越合法边后
  预算减一。**剩余恰好降到 0 的那一跳仍然生效**；从剩余 0 的节点再往外一
  跳不传播，这是“自然终止”（`depth_exhausted`），**不是错误**，并且必须与
  “传播环路”错误可区分。
- **覆盖（override）** 两种、必须可区分：
  - `ReplaceOverride`：在该目标处，**替换**（而非合并）所有上游传播并集，
    只保留该目标自身的直接授权；该目标自身的直接授权仍可作为**新来源**向
    下游继续传播。
  - `BlockOverride`：在替换的基础上**额外阻断**继续向下游传播——即使仍有剩
    余深度，且连该目标自身直接授权的继续传播也被切断（在本类型本地仍然有
    效）。
  - 覆盖只作用于“从上游到达的内容”，不作用于节点自身刚发出的 token。
  - 同一目标在一个原子变更批次内不得同时被声明为两种覆盖，否则
    `conflicting_override`，整批拒绝。
- **多路径并集**：不同起点或不同链接类型到达同一 `(目标, 主体, 动作)` 时，
  各路径独立计算后取并集；**否定项在并集中获胜**。若目标声明了覆盖，则覆
  盖结果**替换**整个传播并集，不再与其合并。
- **显式授权优先**：目标对象类型上的显式记录（含显式否定）始终优先于任何
  传播结果，与其覆盖范围大小无关。

### 拒绝原因 / 错误的优先级

配置性错误以 `GatewayError.Kind` 返回，彼此可区分；普通无权限不是错误，而
是 `Decision{Allowed:false, DenyReason}`。优先级从高到低：

1. 对象类型不存在 `object_type_not_found`
2. 传播配置含环路 `propagation_cycle`
3. 显式否定（授权记录中的否定项，且确实投递到目标）`explicit_deny`
4. 覆盖阻断导致的无权限 `override_blocked`（Block 强于 Replace）
5. Replace 替换导致的无权限 `override_replaced`
6. 深度自然耗尽 `depth_exhausted`（非错误）
7. 普通无授权 `no_grant`

## 3. 关键数据结构：结构索引（与主体无关）

传播规则只依赖图结构，不依赖“谁有什么授权”。因此把所有与主体无关的计算
从判定路径移到**配置提交时**，预算一次、判定多次复用。

`propIndex.inflow[target][origin]` 保存从来源对象类型 `origin` 到目标
`target` 的**所有结构路径结局的位集合**：

- `bitDeliver`：至少存在一条路径以剩余预算 `>= 0` 投递（携带最强剩余预算）。
- `bitBlocked`：存在路径在 Block 节点被阻断。
- `bitReplaced`：存在路径在 Replace 节点被替换。
- `bitDepth`：存在一次具体的“预算耗尽的最后一跳”落到该节点。

### 为什么是“位集合”而不是单一最强标记（关键取舍）

最早的实现对每个 `(target, origin)` 只保留一个标量结局。随机差分测试立刻
暴露反例：同一来源可能既有一条**直连投递边**，又有一条**绕行经过 Block 节
点的路径**。单标量要么错误地放行（用 deliver 压掉 block 信息），要么错误
地把直连边也阻断。结局位集合让“投递/阻断/替换/耗尽”在同一来源上并存，判
定时再按主体授权与优先级裁决。朴素模型里对应的坑是“在分叉处用可变状态把
覆盖标记带到兄弟分支”，改为按路径分支独立携带状态后消除。

### 深度位是终态

`bitDepth` 只记录在“预算耗尽的那一跳”的目的地，**绝不继续向下游传播**，
也不被该目的地的覆盖变换（因为没有任何 token 真正进入它）。这保证深度耗
尽不会伪装成阻断/替换，也不会无限结构性蔓延。

### Block / Replace 标记的传播

- 阻断/替换标记作为“纯原因标记”沿传播边结构性下传（不消耗预算、不授权），
  仅用于让后代把“为什么没有权限”报告准确。
- Block 强于 Replace：已经带 block 的路径再经过 Replace 不降级；Replace
  节点只把 `deliver` 位改写成 `replaced`。
- Block 节点自身的直接授权在本节点本地有效，但跨越出边时其自身 token 被转
  成 `blocked`（内部用 `blockSelfBit` 表示“本地投递、出边切断”，该内部位从
  不出现在可观察的 `inflow` 中）。

### 拓扑序动态规划

传播子图无环（见下节），所以按 Kahn 拓扑序处理节点：每个节点先接收所有上
游并入账的结局位，应用本节点覆盖变换，再向出边转发。`deliver` 跨越边时做
`min(remaining, link.depth)-1`。

## 4. 环路检测

`findPropagationCycle` 仅在“传播启用（depth>0）”的边上做 DFS 三色标记，命
中后向边即描述出环并返回 `propagation_cycle`。深度为 0 的边不承载传播，故
即使构成结构环（例如 A→B 传播、B→A depth=0）也不算传播环——这一点有专门
测试。

环路检查是**提交闸门**的一部分：候选快照先在副本上通过全部校验（深度、端
点存在性、覆盖引用、环路、同批覆盖冲突），全部通过才原子地替换当前快照；
任何失败都丢弃候选快照，已发布快照与所有判定结果保持不变。

## 5. 并发模型与线性一致性

- `Gateway` 内部一把 `sync.RWMutex` + 一个**不可变** `*state` 快照。
- 变更在写锁下：克隆当前快照 → 应用变更集 → 校验/重建索引 → 原子换入。失
  败则候选快照被丢弃，**图结构与判定结果都不变**。
- 判定在读锁下取快照指针后释放读锁，在不可变快照上计算，永不观察到半成品。

因此：

- 每次判定对应某个全局串行顺序下的确定前缀；
- 变更批次整体效果等价于单个串行步骤（all-or-nothing）；
- 被拒绝的授权 / 覆盖 / 链接调整不产生任何可观察影响。

`TestConcurrentRejectedBatchInert` 在持续读判定的同时反复提交“会成环而被拒
绝”的批次，验证读者永远只看到已提交前缀。

## 6. 判定算法

`Decide(subject, target, action)`：

1. 目标不存在 → `object_type_not_found`（最高优先级）。
2. 目标上存在该主体/动作的显式记录 → 直接以此为准（显式否定→`explicit_deny`，
   显式允许→allow），完全不看传播。
3. 否则取索引的**一行** `inflow[target]`，仅遍历该主体、该动作的授权记录：
   - 来源结局含 `deliver`：把该来源的显式效果计入并集（deny 胜 allow）。
   - 来源结局含 `blocked/replaced/depth`：记录对应“原因标记”。
4. 裁决：投递到的 deny > 投递到的 allow > blocked > replaced > depth >
   `no_grant`。

每次判定都通过 `Logger.LogDecision` 打印：输入（主体/对象/动作）、输出
（allow 或带原因的 deny）、依据（命中的路径 basis 或显式授权）。

## 7. 与规模无关的性能要求（含可验证证明）

判定时不做任何图遍历，读取的图信息只有一张**预算好的表的一行**
`inflow[target]`（O(1) map 取行），随后只遍历**该主体的匹配授权记录**。

记：

- `g_s` = 主体 `s` 拥有的、与查询动作匹配的授权记录数；
- `N` = 平台对象类型总数，`L` = 链接类型总数（可能远大于 `g_s`）。

则一次判定考察的“传播路径/条目”数量上界是 `O(g_s)`，**不随 `N` 或 `L`
线性增长**。代价被移到提交期：建索引为传播子图规模的
`O(V' + E')`（拓扑）加上结局位合并，适合“变更少、判定多”的负载。

可验证方式：

- 结构证明：代码中 `Decide` 无任何邻接表/链接遍历，只读 `inflow[target]`
  与主体授权（`ontology/gateway.go`）。
- 实验验证：`TestDecisionWorkIndependentOfGraphSize` 在 50/200/800 个对象
  类型与 O(N) 条链接下，断言判定考察的授权槽恒为主体自己的授权数（1）；
  `BenchmarkDecideDenseGraph` 在 500 节点长链 + 直达边上给出判定耗时（实测
  约百纳秒量级），与总图规模无关。

## 8. 被放弃 / 未采用的方案

- **判定时实时 DFS/BFS 传播**：实现直观、天然处理并集，但判定成本随 `N/L`
  增长，直接违反“与规模无关”的性能要求。降级为仅用于测试的**朴素参照模型**
  （`ontology/reference.go`），与生产索引相互独立。
- **每 `(target, origin)` 单一最强结局**：无法表达“直连放行 + 绕行被阻断”
  的并存情形，被结局位集合取代。
- **增量维护索引**：每次单条边增删打补丁。语义正确但实现复杂、易在覆盖/环
  边界出错；改为每次提交整体重建不可变索引（DAG DP），换取简单与可证明的
  一致性。提交期成本可接受时这是更稳的取舍。
- **把 depth-0 边纳入环检测**：会误报“合法的非传播回边”。严格按“只有传播
  启用边可成传播环”处理。
- **跨主体预算索引**：预算与主体无关，无需按主体复制；仅授权按主体过滤。

## 9. 错误类别（覆盖且可区分）

| 类别 | ErrorKind | 含义 |
| --- | --- | --- |
| 对象类型不存在 | `object_type_not_found` | 查询或配置引用了未注册类型 |
| 传播环路 | `propagation_cycle` | 传播边子图存在有向环，整批拒绝 |
| 深度非法 | `invalid_depth` | 负数或超过 `MaxPropagationDepth` |
| 覆盖冲突 | `conflicting_override` | 同批对同一目标声明两种互斥覆盖 |
| 普通无权限 | `Decision.Allowed=false` | 非错误，按 `DenyReason` 细分 |

## 10. 本地验证方法

需要 Go 1.26+。若 `go` 不在 PATH，使用 `/usr/local/go/bin`。

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache-ontology   # 仅当默认 GOCACHE 只读时需要

go test ./...                 # 全量测试
go test -race ./...           # 竞态检测
go test -v -run TestRandomDifferential ./ontology
go test -run TestDecisionWorkIndependentOfGraphSize ./ontology
go test -bench BenchmarkDecideDenseGraph -run '^$' ./ontology
go vet ./... && gofmt -l .
go run ./cmd/server            # 脚本化端到端演示（含日志输出）
```

## 11. 测试覆盖面

- 环路检测：传播环拒绝、depth-0 回边不算环、被拒批次不改图不改变判定
  （`cycle_test.go`）。
- 两种覆盖行为差异：Replace 替换并允许自身授权继续、Block 额外彻底切断
  （`override_test.go`）。
- 并集与覆盖替换边界：多路径 deny 胜 allow、替换不与并集合并
  （`decision_test.go`、`override_test.go`）。
- 剩余深度恰好耗尽的那一跳放行、再一跳自然终止；每条链接独立
  `min(remaining, depth)` 预算（`depth_test.go`）。
- 显式授权压过传播结果（含显式否定）（`decision_test.go`）。
- 与**独立朴素图遍历模型**对照：200 个随机 DAG（随机链接深度、覆盖、授权与
  否定项、动作/主体），逐 `(主体, 目标, 动作)` 比对允许与否**以及拒绝原因**
  （`diff_test.go`）。
- 并发原子性与规模无关性能（`scale_test.go`、`decision_test.go`）。
