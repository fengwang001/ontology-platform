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

## 事务批调度器（`scheduler` 包）

声明读写集的事务批调度器：按到达顺序接收事务，在不违背冲突先后的前提下
尽量并行地放行，使执行结果等价于按到达顺序的串行执行。

### 基本概念

- 创建时给定并发上限 `K`（正整数），`scheduler.New(k)`。
- 事务到达时声明读集与写集（字符串键的集合，二者可有交集），编号从 1 起
  按到达顺序连续分配。
- 事务状态：等待（Waiting）、运行（Running）、已完成（Completed）。

### 冲突定义

两个事务冲突，当且仅当其中一方的**写集**与另一方的**读集或写集**有交集
（写-读、写-写、读-写三种情形）。只读对只读不冲突，可以并行。

### 放行条件（取批）

每次到达、完成、取消之后立即取批一次：按编号升序扫描等待事务——

- 运行数已达 `K` 则停止扫描；
- 否则，当该事务与**所有编号更小且未完成**的事务（运行中的，以及仍在
  等待的、包括先前被跳过的）都不冲突时，放行为运行。

每个操作返回本次放行的编号序列（升序）。

### 不越过规则的理由

放行时不仅检查运行中的事务，还检查仍在等待的更早事务（包括被并发上限或
冲突挡下的），即任何事务都不得越过仍未完成的冲突事务。这样保证：

- 每个冲突事务对的放行先后都等于到达先后，因此所有冲突操作的生效顺序
  与到达序串行执行一致，执行结果与之等价（可串行化）；
- 运行中的事务两两不冲突，它们内部的并发执行互不干扰；
- 若允许越过等待中的冲突者，后到事务的写可能先生效，改变先达事务观察到
  的结果，破坏串行等价性。

### 取消与查询

- 完成（成功与失败都算）使事务转为已完成；取消只允许等待中的事务，
  取消后它不再阻挡他人。
- `Blockers(id)` 查询等待事务的阻塞者：与它冲突且编号更小、未完成的
  事务编号（升序）。
- 所有方法均可并发调用；相同操作序列重放结果完全相同。

### 错误与校验

被拒绝的操作不改变任何状态，且按固定顺序只报第一个错误：

- 到达：读写集皆空（`ErrEmptySets`）→ 含空串键（`ErrEmptyKey`）；
- 完成：编号不存在（`ErrNotFound`）→ 已完成（`ErrAlreadyCompleted`）→
  并非运行中（`ErrNotRunning`）；
- 取消：编号不存在（`ErrNotFound`）→ 已完成（`ErrAlreadyCompleted`）→
  正在运行（`ErrRunning`）。

### 本地验证

```bash
# 全部测试（含随机串行等价性与并发用例）
go test ./scheduler/

# 竞态检测
go test -race ./scheduler/

# 查看日志中的输入、输出与判定依据
go test -v ./scheduler/
```
