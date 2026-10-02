# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分层即时编译热度管理器

`jit` 包实现一个可确定重放的分层 JIT 热度管理器。所有公开操作都由互斥锁保护，并发调用等价于某个串行执行顺序。

### 状态与操作

- `NewManager(Config)` 校验方法数、阈值、反馈除数、队列容量、编译时长、衰减周期、去优化冷却参数和层 2 封禁阈值。
- `Call(m, n, now)` 严格按“校验 → 安装完成作业 → 衰减 → 按当前层返回并累计 i/b → 晋升判定/入队”的顺序执行。
- `Deopt(m, now)` 只允许安装后处于层 2 的方法执行；成功后方法回到层 0，i/b 清零，`dc++`，冷却截止时刻为 `now + C*dc`。
- `State(m, now)` 以“先虚拟安装所有 `finish <= now` 的作业，再衰减该方法”的视角返回结果，不修改真实队列、层级、纪元或全局时刻。
- 只有被接受的 `Call` 和 `Deopt` 推进全局最大时刻；参数非法、时钟回退或不可去优化的操作不产生任何副作用。

### 晋升与负载反馈

晋升判定只在方法低于层 2、没有在途编译作业且 `now >= cu` 时进行。当前队列长度为 `q` 时：

- 负载缩放：`s = 1 + floor(q/F)`。
- 去优化惩罚：`d = 1 + dc`。
- 层 2：`i >= A2*d*s`，或 `i >= M2*d*s` 且 `i+b >= B2*d*s`。
- 层 1：`i >= A1*s`，或 `i >= M1*s` 且 `i+b >= B1*s`。
- 层 0 同时满足 H1/H2 时优先跳级到层 2；层 1 只允许晋升到层 2。
- `dc >= Kd` 后封禁层 2，H2 恒不成立，但层 1 条件不受影响。
- 所有阈值乘积和 `i+b` 比较都通过 128 位无符号整数完成，避免 `int64` 溢出。

### 串行编译队列

队列是先进先出队列，容量为 `Qc`。晋升时若 `q >= Qc`，只增加丢弃计数，不入队；否则：

- `start = max(now, LF)`。
- 层 1 作业 `finish = start + D1`，层 2 作业 `finish = start + D2`。
- `LF = finish`，方法标记为在途；完成并安装前不会再次为同一方法入队。
- 每个方法同时至多一个在途作业，队列长度始终不超过 `Qc`，完成时刻单调不降。

### 计数衰减

每次操作或查询计算方法纪元 `epoch = floor(now/Pd)`。跨纪元数为：

```text
g = min(floor(now/Pd) - appliedEpoch, 62)
i = floor(i / 2^g)
b = floor(b / 2^g)
```

一次跨多个纪元只累计右移 `g` 位，而不是逐次重复衰减；右移位数封顶 62。`now` 恰为 `Pd` 的倍数时已属于新纪元。衰减发生在本次 `i++`、`b += n` 之前。

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

分层 JIT 的本地验证：

```bash
# 固定边界、拒绝语义、并发与 2000 组随机朴素对照
go test ./jit -v

# 竞态检测
go test -race ./jit

# 静态检查
go vet ./...
```

随机测试中的朴素模拟器每次全量扫描 FIFO 队列并逐位计算右移；它独立维护一份状态，与正式实现逐步比较层级、i/b、dc、纪元、冷却、作业开始/完成时刻、LF、队列长度和丢弃数。前两组随机序列使用 `t.Logf` 打印输入、输出与晋升/丢弃判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
