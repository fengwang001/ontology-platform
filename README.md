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

## 分层瀑布分账器（`waterfall` 包）

`waterfall` 包实现带追回（clawback）的分层瀑布分账器，分配、追回、查询均为
并发安全，可并发调用且结果等价于某一串行顺序。

### 配置

- 至少两层；前 `n-1` 层各有非负累计上限 `cap`（int64），最后一层是无上限的
  余额层，并带管理人提成比例 `g`（0–100 的整数百分比）。
- 层数小于 2、`cap` 为负、`g` 越界均整体拒绝（`ErrInvalidConfig`）。

### 分配（Allocate x，x 为正整数）

- 从第 0 层起依次填入 `min(剩余额, cap - recv)`，剩余额转入下一层。
- 余额层吞下全部剩余。
- 每次都从第 0 层重新开始填：追回后再分配时，先补前面空出的层，而不是接着
  上次停下的层。
- 分配后已分配总额（累计分配减累计追回）将超过 `1e15` 时拒绝
  （`ErrTotalExceedsLimit`，恰为 `1e15` 允许）。

### 追回（Clawback y，y 为正整数）

- 从最后一层（余额层）起逆序扣减，每层扣 `min(剩余追回额, recv)`，直到扣完。
- 追回额超过当前已分配总额时拒绝（`ErrClawbackExceedsReceived`）。
- 金额不为正先于"超过总额"判定，返回 `ErrNonPositiveAmount`。
- 任何被拒绝的操作都不改变各层已收额。

### 余额层拆分口径（关键）

拆分始终按余额层的**累计已收额 R** 整体重算，而不是把每笔增量的拆分结果累加：

- 管理人：`floor(R * g / 100)`
- 出资人：`R - 管理人所得`

每次分配或追回后都按当前累计 `R` 重算。例如 `g = 20`、余额层先后收到 4 与 4：
累计口径为 `floor(8*20/100) = 1`；若错误地按逐笔口径则会得到 0+0 = 0。追回使
`R` 从 8 降到 4 时，管理人所得从 1 降回 0。

### 不变量与确定性

- 任何时刻各层已收额之和等于累计分配减去累计追回。
- 非末层已收额不超过其 `cap`。
- 相同操作序列重放，得到完全相同的各层已收额与拆分结果。

### 本地验证

```bash
# 全量测试（含竞态检测；-v 可查看每步输入、输出与判定依据日志）
go test -race -v ./waterfall

# 多次重复以压并发
go test -race -count=10 ./waterfall

go vet ./...
gofmt -l .
```

测试覆盖：恰好填满某层、单笔跨多层、追回只扣末层、跨层逆序追回、追回后再
分配先补前层、累计口径与逐笔口径差异构造、追回使管理人所得下降、全部拒绝
原因与拒绝后状态不变、并发不变量，并与按上述规则逐步书写的朴素模拟器
（`naiveModel`）逐操作对照。
