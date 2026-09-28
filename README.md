# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 有界乱序事件时间重排缓冲（`watermark` 包）

`watermark` 包把乱序到达的事件按**事件时间**排序后从主输出交出，太晚到达的
事件立即走旁路单独交出。主输出严格有序，所有被接受事件不丢、不重。

### 核心概念

- **事件 `Event{ID, Time}`**：`ID` 为非空、非纯空白的唯一标识；`Time` 为非负事件时间。
- **到达序号 `Seq`**：每个被系统**接收**的事件（进入缓冲或旁路）分配一个从 1 开始
  的连续序号。被拒绝的事件不分配序号，因此序号不会因拒绝而跳号。
- **水位线（Watermark）**：初始为 0，随已接收事件的最大事件时间**单调不减**。
- **缓冲（pending）**：容量固定（`NewBuffer(capacity)`，必须为正数），存放已接收
  但尚未到期的事件。
- **两路输出**：主输出 `MainOutput`（到期释放，有序）与旁路 `SideOutput`（迟到）。

### 判定规则（`Offer`，按顺序检查）

1. 缓冲已关闭 → `CLOSED`；
2. `Time < 0` → `INVALID_PARAMETER`；
3. `ID` 为空或纯空白 → `EMPTY_ID`；
4. `ID` 此前已被接收（进过主路或旁路）→ `DUPLICATE_ID`；
5. `Time < Watermark`（**严格小于**）→ `LATE`：分配序号后**立即进入旁路**，
   不进缓冲、不占容量、不推进水位线；
6. 缓冲驻留数已达容量 → `BUFFER_FULL`（硬上限，不提前触发释放）；
7. 其余 → `ACCEPTED`：分配序号进入缓冲；若 `Time > Watermark`，水位线前进到
   `Time`，随即释放所有到期事件。

所有拒绝类别（2、3、4、6 及 1）都是**纯拒绝**：不改变水位线、序号、缓冲或
两路输出，并在结果 `Reason` 中给出可区分原因。

### 水位线、迟到与释放规则

- **迟到边界取严格小于**：事件时间**等于**水位线的事件仍属准时，保留在缓冲中。
  这样同一时间戳的一批事件可以一起按序号稳定排序输出，而不会被先后切成
  “准时/迟到”。
- **释放条件**：水位线推进到 `W` 后，缓冲中所有 `Time < W` 的事件立即释放；
  同一次释放按 `(事件时间, 到达序号)` 升序做**稳定排序**（相同时间，先到先出）。
- **尾批排空**：时间始终等于最高水位线的边界事件永远不会被“越过”，因此
  `Close()` 会把缓冲中剩余事件按同样顺序作为尾批排空，保证不丢。`Close`
  之后 `Offer` 一律得到 `CLOSED`，重复 `Close` 返回 `nil`。

### 并发与确定性

- 所有方法均并发安全（内部互斥锁线性化），并发提交时每个被接受事件恰好获得
  一个序号、恰好出现在主输出或旁路之一中，主输出始终严格有序。
- 输出仅取决于输入序列本身：同一序列反复计算，水位线、主输出、旁路完全一致。

### 日志

`NewLogger(buf, io.Writer)` 包装缓冲，为每次操作打印：输入事件、判定结果与
**判定依据**（basis）、当次释放事件，以及主/旁路两路输出快照。

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

### 本地验证重排缓冲

```bash
# 只跑 watermark 包，带竞态检测、详细日志（含每次输入、判定依据与两路输出）
go test -race -v ./watermark

# 只看某类场景（稳定排序 / 迟到旁路 / 非法输入 / 并发不变量 / 确定性）
go test -race -run 'TestStableOrderSameTimestamp|TestLateRoutingSideOutput' ./watermark
go test -race -run TestInvalidRejections ./watermark
go test -race -run TestConcurrent ./watermark
go test -run TestDeterminism ./watermark

# 覆盖率
go test -coverprofile=coverage.out ./watermark
go tool cover -html=coverage.out

# 运行可执行演示，直接观察输入、判定依据与主/旁路输出
go run ./cmd/watermark-demo
```

覆盖的测试场景包括：相同时间戳按到达序号稳定排序、迟到立即走旁路
（且等于水位线不算迟到）、非法时间 / 空标识 / 重复标识 / 缓冲超限 / 关闭后
提交等各类拒绝、拒绝不改变任何状态、整体不丢不重、水位线单调、同序列反复
计算结果一致，以及并发下恰好一次与主输出严格有序。


## 代码检查

```bash
gofmt -l .
go vet ./...
```
