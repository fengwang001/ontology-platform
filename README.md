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

## DMA 描述符环主机侧驱动模型（`dmaring` 包）

`dmaring` 模拟一个带 OWN 位所有权交接的 DMA 描述符环：主机按分散聚集
（scatter-gather）方式整包提交，模拟设备按序处理，主机再整包回收。

### 环指针与 OWN 位规则

- 构造 `NewRing(N)`：`N` 必须为 2 的幂且不小于 2，否则返回
  `ErrInvalidSlotCount`。
- 三个指针 `prod`（下一个提交位）、`dev`（设备下一个处理位）、
  `reap`（主机下一个回收位）从 0 起只增不回绕，记为无界整数；
  槽号 = 指针对 `N` 取余。任意时刻 `reap <= dev <= prod`。
- 空闲数 `Free = N - (prod - reap)`，环可以被完全占满，不留空槽；
  空闲数只随 `Reap` 增加，设备已处理但未回收的槽仍占用。
- 每个槽含 `OWN`（1 归设备 / 0 归主机）、`FIRST`、`LAST`、`len`、
  `st`（0 新鲜 / 1 正常 / 2 出错首段 / 3 连带丢弃）。任意时刻
  `OWN=1` 的槽数恰等于 `prod - dev`。
- `Submit(lens)`：一个包占从 `prod` 起连续 `m=len(lens)` 个槽，首段
  `FIRST=1`、末段 `LAST=1`（`m=1` 时两者同为 1），各段 `OWN=1、st=0`，
  成功后 `prod += m`；整个包要么全部写入要么不写（原子性）。
- `DeviceRun(k)`：从 `dev` 起逐个处理，遇 `OWN=0` 即停，返回实际完成
  的单位数；正常描述符一个单位，处理后 `OWN=0、st=1、dev+=1`。
- 所有方法可并发调用，内部互斥保证结果等价于某个串行顺序；相同操作
  序列重放得到完全相同的槽内容、返回值与错误。

### 出错连带丢弃与整包回收

- `Fault(seq)` 登记无界序号 `seq`（须 `seq >= dev`，可 `>= prod`）的
  描述符在被设备处理时出错；重复登记无效。
- 设备处理到被登记的描述符时：该段 `st=2`，同包其后各段直到 `LAST`
  （含）全部置 `OWN=0、st=3`，`dev` 一并越过；整个出错连带只算
  `DeviceRun` 的一个单位。落在被连带丢弃段上的故障登记作废，该段仍
  为 `st=3`。
- `Reap()` 从 `reap` 起回收一个完整包：要求从 `FIRST` 到 `LAST` 所有
  段 `OWN=0`，返回段数、总长度与包内 `st=2` 段的下标（无则 -1），
  `reap += 段数`。
- 可区分的拒绝原因（被拒绝的操作不改变任何状态）：`ErrEmptyLens`、
  `ErrNonPositiveLen`、`ErrTooManySegments`、`ErrInsufficientFree`
  （Submit 按此顺序只报第一个）、`ErrNegativeK`、`ErrFaultBeforeDev`、
  `ErrNoPacket`、`ErrPacketIncomplete`（Reap 先判无包再判未完成）。

### 本地验证

```bash
# 全部单元测试 + 与朴素逐步模拟的随机对照（日志打印输入/输出/判定依据）
go test -v ./dmaring

# 并发等价性与数据竞态检测
go test -race ./dmaring
```
