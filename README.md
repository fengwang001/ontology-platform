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

## 调度器待调度队列

调度器实现在 `scheduler` 包，维护四个互斥状态：活跃、退避、不可调度、在途。每个存活 Pod 在任一时刻恰好属于其中一个状态，`Sizes()` 返回四个状态的成员数。

### 队列流转

- `Add(id, prio, now)`：新 Pod 进入活跃队列，并固定保存首次入队时间 `t0`。
- `Pop(now)`：先执行 `Advance(now)`，再从活跃队列弹出优先级最高的 Pod；顺序为 `prio` 降序、`t0` 升序、ID 字节序升序。弹出后 Pod 进入在途，`att` 加 1，并记录当时的事件序号 `seq`。活跃队列为空时返回成功但无 Pod。
- `Done(id, Scheduled, fb, now)`：删除在途 Pod，`fb` 被忽略但仍做参数范围校验。
- `Done(id, Failed, fb, now)`：计算退避到期时刻；若本次 Pop 后发生过相关事件则进入退避队列，否则进入不可调度队列并记录 `parked=now`。
- `Event(ev, now)`：事件序号先加 1，再过滤不可调度队列。相关 Pod 在 `now < exp` 时进入退避队列，否则进入活跃队列；不相关 Pod 留在不可调度队列。
- `Advance(now)`：不增加事件序号。退避队列中 `exp <= now` 的 Pod 进入活跃队列；不可调度队列中 `parked+L <= now` 的 Pod 无论事件是否相关，都按 `now < exp` 决定进入退避或活跃队列。
- `Remove(id)`：可从活跃、退避、不可调度或在途状态直接删除 Pod。

### 退避公式

第 `att` 次尝试失败后：

```text
exp = now + min(B · 2^(att-1), M)
```

移位和乘法使用饱和上限处理，避免大 `att` 导致整数溢出；`exp <= now` 时 `Advance` 会把 Pod 移入活跃队列，因此 `exp == now` 立即可重试，`exp == now+1` 仍留在退避队列。

### 事件相关性与在途

事件掩码 `ev` 范围为 1 到 255，失败掩码 `fb` 范围为 0 到 255。Pod 与事件相关当且仅当：

```text
fb == 0 或 (fb & ev) != 0
```

在途 Pod 在 Pop 时只记录当时事件序号。事件序号大于该序号且与最终 `fb` 相关的事件，才会让 `Done(Failed)` 直接进入退避队列；Pop 之前发生的事件不计入该次在途。没有相关事件时 Pod 先进入不可调度队列，等待相关 `Event` 或滞留满 `L` 后再被搬移。

### 拒绝规则与并发

错误按以下优先级只返回第一个：参数非法、时钟回退、Add 的 ID 已存在、Done/Remove 的 Pod 不存在、Done 的 Pod 不在途。构造参数越界返回配置非法。被拒绝的操作不会改变队列、`att`、`fb`、事件序号或最大已接受时钟。

所有公开方法都由同一把互斥锁串行化，因此并发调用等价于某个合法的串行操作顺序。测试包含竞态检测、固定边界场景，以及 2000 组随机操作序列与按规则逐步编写的朴素模型对照；`-v` 输出会记录每步输入、输出和判定依据。

### 本地验证

```bash
/usr/local/go/bin/go test ./... -count=1
/usr/local/go/bin/go test -race ./... -count=1
/usr/local/go/bin/go test ./scheduler -run TestRandomAgainstNaiveModel -count=1 -v

# 若系统默认 Go 缓存目录只读，可指定临时缓存：
GOCACHE=/tmp/go-cache GOMODCACHE=/tmp/go-mod-cache /usr/local/go/bin/go test ./... -count=1
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
