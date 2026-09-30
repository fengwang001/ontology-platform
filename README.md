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

## 路径 MTU 发现缓存（`pmtu` 包）

`pmtu.Cache` 按目的地记录路径 MTU，条目为（路径 MTU、设定时刻、连续超时计数）。
所有方法并发安全；任一时刻路径 MTU 落在 `[Lo, E]`，同一目的地在有效期内只降不升；
相同操作序列重放结果完全相同（时间由调用方以 `now` 参数显式传入）。

### 构造参数

- `E`：出接口 MTU；`Lo`：下限；`P`：台阶表（严格降序，各项在 `[Lo, E]` 内）。
- `X`：条目有效期；`K`：黑洞阈值（连续超时次数）。
- 构造拒绝按序只报第一个错误：`Lo` 非正 → `E < Lo` → `P` 非严格降序或有项越界 → `X` 非正 → `K` 非正。

### 报告处理

- **查询 `Query(dest, now)`**：返回条目路径 MTU；无条目或已到期返回 `E`。
- **需分片 `ReportFragmentation(dest, size, m, now)`**：`m == 0` 或 `m >= size` 视为未报告，
  未报告时取 `P` 中严格小于 `size` 的最大项（无则取 `Lo`）作为 `m`；新值为 `max(m, Lo)`；
  新值不小于当前路径 MTU 则忽略且不刷新设定时刻，否则采用并把设定时刻置为 `now`。
- **超时 `ReportTimeout(dest, size, now)`**：仅当 `size` 等于当前路径 MTU 时计数加一，
  其余包长忽略；计数达 `K` 时降为 `P` 中严格小于当前值的最大项（无则取 `Lo`），
  计数清零并把设定时刻置为 `now`。
- **成功 `ReportSuccess(dest, size, now)`**：仅当 `size` 等于当前路径 MTU 时计数清零。

### 降级与到期

- 新建条目路径 MTU 为 `E`、设定时刻为当前；降级只能沿台阶表向下，不会低于 `Lo`。
- `now >= 设定时刻 + X` 时整条失效视为不存在（恰在到期时刻即回升到 `E`），
  此后的报告按新条目重新建立。
- 报告拒绝（不改变任何条目）：时钟回拨、目的地为空、`size` 非正或大于 `E`；
  需分片报告额外校验 `m` 为负或大于 `E`。

### 本地验证

```bash
go test ./pmtu/           # 单元测试（日志打印输入、输出与判定依据）
go test -v ./pmtu/        # 查看逐条判定日志
go test -race ./pmtu/     # 并发竞态检测
go vet ./... && gofmt -l .
```
