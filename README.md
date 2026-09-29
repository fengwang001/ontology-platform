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

## 多重集差增量物化视图（`multisetdiff`）

包 `multisetdiff` 对左、右两侧的行变更流维护多重集差（multiset difference）
的增量物化视图，并为下游输出可顺序重放的结果变更日志。

### 多重集差语义

- 每一行以字符串行键标识，两侧分别维护该行的重数（非负整数，multiset multiplicity）。
- 结果重数：`result(row) = max(left(row) - right(row), 0)`。
- 右侧可以先于左侧到达：仅出现在右侧（或右侧重数 ≥ 左侧）的行结果重数为 0，
  不写入视图；左侧后续到达时重数逐份恢复。
- 重数归零的行立即从视图中删除，不作为 0 出现。

### 变更与变更日志规则

- 输入：`[]Change` 串行，每条变更指定 `Side`（`Left`/`Right`）、行键与整数增量；
  正数为插入、负数为删除。
- 提交是原子的：先在局部副本上校验并派生全部事件，全部合法后才一次性替换正式状态。
  任一条非法则整串拒绝——两侧重数、视图与已输出日志均不变（失败不留痕）。
- 每条输入变更只影响其所在行；结果重数发生变化时恰好输出一条 `Event{Row, Count}`
  （结果的净增量，可正可负），结果不变则不输出。因此每提交一条合法变更，
  日志至多新增一条事件。
- 下游按日志顺序应用事件（对结果重数做带符号累加）后，视图与对两侧当前重数做
  批量重算（`BatchRecompute`）逐行一致；任意日志前缀都满足这一性质，且各行重数非负。
- `Commit` 接受可选 `Logger`，逐条打印输入、输出事件或“无事件”及判定依据。

### 边界与错误类别

错误为互不相同的哨兵值，使用 `errors.Is` 区分；被拒时返回的 `*RejectError`
还通过 `Index` 指向串行中首个非法条目的下标：

| 错误 | 触发条件 |
| --- | --- |
| `ErrEmptySeries` | 提交的变更串行为空（`nil` 或长度 0） |
| `ErrZeroDelta` | 某条变更的增量为 0 |
| `ErrUnknownSide` | 某条变更的 `Side` 既不是 `Left` 也不是 `Right` |
| `ErrUnderflow` | 删除会使任一侧任一行重数变为负数（含串行中间状态） |
| `ErrTooManyRows` | 两侧合并的不同行键数量超过上限（默认 `MaxRows = 2^20`，测试用 `NewWithLimit` 调小） |
| `ErrDeltaOverflow` | 重数加减发生 `int64` 溢出 |

其它边界：恰好删除到 0 合法；删除不占用新行名额，归零后释放名额；
插入/删除按串行顺序逐条校验，串行内部同一行的中间下溢同样整体拒绝。

### 并发与自检

- `View` 内部以 `sync.RWMutex` 保护：多个执行体可并发提交（串行化），
  也可在提交进行时并发调用 `Snapshot`、`Multiplicity`、`BatchRecompute`、`SelfCheck`。
- `SelfCheck` 校验两侧与结果重数非负、行数未超限，并将“按变更日志增量维护的视图”
  与“从两侧重数批量重算的视图”逐行比对。

### 本地验证

```bash
# 全量测试（含竞态检测），-v 可查看每步输入/输出/判定依据日志
go test -race -v ./multisetdiff

# 覆盖率
go test -cover ./...

# 静态检查与格式
go vet ./...
gofmt -l .
```

覆盖场景：右侧先到、删除下溢（含串行中间状态）、变更最小化（一条变更至多一条事件、
截断区无事件）、各类非法输入的可区分错误、行数超限、整数溢出、拒绝后状态不变，
以及任意日志前缀与批量重算一致、多执行体并发提交与并发自检。
