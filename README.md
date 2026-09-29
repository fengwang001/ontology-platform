# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## PN-Counter（无冲突复制计数器）

`pncounter` 包实现了一个可增可减的状态型复制计数器（PN-Counter CvRDT），
多个副本可各自独立增减，通过两两合并同步，在任意合并顺序以及重复、乱序
到达下最终收敛到同一正确值。

### 数据模型与增减规则

- `Cluster` 在创建时固定副本数量 `n` 与每个分量的上限 `limit`
  （`NewCluster` 默认使用 `math.MaxUint64`，也可用 `NewClusterWithLimit` 指定）。
- 每个副本 `Replica` 维护两份长度为 `n` 的非负 `uint64` 向量：
  - `inc`：各副本的累计增；`dec`：各副本的累计减。
- 本地增减只修改**本副本自己编号**对应的分量：
  - `r.Increment(d)` → `inc[r.id] += d`
  - `r.Decrement(d)` → `dec[r.id] += d`
- 增量必须为正数（`uint64` 下非正即 0，被拒绝）；结果值允许为负，
  不会因为变负而拒绝或截断。

### 合并规则

- `dst.Merge(src)` 对每个分量 `i` 执行
  `dst.inc[i] = max(dst.inc[i], src.inc[i])`，`dec` 同理，且**只改目标副本**。
- 目标与来源是同一副本（`r.Merge(r)`）是合法空操作。
- 合并满足交换律、结合律、幂等律，因此重复合并、乱序合并、消息丢失后
  重放都不会破坏状态；全量传播后所有副本逐分量一致。
- 加锁按副本编号全局定序（小编号先锁），同一对副本互逆方向的合并
  同时进行也不会死锁。

### 求值、快照与自检

- `Value()` 返回 `*big.Int`：`Σinc[i] - Σdec[i]`，可能为负；
  用 `big.Int` 求和避免向量求和时溢出。
- `Snapshot()` 返回某一时刻 `inc/dec` 的防御性拷贝，外部修改不影响副本。
- `Replica.Check()` 与 `Cluster.Check()` 校验向量长度、编号归属与
  `0 <= 每个分量 <= limit` 等内部不变量。

### 边界与错误类别

所有非法输入都会被**整体拒绝，失败不留痕**（拒绝前后快照完全一致）。
六类原因各自是唯一的哨兵错误，可用 `errors.Is` 区分：

| 哨兵错误 | 触发场景 |
| --- | --- |
| `ErrInvalidReplicaCount` | 建集群时副本数 `n <= 0` |
| `ErrInvalidLimit` | 建集群时 `limit == 0` |
| `ErrNoSuchReplica` | 副本编号越界（负号或过大），或 `Merge(nil)` |
| `ErrNonPositiveDelta` | 增减量为 0 |
| `ErrComponentOverflow` | 操作会使对应分量超过 `limit`（含单次增量本身超上限） |
| `ErrInvariantViolation` | 自检发现内部不变量被破坏 |

注意：上限约束的是单个分量的取值，不是计数器的对外值；对外值仍可为负。

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

# PN-Counter：竞态检测 + 详细日志（日志含每步输入、各副本值与判定依据）
go test -race -v ./pncounter

# PN-Counter：多轮重复以防并发偶发
go test -race -count=10 ./pncounter

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
