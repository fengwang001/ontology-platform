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

## PN-Counter（无冲突复制计数器）

`counter` 包实现了一个可增可减、最终一致的状态型 CRDT（PN-Counter）。

### 数据模型

`New(n, componentLimit)` 创建 `n` 个互为副本的计数器。每个副本 `i` 维护两份长度为
`n` 的非负向量：

- `P[i]`：增量向量，第 `j` 个分量记录“来自副本 `j` 的增量”。
- `N[i]`：减量向量，第 `j` 个分量记录“来自副本 `j` 的减量”。
- 任一分量恒满足 `0 <= 分量 <= componentLimit`。

### 本地增减规则

- `Increment(i, d)` 只写副本 `i` 自己的分量：`P[i][i] += d`。
- `Decrement(i, d)` 只写副本 `i` 自己的分量：`N[i][i] += d`。
- `d` 必须为正整数（`uint64` 且 `> 0`）。
- 减量不会因为总值变负而被拒绝或截断。

### 合并规则

- `Merge(dst, src)` 对每个分量取两侧较大者：
  `dst.P[k] = max(dst.P[k], src.P[k])`，`N` 同理；**只修改 `dst`**，`src` 不变。
- 合并不受顺序影响，天然满足交换律、结合律、幂等律。
- `Merge(x, x)` 是合法空操作；消息重复、乱序到达均安全。
- 合并只取较大值，不会产生超过上限的分量。
- 双向合并（`Merge(a,b)` 与 `Merge(b,a)`）并发时，内部始终按副本编号升序
  加锁，锁序全局一致，不会循环等待、不会死锁。

### 求值

- `Value(i) = sum(P[i]) - sum(N[i])`，允许为负。
- 返回 `*big.Int`：多个 `uint64` 分量之和可能超出 `int64`。
- `Snapshot(i)` 返回副本状态的深拷贝；`SnapshotAll()` 返回全部副本。
- `SelfCheck()` 校验所有副本分量均在 `[0, componentLimit]` 内。

### 边界与错误类别

所有非法输入被**整体拒绝**，任一拒绝都不改变任何副本状态（失败不留痕）。
错误为互不相同、可用 `errors.Is` 区分的哨兵值：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidArgument` | `New` 时副本数 `<= 0` 或分量上限 `== 0` |
| `ErrUnknownReplica` | 副本编号越界（`< 0` 或 `>= n`） |
| `ErrNonPositiveDelta` | 增减量为 0 |
| `ErrComponentOverflow` | 操作会使自身分量超过 `componentLimit` |

### 并发模型

- 每个副本一把互斥锁；增减、合并、求值、快照、自检均可被多个 goroutine 并发调用。
- 合并需要同时锁住两个副本时，按副本编号升序获取；`SelfCheck` 按相同锁序
  锁定全部副本，避免与合并形成死锁。
- 上限判定在持锁后权威复查，消除“校验—加锁”之间的竞争。

### 本地验证

```bash
# 单元测试（含逐步输入、各副本值与判定依据日志）
go test -v ./counter/

# 竞态检测 + 并发/双向合并压力测试
go test -race -v ./counter/

# 全量测试与静态检查
go test ./...
go vet ./...
gofmt -l .
```

测试覆盖：四类非法输入及拒绝后状态不变、负值允许、合并的交换律/结合律/
幂等/空操作、乱序重复合并与朴素参照逐分量收敛核对、结果可复现，以及
增减与互逆方向合并等操作的并发交错（`-race`）。
