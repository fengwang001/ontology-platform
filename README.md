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

## 副本调度器（`scheduler` 包）

`scheduler` 包实现按可用区（zone）均匀打散的副本调度：同组副本逐个放置到带区标签的节点上，先预留槽位、再异步绑定，绑定失败自动释放预留。

### 数据模型

- `Node{ID, Zone, Slots, Labels}`：节点标识、区标签、槽位容量与标签集合。
- `Group{ID, Skew, RequiredLabels}`：副本组声明允许偏斜 `S`（必须 `>= 1`）与节点必须全部满足的标签键值对。
- 副本生命周期：`reserved`（已预留）→ `binding`（绑定中）→ `bound`（成功）；绑定失败转为 `released`。

### 合格区与偏斜计算

- **合格区**：至少有一个满足组必需标签的节点的区，与该节点是否还有空槽无关；无匹配节点的区完全不参与。
- **区计数**：该组在此区内处于 `reserved`、`binding`、`bound` 状态的副本数之和；`released` 不计数。
- 把副本放入区 `z` 当且仅当：
  1. `z` 内有满足标签要求且剩余槽位 `Slots - Used > 0` 的节点；
  2. 放置后 `count(z) + 1 - min(所有合格区当前计数) <= S`。
- 由于只有计数最小的区可能满足该不等式，调度器只在最小计数区中选择。

### 选点规则（确定性）

1. 在合格区中取计数最小、满足偏斜且有空槽节点的区；区并列按区标识升序。
2. 区内取剩余槽位最多的节点；剩余槽位并列按节点标识升序。
3. 没有任何合格区有空槽节点时返回 `no_node_available`；有空槽但放置都会破坏偏斜（例如计数最小的合格区已满）时返回 `skew_violated`。

### 预留与绑定语义

- `Schedule`：在同一临界区内完成判定、计数加一与槽位预留，随后即可并发调用其他操作。
- `Bind`：通过 `Config.Binder` 注入实际绑定逻辑（可注入故障）；绑定函数在全局锁外执行，槽位在结果落定前保持预留。
  - 成功：`reserved → bound`；
  - 失败：`reserved → released`，区计数与节点槽位同时回退，不留任何占用；
  - 对已 `bound` 或已 `released` 的预留再次绑定分别返回 `already_bound`、`already_released`；不存在的预留返回 `unknown_reservation`。
- `DeleteReplica`：删除副本并释放其计数/槽位，**不触发任何重新平衡**，其余副本位置永不移动；删除不存在的副本返回 `unknown_replica`。

### 拒绝原因码

所有拒绝都返回带 `Reason` 的错误（用 `scheduler.ErrReason(err)` 读取），被拒绝的操作不改变任何计数或槽位：

`invalid_skew`、`invalid_slots`、`duplicate_node`、`empty_zone`、`no_matching_node`、`no_node_available`、`skew_violated`、`unknown_group`、`duplicate_replica`、`unknown_replica`、`already_bound`、`already_released`、`unknown_reservation`。

### 并发与确定性

- `Schedule` / `Bind` / `DeleteReplica` 均可并发调用；同一预留的绑定/删除用独立互斥串行化，整体结果等价于某个串行顺序。
- 任何节点占用不超过容量；只含成功放置且绑定全成功时，合格区计数差恒不超过 `S`。
- 选点只依赖当前计数与标识排序，因此相同的节点、操作序列与故障序列重放，放置结果完全一致。
- 每次调度与绑定都通过 `slog` 打印输入、输出与判定依据（选中区、放置后区计数、最小值、`S`、节点、剩余槽位、必需标签）。

### 使用示例

```go
s, _ := scheduler.New(scheduler.Config{Binder: agent.Bind}, nodes)
s.AddGroup(scheduler.Group{ID: "orders", Skew: 1, RequiredLabels: map[string]string{"disk": "ssd"}})
p, err := s.Schedule("orders", "r1") // 立即预留
err = s.Bind("orders", "r1")        // 异步绑定；失败自动释放
```

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 重复执行（调度器支持并发调用）
go test -race -count=10 ./scheduler

# 覆盖率
go test -cover ./scheduler

# 可运行示例（含绑定失败后槽位复用）
go test -run ExampleNew -v ./scheduler
```
