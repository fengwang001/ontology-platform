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

## 对冲请求执行器（`hedge` 包）

`hedge.Executor` 在请求迟迟不返回时向有序副本列表中的下一副本发备份请求，
采用最先成功的结果并取消其余在途请求，多次调用并发执行时共享全局对冲预算。

### 对冲与重试的区别

- **对冲（hedge）**：在已有请求仍在途时，因超过对冲延迟未获成功而**追加**发往下一副本，
  占用全局对冲预算，受最大在途数约束。
- **重试（retry）**：在途请求**失败**后立即改投下一副本，属于替换而非追加，
  不占对冲预算，但会重新开始对冲计时。
- 两者都遵循“每个副本至多发一次”；**非幂等请求只发往首个副本**，既不对冲也不重试。

### 计时与预算规则

- 先发往首个副本；自最近一次发出起每满一个对冲延迟检查一次：
  仍无成功结果、在途数未满且预算允许，则发往下一副本并计为一次对冲，否则跳过本次检查
  （下一周期锚定上次发出时刻继续检查）。
- 预算是全局共享的：任意时刻已发出对冲总数不超过
  `floor(baseCap + 已接受调用总数 * ratio)`（`NewExecutor(clock, baseCap, ratio)`）。
- 截止时间以注入时钟（`Clock` 接口）为准；到达截止时间取消全部在途请求并返回超时，
  **恰在截止时刻到达的成功也视为超时**。
- 参数在发出任何请求前整体校验：副本列表为空（`ErrNoReplicas`）、有重复
  （`ErrDuplicateReplica`）、对冲延迟不为正（`ErrNonPositiveHedgeDelay`）、
  最大在途数不为正（`ErrNonPositiveMaxInFlight`）、截止时间已过
  （`ErrDeadlinePassed`），原因彼此可区分（`errors.Is`）。

### 结局唯一性

每次调用恰好返回一个结局：

- `OutcomeSuccess`：首个成功被采用，其余在途请求被取消，迟到结果直接丢弃；
- `OutcomeAllFailed`：所有已发副本均失败，按**发出顺序**返回各副本错误；
- `OutcomeTimeout`：到达截止时间，取消全部在途请求。

成功与取消、成功与截止同时发生时，以到达时刻是否早于截止时间判定，
同刻一律按超时处理，结局仍然唯一。

### 本地验证

```bash
# 全部测试（含脚本化应答 + 手动时钟的确定性用例、100 并发预算核对）
go test -v ./hedge

# 竞态检测 + 重复执行（验证可重放性）
go test -race -count=2 ./hedge
```

测试日志会打印每个用例的输入、输出与判定依据。
