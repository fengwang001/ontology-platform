# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## HTM 回退控制器

`htmfallback` 包实现尽力而为的硬件事务内存锁消除回退控制器。构造参数依次为：

- `N`：线程数，范围 `1...16`，线程编号 `0...N-1`。
- `S`：缓存组数，范围 `1...8`。
- `W`：每组路数，范围 `1...4`。
- `A`：缓存行地址数，范围 `1...64`，地址 `a` 所在组为 `a mod S`。
- `R`：冲突重试上限，范围 `0...8`。
- `SK`：容量中止后的跳过长度，范围 `0...8`。
- `F`：经容量或重试预算触发的回退连败阈值，范围 `1...8`。

任一构造参数越界，`New` 返回 `ErrInvalidConfig`，且不会创建部分状态对象。

### 状态与中止原因

- 线程状态：空闲、推测、回退、已中止。
- `ReasonConflict`：本次访问与其他推测线程的读集或写集相交；锁被占中止不计入该线程的冲突重试计数 `r`。
- `ReasonCapacity`：地址写入集合后，该线程读集与写集在同一缓存组内的不同地址数超过 `W`。
- `ReasonLockHeld`：某线程进入全局回退锁时，其余所有推测线程被中止。

返回结果包含调用线程的状态、旧中止原因（若本次因该原因走回退）、冲突中被中止线程的升序列表，以及调用线程的 `Retries`、全局 `Skip` 和 `ConsecutiveFallbacks`。`Snapshot` 可读取完整线程读写集、锁持有者和全局计数。

### 判定顺序

`Lock(t)` 只允许空闲或已中止线程调用，顺序固定为：

1. 全局锁持有者非空：返回等待，不改变任何状态，不消耗 `skip` 或 `r`。
2. `t` 已因容量中止：进入回退；计入连败 `fb`，但不扣减 `skip`。
3. `skip > 0`：先将 `skip` 减一，再进入回退；不计入 `fb`。
4. `t.r > R`：进入回退并计入 `fb`。注意 `t.r == R` 时仍可重新推测。
5. 否则清空读写集并进入推测。

进入回退时调用线程成为全局锁持有者；所有仍在推测的其他线程按线程号升序变为“已中止/锁被占”，并清空读写集。经容量或重试预算路径累计到第 `F` 次连败时，将 `skip` 置为 `SK`，并清零 `fb`。

`Access(t,a,write)` 只允许推测或回退线程调用，拒绝检查顺序为线程号、线程状态、地址：

1. 回退线程的访问没有任何影响。
2. 写访问与其他线程读集或写集相交时，中止其他线程；读访问只与其他线程写集相交时中止其他线程。
3. 被冲突中止的其他线程按线程号升序返回，其冲突计数 `r` 加一。
4. 冲突处理完成后才把地址记入调用线程的读集或写集；同地址读后写或写后读在容量统计中只算一个地址。
5. 记录后若本组不同地址数超过 `W`，调用线程容量中止，清空集合，并将 `skip` 置为 `SK`。

`Unlock(t)` 只允许推测或回退线程。推测提交会清空集合、转空闲、清零调用线程 `r` 并清零 `fb`；回退解锁会释放持有者、转空闲并清零调用线程 `r`，但保留 `fb`。

所有操作由同一个互斥锁保护，并发调用等价于某个合法串行顺序。相同调用序列重放会得到相同结果、中止原因、重试计数、跳过计数、连败计数和读写集。

### 本地验证

```bash
# 全量测试（包含 2000 组随机序列与朴素模拟器逐步差分）
GOCACHE=/tmp/go-cache go test ./... -count=1

# 竞态检测
GOCACHE=/tmp/go-cache go test -race ./htmfallback -count=1

# 查看随机差分的每步输入、输出快照和判定依据
GOCACHE=/tmp/go-cache go test ./htmfallback -run TestRandomDifferential -count=1 -v

go vet ./...
gofmt -w htmfallback/*.go
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
