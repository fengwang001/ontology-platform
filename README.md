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

## 刷新令牌轮换与重用检测（`refresh` 包）

`refresh/refresh.go` 实现刷新令牌轮换（refresh token rotation）与重用检测，
保证每个登录家族任意时刻至多一个可用令牌，盗用一经判定整个家族立即失效。
时间由调用方以参数显式传入，逻辑完全确定，便于重放与测试。

### 数据模型

- **家族（family）**：一次 `Login` 创建一个家族（`f1`、`f2`……），记录创建时刻。
- **令牌（token）**：`Login` 签发首令牌；`Renew` 成功轮换时签发新令牌。
  令牌标识由检测器按全局签发顺序生成（`t1`、`t2`……），与家族无关、跨家族连续。
- **当前令牌**：家族内至多一个；旧令牌换出后记录换出时刻 `rotatedAt` 与后继
  `successor`。家族吊销后不存在当前令牌。

### 续期（Renew）语义

1. 出示**家族当前令牌**且未到期：旧令牌变为「已换出」（记下换出时刻与后继），
   签发新令牌，新令牌到期时刻为
   `min(出示时刻 + L, 家族创建时刻 + D)`；到期时刻起该令牌失效。
2. 出示**已换出令牌**，同时满足
   - 距换出时刻**严格不足**宽限 G（`at - rotatedAt < G`，恰在边界 `== G` 不算重试），
   - 其后继**仍是家族当前令牌**，

   则判定为**客户端重试**：不签发新令牌，原样返回该后继，家族状态不变。
3. 其他对已换出令牌的出示（超过宽限、或后继已被再次轮换）均判定为**盗用**：
   立即吊销整个家族，含最新令牌在内的所有令牌随即不可用。
4. 已换出的令牌**不检查自身到期**；到期检查只针对家族当前令牌。

### 登出（Logout）与吊销语义

- 出示家族内**任一**令牌（包括已换出的旧令牌）即吊销该家族。
- 对已吊销家族重复登出视为成功且不改变任何状态。
- 未知令牌登出返回 `ErrTokenUnknown`。

### 拒绝原因与固定优先级

`Renew` 按以下顺序只返回第一个命中的原因；除盗用会吊销家族外，
**任何被拒绝的操作都不改变状态**：

1. `ErrTokenUnknown`：令牌未知
2. `ErrFamilyRevoked`：家族已吊销
3. `ErrFamilyAbsoluteExpired`：家族已达绝对寿命（`at >= 创建时刻 + D`）
4. `ErrTokenExpired`：家族当前令牌已到期（`at >= expires`）
5. `ErrReuseDetected`：盗用（家族随之吊销）

### 构造期配置校验

`New(Config{L, D, G})` 在创建时整体拒绝非法配置，按顺序返回第一个可区分错误：

- `L <= 0` → `ErrNonPositiveL`
- `D <= 0` → `ErrNonPositiveD`
- `G < 0` → `ErrNegativeG`
- `G >= L` → `ErrGraceTooLarge`
- `D < L` → `ErrAbsoluteShorter`

合法边界：`G == 0`（任何已换出令牌的复用都不构成重试）、`D == L` 均允许。

### 并发与确定性

- 所有方法以单一互斥锁串行化状态变更，可被并发调用。
- 并发出示同一当前令牌时：恰有一个调用完成换出，其余调用落入宽限重试路径，
   得到**同一个后继**，全家族仅多一个新令牌。
- 相同操作序列（含相同时间参数）重放，得到完全相同的令牌标识、到期时刻与结果。

### 日志

`Config.Log` 指定日志输出（`nil` 丢弃）。每次调用打印一行，包含输入
（操作、令牌、出示时刻、家族）、输出（新令牌/后继/错误）与判定依据
（`login` / `current token renewed` / `reused within grace` /
`absolute lifetime reached` / `current token expired` /
`reuse ... outside retry window` / `active logout` / `already revoked, no change`）。

### 本地验证

```bash
# 运行 refresh 包全部测试（含并发与确定性用例）
go test -race -v ./refresh

# 重复执行以压测并发交错
go test -race -count=20 ./refresh

# 覆盖率
go test -coverprofile=coverage.out ./refresh
go tool cover -html=coverage.out
```
