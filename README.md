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

## 事务半消息回查与提交日志（`txlog` 包）

`txlog` 实现事务半消息：发送后待定且对消费者不可见，提交时才获得日志位点，
到期由回查决定去向，可见顺序、回查次数与终态可精确复现。

### 状态迁移

```
            Send                 Commit / 回调返回提交
  (不存在) -------> 待定(Pending) ---------------------> 已提交(Committed)
                     |  \                                  （终态，进入日志）
                     |   \ Rollback / 回调返回回滚
                     |    ----------------------------> 已回滚(RolledBack)
                     |     第 M 次回查仍返回未知           （终态，原因可区分：
                     +------------------------------->   explicit / check_exhausted）
```

- 每个事务恰好进入一种终态；终态后事务标识仍保留，重复发送按 `ErrDuplicateTx` 拒绝。
- 四类拒绝两两可区分（`errors.Is`）：`ErrUnknownTx`（未知标识）、
  `ErrDuplicateTx`（重复发送）、`ErrAlreadyCommitted`（对已提交再提交/回滚）、
  `ErrAlreadyRolledBack`（对已回滚再提交/回滚）。被拒绝的操作不改变状态、日志与回查计数。

### 位点分配时点

- 日志位点从 0 起，连续无空洞，只含已提交消息。
- 发送半消息不占位点；**提交成功的瞬间**才追加到日志末尾获得位点，
  因此位点顺序是提交先后，而非发送先后。

### 回查时刻规则

- 时钟由调用方传入：`now` 小于此前任一次调用传入的 `now` 时整体拒绝（`ErrClockBackward`）。
- 待定消息从创建时刻起满 `F` 毫秒首次可被回查（`now >= createdAt + F`，恰在第 `F` 毫秒到点）。
- 之后每次回查时刻起满 `I` 毫秒再次到点：间隔从**实际回查调用的 `now`** 起算
  （`nextCheckAt = 实际回查 now + I`），不是从原定时刻起算。
- `Tick(now)` 的到点集合在推进开始时按创建先后确定；每条每次推进至多回查一次，
  哪怕一次推进跨越多个间隔；推进期间新发送的消息不在本次处理；
  轮到某条时若已进入终态则跳过且不计回查次数。
- 回调每被调用计一次；返回提交/回滚立即生效；返回未知继续等待，
  第 `M` 次仍未知则立即回滚，原因为回查耗尽（`check_exhausted`）。
- 回调在不持内部锁时调用，回调内允许调用 `Commit`/`Rollback`；
  回调返回时若该消息已进入终态，其返回值被忽略。

### 并发与可复现性

所有方法可并发调用，结果等价于某个串行顺序；相同的调用序列与回调返回序列
重放得到完全相同的日志与终态（`TestCompareWithNaiveModel` 与朴素逐步模拟对照验证）。

### 本地验证

```bash
# 全部用例（含提交序位点、恰第 F 毫秒首查、间隔从实际时刻起算、
# 跨间隔只查一次、第 M 次未知回滚、回调内先行终态、终态拒绝区分、
# 时钟倒退、并发等价性与朴素模拟对照）
go test -v ./txlog/

# 竞态检测
go test -race ./txlog/
```
