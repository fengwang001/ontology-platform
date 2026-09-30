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

## 基于时间戳排序的事务调度器（`ontology` 包）

`ontology/scheduler.go` 实现了 Basic Timestamp Ordering 调度器。事务 `Begin`
时取得从 1 起连续递增的时间戳；每个键记录三个字段，初值分别为
`readTS=0`、`writeTS=0`、`value=0`。

### 三个时间戳比较的方向与先后

所有比较都是**严格小于**（时间戳相等不中止）：

1. 读（`Read`）：先查事务私有缓冲，命中则返回缓冲值，**不触碰键的任何记录**；
   未命中时比较 `ts < writeTS`，成立则中止，原因「读过晚」；否则返回当前值，
   并把读时间戳更新为 `readTS = max(readTS, ts)`。
2. 写（`Write`）：只写事务私有缓冲；先比较 `ts < readTS`，成立则事务**立即**
   中止，原因「写过晚」，缓冲不落盘；否则只更新私有缓冲，键记录不变。
3. 提交（`Commit`）：分两个阶段——
   - 复查阶段：**按键名升序**逐个检查缓冲写，任一键满足 `ts < readTS` 则整个
     事务以「写过晚」中止，**零安装**（复查期间不写任何值）；
   - 安装阶段：全部通过后按键名升序处理，`ts < writeTS` 的写被**忽略**
     （值与写时间戳都不变），否则安装值并置 `writeTS = ts`。

「写时检查 + 提交复查」缺一不可：写入缓冲后、提交前，可能有更晚的事务读过
该键并抬高了 `readTS`，复查负责拦住这种情况。

### 拒绝与中止的区别

- 拒绝（`RejectError`，操作整体拒绝、状态不变）只按以下顺序报第一个原因：
  键为空 → 事务不存在 → 事务已提交 → 事务已中止。提交操作不带键，从「事务
  不存在」开始检查。
- 中止（`AbortError`）是合法的事务结果：事务转为 `aborted` 并丢弃私有缓冲；
  之后该事务上的操作按拒绝处理。

### 为什么「忽略过时写」是正确的

参照执行是「把所有**已提交**事务按时间戳从小到大串行执行」。提交时若
`ts < writeTS`，说明已有一个时间戳更大的已提交写安装了该键；在串行参照中，
本事务排在那个写事务之前，它对该键的写必然被后来的写覆盖。因此直接忽略这笔
写，值与 `writeTS` 都不变，恰好等价于「被后者覆盖」，且保留了正确的安装者
时间戳。读侧同理：若 `ts < writeTS`，串行顺序中本事务的读发生在当前值的
安装写之前，不可能读到这个值，故以「读过晚」中止而不是返回脏数据。

并发方面，所有调度器操作在同一把互斥锁内完成，因此每个调用都是原子的，
整个历史可线性化；时间戳规则保证可恢复（允许忽略、不需要先写后装的两阶段），
最终键值与每个已提交事务读到的值都与时间戳升序串行参照一致。

### 本地验证

```bash
# 全量测试（含随机交错对拍与重放确定性测试）
go test ./ontology/ -v

# 竞态检测、重复执行
go test -race -count=3 ./ontology/

# 仅看定向边界用例
go test -run 'TestRejectOrder|TestReadLate|TestWriteLate|TestEqualTimestampNoAbort|TestIgnoredWrite|TestCommitRecheckZeroInstall|TestReadOwnBufferedWrite' -v ./ontology/

# 随机交错串行对拍 / 同时刻表重放确定性
go test -run 'TestRandomInterleavingAgainstSerialReference|TestReplayDeterminism' -v ./ontology/
```

测试在 `-v` 下通过 `slog` 文本日志打印每个操作的输入、输出与判定依据
（如 `ts < readTS => 写过晚`、`ts < writeTS => 过时写被忽略`）。覆盖点：

- 读过晚、写过晚中止；
- 事务时间戳恰等于读/写时间戳时不中止（严格小于）；
- 被忽略的写不改值也不改写时间戳；
- 提交复查失败时零安装；
- 拒绝优先级（空键 → 不存在 → 已提交 → 已中止）与拒绝不改状态；
- 读自身缓冲不触发读过晚、不触碰键记录；
- 60 轮随机并发交错与「时间戳升序串行」参照逐读值、逐终值对拍；
- 相同全局交错时刻表的多 goroutine 重放结果逐字节相同。
