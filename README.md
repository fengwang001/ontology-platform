# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## Pod 放置过滤器（`placement` 包）

`placement.Filter` 是并发安全的 Pod 放置过滤器，支持必需亲和（含最少个数）、
必需反亲和、已有实例的对称排斥、预留可见性与标签变更。全部方法由同一把
互斥锁串行化，因此并发调用的结果等价于某个串行执行顺序；相同操作序列重放
得到完全相同的返回值、可行节点列表与拒绝原因。

构造：`NewFilter(Q)`，`Q` 为同时处于预留中的 Pod 数量上限（1..1000）。

### 数据模型

- 节点：`Node{Name, Zone}`，名称与可用区均为非空串。
- 拓扑键只有两种：`TopologyNode`（域 = 节点名）与 `TopologyZone`
  （域 = 节点所在可用区）。
- Pod：`Pod{ID, Labels, Affinity, AntiAffinity}`。ID 全局唯一；
  亲和项为 `(Selector, Topology, MinRequired m)`，`1 ≤ m ≤ 100`；
  反亲和项为 `(Selector, Topology)`。
- 选择器是键值对集合：Pod 标签包含其全部键值对才算匹配；空选择器匹配所有 Pod。
- “已放置 Pod”包括已提交与预留中两类，在所有判定中一视同仁。

### 放置判定

对候选节点 `n` 与新 Pod `x`（`x` 自身不计入个数），按顺序判定：

1. 必需亲和：对每条亲和项，已放置 Pod 中匹配选择器且按该拓扑键与 `n`
   同域的个数不少于 `m`。
   - 首个成员豁免（逐项独立判断）：若全集群已放置 Pod 中没有任何一个匹配
     该选择器，且 `x` 自身标签匹配该选择器，则该项视为满足。
   - 只要全集群存在任一匹配的已放置 Pod（哪怕数量不足 `m` 或在别的域），
     该项就不再豁免。
2. 必需反亲和：不存在匹配 `x` 任一反亲和项选择器且与 `n` 同域的已放置 Pod。
3. 对称排斥：对任一已放置 Pod `q` 的任一反亲和项，若 `x` 的标签匹配其选择器
   且 `q` 与 `n` 同域，则不可放置。亲和项不具有对称性。

`x` 的选择器匹配 `x` 自身标签不产生自我排斥。

示例：zone `a` 有 `n1`、`n2`，zone `b` 有 `n3`，已提交 `db1`、`db2`
（标签 `app=db`）分别在 `n1`、`n2`，则带亲和项 `(app=db, zone, 2)` 的
Pod 可行节点为 `[n1 n2]`；若只剩 `db1`，因全集群已有匹配 Pod 而不豁免，
可行节点为空。

### 操作语义

- `AddNode(Node)` / `RemoveNode(name)`：登记节点；仅当节点上没有任何
  已放置 Pod（含预留）时才允许删除。
- `Reserve(x, n)`：判定通过后记为 `n` 上的预留，且预留总数不超过 `Q`；
  预留立即参与所有判定。
- `Commit(id)`：把预留转为已提交，不再重新校验，并释放一个预留额度。
- `Cancel(id)`：删除预留。
- `Place(x, n)`：判定通过后直接记为已提交，不占用预留额度。
- `Remove(id)`：只删除已提交 Pod，不触发其他 Pod 的重新校验。
- `Relabel(id, labels)`：替换已放置 Pod（含预留）的标签。只检查一个方向：
  替换后不存在另一已放置 Pod `q`，其某条反亲和项匹配新标签且与该 Pod 同域；
  亲和项不重新校验。豁免状态随后续判定即时变化。
- `Feasible(x)`：不改变状态，返回所有可放置节点名（字节序升序）。

### 拒绝原因与顺序

错误为 `*PlacementError`，含 `Reason`、`TermIndex`（亲和/反亲和项下标，
从 0 开始）与 `BlockerID`（阻挡者中字节序最小的 Pod ID）。按顺序只报第一个：

- `Reserve` / `Place`：参数非法 → Pod 已存在（含预留同名者）→ 节点不存在 →
  亲和不满足（带第一条未满足项下标）→ 反亲和冲突（带第一条冲突项下标与
  最小阻挡 Pod ID）→ 被已有 Pod 排斥（带最小排斥者 ID）→
  预留已满（仅 `Reserve`，最后判）。
- `Feasible`：只可能因参数非法或 Pod 已存在被拒绝；其余原因逐节点判定，
  不满足的节点只是不出现在结果中。
- `Relabel`：参数非法 → Pod 不存在 → 标签变更被排斥（带最小排斥者 ID）。
- `Commit` / `Cancel`：Pod 不存在与 Pod 不在预留中（对已提交者）可区分。
- `Remove`：Pod 不存在与 Pod 仍在预留中可区分。
- `AddNode`：参数非法与重名可区分；`RemoveNode`：节点不存在与节点上仍有
  Pod 可区分。

被拒绝的操作不改变任何状态。

### 本地验证

```bash
# 全量测试（含 2000 组随机操作序列与朴素参考模型差分、并发同 ID 竞态）
go test -race -v ./placement

# 差分测试在临时目录输出输入/输出/判定依据日志，-v 时打印日志路径，例如：
#   differential log written to /tmp/placement-diff-*.log

# 快速跳过差分测试
go test -short ./placement

go vet ./...
gofmt -l .
```
