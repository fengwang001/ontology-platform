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

## 时间戳排序事务调度器（`scheduler` 包）

`scheduler/scheduler.go` 实现了基于时间戳排序（Timestamp Ordering）的并发
事务调度器，保证全部已提交事务的最终键值、以及每个已提交事务读到的值，
都与「按开始时间戳从小到大串行执行这些已提交事务」的参照完全一致
（被忽略的过时写视同被更晚的写覆盖）。

### 三个时间戳与比较方向

事务 `Begin` 时拿到从 1 起连续递增的时间戳 `TS`。每个键惰性建立，记录：

- `RTS`：读过该键的事务时间戳的最大值（初值 0）
- `WTS`：当前值安装者的时间戳（初值 0）
- `value`：当前值（初值 0）

所有比较都是**严格小于**（相等合法），检查按下列先后进行：

1. **读** `Read(TS, key)`
   - 私有写缓冲已有该键 → 返回缓冲值，**不触碰** `RTS/WTS/value` 任何记录；
   - 否则若 `TS < WTS` → 事务中止，原因「读过晚」
     （当前值由更晚的事务安装，读它会破坏串行序）；
   - 否则返回 `value`，并令 `RTS = max(RTS, TS)`。
2. **写** `Write(TS, key, v)` 只进入事务私有缓冲：
   - 若 `TS < RTS` → 立即中止，原因「写过晚」
     （已有更晚的读者看到了旧值，再覆盖就无法等价串行）；
   - 否则暂存，不立即安装，也不改任何键记录。
3. **提交** `Commit(TS)` 分两阶段，顺序不能调换：
   - **先按键名升序逐个复查**缓冲写：任一键上 `TS < RTS`（写之后又出现了
     更晚的读者）→ 整个事务中止「写过晚」，**零安装**并丢弃缓冲；
   - **复查全部通过后再逐个安装**：若 `TS < WTS`（提交期间有更晚的写已先
     安装），该写被**忽略**，键的值与 `WTS` 都不变；否则安装值并令
     `WTS = TS`（`RTS` 不动）。

### 为什么「忽略过时写」仍然正确（Thomas 写规则）

若安装时 `TS < WTS`，说明更晚时间戳的事务已经把该键安装为其值。在
时间戳升序串行参照中，本事务排在那位更晚写者之前：本事务的写随后必被
后者覆盖，对后续任何读者都不可见。因此跳过安装（值与 `WTS` 均不变）
与串行参照的最终状态完全等价，且事务仍正常提交。前提是复查阶段已确认
`TS >= RTS`——没有任何更晚的读者读过旧值，否则在第一阶段就会以
「写过晚」中止，不会走到忽略逻辑。

### 拒绝与中止的区别

- 操作参数/生命周期非法时被**整体拒绝**，返回哨兵错误且不改变任何状态，
  校验顺序为：键为空 → 事务不存在 → 事务已提交 → 事务已中止，只报第一个；
  `Begin` 时空 id 不适用（时间戳即事务标识）。
- **中止是合法结果**：返回 `*scheduler.AbortError`，事务转为已中止并丢弃
  私有缓冲；之后再对其操作才属于「事务已中止」的拒绝。

### 并发与确定性

所有方法在单把互斥锁内一次性完成「检查 + 修改 + 日志」，因此并发调用的
效果等价于这些调用按某个先后顺序串行执行；中止集合可能随线程调度不同而
变化，但每一次运行的已提交事务读值与最终键状态都满足串行参照。相同的
操作输入（测试中以固定随机种子重放）结果完全相同。

每个操作都通过日志记录输入、输出与判定依据（比较了哪个时间戳、
`RTS/WTS` 如何变化）。默认写入 `stderr`，可用 `NewWithLogger(w)` 重定向
（传 `io.Discard` 静默、传 `nil` 关闭）。

### 本地验证

```bash
# 全量测试（含随机交错对拍，日志打印输入/输出/判定依据）
go test -v ./scheduler

# 竞态检测下反复验证并发正确性
go test -race -count=10 ./scheduler

# 全部包 + 静态检查 + 格式
go test ./...
go vet ./...
gofmt -l .
```

测试覆盖：

- `TestReadTooLate`：读过晚中止及中止后状态；
- `TestWriteTooLate`：写时与提交复查两个时点的写过晚，复查失败零安装；
- `TestTSEqualsRTS`：时间戳恰等于 `RTS` 时写与提交均不中止；
- `TestIgnoredWrite`：`TS < WTS` 的写被忽略，值与写时间戳都不变；
- `TestRejectionOrderAndNoStateChange`：四类拒绝的优先级与拒绝不改状态；
- `TestReadOwnWrite`：读私有缓冲不触碰键记录；
- `TestRandomInterleavingMatchesSerialReference`：固定种子随机计划，
  并发交错执行后与时间戳升序串行参照逐项对拍，并校验重放确定性。
