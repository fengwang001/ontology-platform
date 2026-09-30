# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 刷新令牌轮换与重用检测（`refresh` 包）

`refresh.Detector` 实现刷新令牌自动轮换（rotation）与重用检测（reuse detection）。
每次登录创建一个**令牌家族**（family，即一次登录会话），续期时换发新令牌并作废旧令牌；
旧令牌在换出后再次出示时，区分「客户端重试」与「令牌盗用」。

### 令牌标识与家族

- 令牌标识由检测器按**全局签发顺序**生成：`t1`、`t2`……（跨所有家族连续编号）。
- `Login(now)` 创建家族并签发首个令牌；家族记录创建时刻与绝对到期时刻 `创建时刻 + D`。
- 家族任意时刻至多一个**可用令牌**（即家族当前令牌）；其余令牌均已换出。

### 续期与到期

- 出示家族**当前令牌**即续期：签发后继令牌，旧令牌变为「已换出」，记录其换出时刻与后继；
  新令牌成为家族当前令牌。
- 新令牌到期时刻 = `min(出示时刻 + L, 家族创建时刻 + D)`。
- 到期时刻**起**失效：`出示时刻 < 到期时刻` 才有效，恰在到期时刻即报到期。
- 家族达到绝对寿命（`出示时刻 >= 创建时刻 + D`）后，家族内所有令牌均不可再续期。
- **已换出令牌不检查自身到期**，只参与下面的重试/盗用判定。

### 重试与盗用

出示一张**已换出**令牌时，记 `elapsed = 出示时刻 - 该令牌换出时刻`：

- **重试**：`elapsed >= 0`、`elapsed < G`（严格小于宽限）**且**其后继仍是家族当前令牌。
  此时不签发新令牌、不刷新换出时刻，原样返回该后继令牌及其到期时刻，家族状态不变。
- **盗用**（满足任一即判定，立即吊销整个家族，含最新令牌）：
  - `elapsed >= G`：恰在宽限边界（`elapsed == G`）也算盗用；
  - 后继已被再次续期（后继不再是家族当前令牌）——即使仍在宽限内；
  - `elapsed < 0`（出示时刻早于换出时刻）。
- 吊销后家族内所有令牌的后续出示一律报「家族已吊销」。

> 宽限是基于「换出时刻」的时间窗。`G == 0` 时不存在重试窗口：同一时刻的第二次出示
> 已落在 `elapsed == 0 == G` 的边界，按盗用处理。需要容忍同刻并发重试时应配置 `G > 0`。

### 主动登出

- `Logout(t)` 按令牌 `t` 所在家族吊销整个家族；旧令牌（已换出）同样可以登出。
- 对已吊销家族重复登出视为成功且无变化。
- 令牌未知（无法定位家族）时报 `ErrUnknownToken`，不改变任何状态。

### 错误优先级

出示令牌被拒绝时，按下列**固定顺序**只报第一个命中的原因：

1. `ErrUnknownToken` 令牌未知
2. `ErrFamilyRevoked` 家族已吊销
3. `ErrFamilyExpired` 家族已达绝对寿命
4. `ErrTokenExpired` 当前令牌已到期
5. `ErrReuseDetected` 盗用（仅此原因会吊销家族）

除盗用会吊销家族外，任何被拒绝的操作都不改变检测器状态。

### 构造参数校验

`New(L, D, G)` 在创建检测器时整体拒绝非法配置，原因可区分：

- `L <= 0`：`ErrNonPositiveL`
- `D <= 0`：`ErrNonPositiveD`
- `G < 0`：`ErrNegativeG`
- `G >= L`：`ErrGraceTooLarge`
- `D < L`：`ErrDTooSmall`

### 并发与确定性

- `Login` / `Renew` / `Logout` 可被任意 goroutine 并发调用，内部以互斥保护全部状态。
- 并发出示同一当前令牌时，恰有一个调用完成换发，其余调用得到**同一个后继令牌**
   （在宽限内以重试形式返回，不产生额外签发）。
- 任意交错下，每个未吊销家族恰有一个可用令牌；盗用吊销后家族内所有令牌立即不可用。
- 令牌标识只取决于操作顺序而非并发时序，因此相同操作序列重放得到完全相同的标识与结果。

### 判定日志

通过 `WithLogger(io.Writer)` 配置日志输出（默认 stderr，传 `nil` 关闭）。
每次操作打印输入（令牌、出示时刻）、输出（后继令牌、到期时刻、是否签发）
与判定依据（`rotated` / `retry` 附带 `elapsed`、`grace`；`rejected` 附带 `reason`、`why`）。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 随机用例顺序 + 重复执行
go test -race -shuffle=on -count=3 ./refresh

# 覆盖率
go test -cover ./refresh

# 代码检查
gofmt -l .
go vet ./...
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
