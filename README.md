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

## 集群缩容执行器（`scaledown` 包）

`scaledown` 包实现集群缩容候选判定与**每轮至多移除一个节点**的执行器，
所有操作并发安全（内部互斥），相同操作序列重放结果完全一致。

### 数据模型与构造

- 节点：名字、CPU 可分配量 `ca`、内存可分配量 `ma`（均 1～10^12）。
- Pod：唯一 ID、所在节点、CPU 请求 `pc` 与内存请求 `pm`（0～10^12 且不同时为 0）、
  种类 `normal` / `daemon` / `pinned`。
- `New(P, T, MinNodes)`：`P` 为利用率阈值百分比（1～100），`T` 为持续时长毫秒（≥1），
  `MinNodes` 为节点数下限（0～10^6），越界返回 `ErrInvalidConfig`。
- `AddPod` 要求节点上全部 Pod（含 daemon）请求之和不超过可分配量，否则 `ErrCapacity`。

### 低利用与可移除判定（评估阶段）

`Tick(now)` 按节点名字节序遍历，节点 `v` 须**同时**满足：

- (a) 低利用：`v` 上**非 daemon** Pod 的请求和 `sc/sm` 满足
  `sc*100 < P*ca` 且 `sm*100 < P*ma`，严格小于，恰等于阈值不算低利用。
  daemon 不计入利用率，但仍占用节点容量。
- (b) `v` 在本轮此前没有接收过任何模拟迁入的 Pod。
- (c) `v` 上没有 pinned Pod。
- (d) `v` 上全部 normal Pod 可模拟迁出：按（CPU 降序、内存降序、ID 字节序）排序后
  逐个选目标；目标按节点名字节序取**第一个放得下**的，不得是 `v` 自己、
  也不得是本轮已判定可移除的节点。

### 模拟迁移与撤销

- 目标空闲量 = 可分配量 − 其上全部 Pod（含 daemon）请求和 − 本轮此前所有模拟迁入请求；
  CPU 与内存两维都需放得下。
- 任一 Pod 放不下则 `v` 不可移除，此次尝试产生的**临时落点全部撤销**，不影响后续节点。
- `v` 可移除则其落点**保留**，模拟迁入占用计入后续候选的目标容量与条件 (b)。

### 计时与单次移除

- 计时：可移除节点若无 `since` 则 `since=now`，已有则保持；不可移除节点清除 `since`。
  `AddNode`/`AddPod`/`RemovePod` 不改变 `since`，被拒绝的操作不改变任何状态。
- 执行：仅当节点总数 `> MinNodes` 时，在可移除且 `now-since >= T` 的节点中
  取 `since` 最小者（并列取名字节序小者）移除。
- 被移除节点的 daemon Pod 随之删除，normal Pod 按本轮记录的落点真实迁移；
  返回被移除节点名与 `(Pod ID, 目标节点)` 列表（按迁出排序），无移除返回空结果。
- `Tick(now)`：`now<0` 为 `ErrInvalidArg`；`now` 小于此前接受过的最大时间戳为
  `ErrClockRewind`（相等允许）。

### 拒绝原因（可区分的哨兵错误）

`ErrInvalidConfig`（构造越界）；`AddNode`：`ErrInvalidArg`、`ErrNodeExists`；
`AddPod` 按序只报第一个：`ErrInvalidArg`（ID 空/种类非法/请求越界或同为 0）、
`ErrPodExists`、`ErrNodeNotFound`、`ErrCapacity`；`RemovePod`：`ErrPodNotFound`；
`Tick`：`ErrInvalidArg`、`ErrClockRewind`。

### 本地验证

```bash
# 全量单测（含边界规则、拒绝顺序、2000 组随机差分与判定日志）
go test ./scaledown/ -v

# 竞态检测
go test -race ./...

# 仅看 2000 组生产实现 vs 朴素参照模拟的对照
go test ./scaledown/ -run TestRandomDifferential -v
```

`scaledown/diff_test.go` 用固定种子生成 2000 组随机场景（登记/增删 Pod/多轮 Tick/
非法操作），与 `scaledown/naive_test.go` 中按规则逐行写成的朴素模拟器逐轮对照
移除序列、迁移落点、since 与最终集群状态；`-v` 会打印输入、输出与每轮判定依据，
前 3 组额外打印完整操作轨迹。
