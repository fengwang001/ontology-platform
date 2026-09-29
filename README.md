# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 按可用区打散的副本调度器（`scheduler` 包）

`scheduler` 包实现"先预留、后绑定"的副本放置：同组副本逐个放到带可用区
（zone）标签与槽位容量的节点上，调度在互斥锁内原子完成，绑定异步执行且
可注入失败，失败即回退预留。

### 合格区与区计数

- 节点：`Node{ID, Zone, Slots, Labels}`；副本组：`GroupSpec{ID, Skew, Needs}`。
- **合格区**：该组的标签要求 `Needs`（键值全等）至少被区内一个节点满足的区，
  **不论该节点或该区是否还有空槽**。没有任何满足要求节点的区不参与任何计算
  （不参与最小值、不参与放置）。
- **区计数** `count[z]`：该组在区内**已绑定与已预留（含绑定中）副本数之和**。

### 偏斜判定与选点规则

对每次放置，设 `m = min(count[z])`，最小值只在合格区集合上取：

1. 区 `z` 可放置，当且仅当区内存在满足 `Needs` 且 `used < Slots` 的节点，
   并且 `count[z] + 1 - m <= S`。
2. 在所有可放置区中取 **区计数最小** 者；区计数并列按区标识升序。
3. 区内取 **剩余槽位（`Slots - used`）最多** 的节点；并列按节点标识升序。
4. 没有任何可放置区时返回 `ErrUnschedulable`。

注意：已满但计数最小的合格区本身不能放置，也不会抬高最小值——此时若放到
其他区会使计数差超过 `S`，则其他区同样被阻止；当 `S` 足够大时其他区仍可放置。

### 预留与绑定语义

- `Schedule` 成功即**预留**：原子地占用节点槽位并把区计数加一，状态为
  `reserved`。并发 `Schedule/Bind/Delete` 都可调用，调度判定与预留位于同一临界区，
  因此并发结果等价于某个串行顺序；任何节点占用不超过容量。
- `Bind` 通过 `BindFunc` 执行异步绑定（返回的 channel 在结束时得到结果）：
  - 绑定成功：状态变为 `bound`；
  - 绑定失败（注入的 `BindFunc` 返回错误）：**释放预留**，槽位与区计数一并回退，
    不留任何占用，状态变为 `released`；
  - 对 `released`/`bound`/`binding` 状态的预留再次绑定，分别返回
    `ErrReservationReleased`、`ErrReservationBound`、`ErrBindingInFlight`。
- `Delete` 删除副本：释放该副本自身占用的槽位并把区计数减一，**不搬运、不
  重新平衡**任何其他副本。
- 相同的节点、操作与故障序列重放，放置结果相同（选择规则全部为确定性比较）。

### 被整体拒绝的操作

下列情况返回可区分的哨兵错误（见 `scheduler/errors.go`），且**不改变任何计数
或槽位**：`ErrInvalidSkew`（S<1）、`ErrInvalidSlots`（槽位非正）、
`ErrDuplicateNode`（节点标识重复）、`ErrEmptyZone`（区标签为空）、
`ErrNoMatchingNode`（标签要求无节点满足）、`ErrUnknownGroup`、
`ErrDuplicateReplica`、`ErrUnknownReplica`（删除不存在的副本）、
`ErrUnschedulable`、`ErrReservationReleased`、`ErrReservationBound`。

### 日志

通过 `WithLogger` 注入实现 `Printf` 的日志器，日志包含每次操作的输入、
判定依据（合格区数、最小计数、满区跳过、偏斜比较）与输出/拒绝原因。

### 本地验证

```bash
go test ./scheduler -v
go test -race -count=50 ./...
go test -cover ./...
go vet ./...
```

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
