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
构造参数：出接口 MTU `E`、下限 `Lo`、严格降序台阶表 `P`（各项在 `[Lo, E]` 内）、
有效期 `X`、黑洞阈值 `K`。所有方法传入调用方时间戳，可并发调用，相同操作序列
重放结果完全相同。

### 报告处理

- **查询 `Query`**：返回条目路径 MTU；无条目（或已过期）返回 `E`。
- **需分片 `ReportFragmentationNeeded(dest, size, m, now)`**：`m == 0` 或
  `m >= size` 视为未报告，改取 `P` 中严格小于 `size` 的最大项（无则取 `Lo`）；
  新值为 `max(m, Lo)`。新值不小于当前路径 MTU 时忽略且**不刷新设定时刻**，
  否则采用并把设定时刻置为 `now`。
- **超时 `ReportTimeout(dest, size, now)`**：仅当 `size` 等于当前路径 MTU 时
  计数加一，其余包长忽略；计数达 `K` 时路径 MTU 降为 `P` 中严格小于当前值的
  最大项（无则取 `Lo`），计数清零，设定时刻置为 `now`。
- **成功 `ReportSuccess(dest, size, now)`**：仅当 `size` 等于当前路径 MTU 时
  清零连续超时计数。

### 降级与到期

- 有效期内同一条目只降不升，且任意时刻路径 MTU 都落在 `[Lo, E]` 内。
- `now >= 设定时刻 + X` 时整条失效（恰在到期时刻即失效），视为不存在，
  查询回到 `E`，之后的报告按新条目处理。
- 拒绝顺序：构造按 `Lo`、`E`、`P`、`X`、`K`；报告先查时钟回拨（`now` 早于
  条目设定时刻），再查空目的地、`size` 越出 `(0, E]`，需分片报告额外校验
  `m` 越出 `[0, E]`。被拒绝的操作不改变任何条目。

### 本地验证

```bash
# 全部测试（含竞态检测与判定依据日志）
go test -race -v ./pmtu/

# 格式与静态检查
gofmt -l .
go vet ./...
```
