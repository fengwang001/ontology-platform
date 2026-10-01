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

## 会话生命周期管理（`session` 包）

`session.Manager` 以空闲期 `I`、绝对期 `A`、每用户并发上限 `N` 构造，
所有方法可并发调用，内部以互斥锁串行化；时钟由调用方显式传入，
相同的操作与时钟序列得到完全相同的状态与标识。

### 有效判定

- 会话有效当且仅当 `now < lastActive + I` 且 `now < createdAt + A`，恰到点即失效。
- 活动（`Activity`）只对有效会话把 `lastActive` 置为当前时刻，不延长绝对期。
- 创建（`Create`）返回按全局顺序生成的标识（`s1`、`s2`……）。

### 状态优先级

对非有效会话做活动或查询时，按以下优先级报告状态：

1. 已登出（`StatusLoggedOut`）
2. 被驱逐（`StatusEvicted`）
3. 绝对超时（`StatusExpiredAbsolute`，两种超时同刻成立时报此状态）
4. 空闲超时（`StatusExpiredIdle`）

### 驱逐规则

创建时先排除已失效会话（不占名额、不被误驱逐）；若该用户有效会话数已达 `N`，
驱逐其中最近活动最早者，并列取创建更早者，再并列取标识序号小者，
被驱逐者状态为被驱逐而非超时。任意串行化点上每用户有效会话数不超过 `N`。

### 本地验证

```bash
# 运行 session 包全部测试（日志打印输入、输出与判定依据）
go test -v ./session

# 带竞态检测验证并发场景
go test -race -v ./session
```
