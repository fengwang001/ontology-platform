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

`hedge` 提供带全局预算的对冲请求执行器 `Executor`：向有序副本列表发请求，
主请求迟迟不返回时发备份（对冲）请求，采用最先成功的结果并取消其余在途请求。

### 对冲与重试的区别

- **对冲（hedge）**：自最近一次发出起每满一个对冲延迟检查一次；仍无成功结果、
  在途数未满且全局预算允许时，发往下一个副本，计为一次对冲并消耗全局预算；
  任一条件不满足则跳过本次检查（下一周期再查）。对冲是"加发"，原请求仍在途。
- **重试（retry）**：在途请求失败时立即改投下一个副本，计为重试，**不占用对冲预算**，
  并自这次发出起重新开始对冲计时。重试是"替换"，失败请求已结束。
- 每个副本至多发一次；非幂等请求只发往首个副本，既不对冲也不重试。

### 计时与预算规则

- 时钟通过 `Clock` 接口注入：生产用 `RealClock`，测试用 `ManualClock`
  （`Advance`/`AdvanceTo` 同步按时间序、同刻按注册序触发定时器，保证可重放）。
- 对冲计时锚定在"最近一次发出"（含首次、对冲、重试），每满一个对冲延迟检查一次。
- 全局预算由 `NewBudget(fixed, ratio)` 配置，多次调用共享：任意时刻
  `已发出对冲总数 <= fixed + floor(ratio * 已接受调用总数)`。
  只有通过参数校验、被接受的调用才计入"已接受调用总数"。
- 参数非法（副本列表为空或有重复、对冲延迟或最大在途数不为正、截止时间已过）
  会在发出任何请求前整体拒绝，并返回可区分的哨兵错误
  （`ErrNoReplicas` / `ErrDuplicateReplica` / `ErrNonPositiveHedgeDelay` /
  `ErrNonPositiveMaxInFlight` / `ErrDeadlinePassed`）。

### 结局唯一性

每次调用恰好得到一个结局（`Outcome.Kind`）：

- `Success`：首个成功被采用，其余在途请求全部取消，迟到结果一律丢弃；
- `AllFailed`：全部已发出请求失败，`Errors` 按发出顺序给出各副本错误；
- `Timeout`：到达截止时间即取消全部在途；**恰在截止时刻到达的成功也视为超时**；
- `Rejected`：参数非法，未发出任何请求。

成功与取消、成功与截止同时发生时，状态机在锁内串行化结算，`done` 只关闭一次，
结局仍然唯一。相同副本应答脚本与时钟推进反复执行，发送序列与结局完全相同。

### 本地验证

```bash
# 全部用例（含慢主请求被备份胜出、快速失败立即改投、预算耗尽、非幂等、
# 迟到丢弃、成功与截止同刻、百并发预算核对、可重放性）
go test ./hedge/ -v

# 竞态检测
go test -race ./hedge/

# 单个场景
go test ./hedge/ -run TestConcurrentCallsShareBudget -v
```

测试日志会打印每次调用的输入、输出与判定依据
（如 `kind=hedge`、`reason=budget-exhausted`、`late response discarded`）。
