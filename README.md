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

## 会话一致性令牌路由（`session` 包）

`session.Router` 把读写请求以会话为单位路由到足够新的只读副本，保证同一会话**读到自己的写入**且读到的进度**单调不降**。

### 核心规则

- **令牌（Token）**：每个会话持有一个单调不降的序号令牌，初始为 0。
  - 写入后：令牌取 `max(令牌, 新主库序号)`。
  - 读取后：令牌取 `max(令牌, 观察到的副本进度)`。
- **写入（Write）**：主库每次写入产生递增序号（1, 2, 3, …），并推进该会话令牌。
- **读路由（Read）**：只在已应用序号 **不低于令牌** 的副本中选择；
  选**进度最小**者，并列时按**副本名字典序**打破并列（保证确定性）；
  没有任何副本追上时**立即**返回 `replica_lagging` 错误，不阻塞、不改状态。
- **推进（Advance）**：副本已应用序号只允许单调前进，且不得超过主库已提交序号；
  回退（`seq_regression`）与超前（`seq_ahead`）都会被拒绝。

### 可区分的拒绝原因

所有拒绝以 `*session.Error` 返回，`Reason` 字段可区分原因，且被拒绝的操作**不改变**主库序号、副本进度或任何会话令牌：

| Reason | 含义 |
| --- | --- |
| `unknown_session` | 会话未注册 |
| `unknown_replica` | 副本未注册 |
| `replica_lagging` | 没有副本追上到会话令牌 |
| `seq_regression` | 推进序号小于副本当前进度 |
| `seq_ahead` | 推进序号超过主库已提交序号 |
| `empty_id` / `duplicate` | 空标识 / 重复注册 |

### 并发与确定性

- `Write` / `Read` / `Advance` 均可并发调用（内部互斥锁保护）；并发下每个会话读到的进度单调不减且不小于其此前最后一次写入序号。
- 路由决策只依赖状态与确定性比较（进度最小、名字字典序），同一输入序列反复计算得到完全相同的输出。
- 每次操作通过 `slog` 打印输入、所选副本与判定依据（`op`、`session`、`token`、`replica`、`progress`、`reason` 等字段），可用 `NewRouter(logger)` 注入自定义日志器。

### 本地验证

```bash
# 全部测试（含恰好追平路由、未追上失败、非法输入、并发单调性、确定性重放）
go test ./session

# 竞态检测 + 详细日志（可看到每次路由的输入与判定依据）
go test -race -v ./session

# 重复运行验证确定性
go test -count=5 -run TestDeterministicReplay ./session
```
