# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分片上传会话（`upload` 包）

`upload.Registry` 提供分片上传会话的登记与完成校验，所有方法均可并发调用。

### 编号与大小规则

- 创建会话（`CreateSession`）时声明总片数 `N` 与单片大小上限，两者必须为正，否则报 `ErrInvalidSessionParams`。
- 分片编号取 `1..N`；非末片大小必须等于上限，末片大小在 `1..上限` 之间（`N=1` 时唯一分片即末片）。

### 覆盖记账

- 同号分片再次上传视为覆盖：总字节数减旧加新，覆盖次数加一。
- 任意时刻恒有：`覆盖次数 == 成功上传数 - 已登记的不同编号数`，`字节数 == 当前各片大小之和`。

### 完成与冻结

- `Complete` 时若存在缺片，按编号升序一次性列出全部缺片并拒绝（`ErrMissingParts`）；已登记分片保留，可补传后再次完成。
- 完成成功后会话冻结：后续上传与重复完成均报 `ErrSessionCompleted`，`Stats` 查询仍如实返回统计。
- 与完成并发的上传要么计入完成结果，要么以 `ErrSessionCompleted` 被拒。

### 错误优先级

多个拒绝原因同时成立时，按「不存在（`ErrSessionNotFound`）、已完成（`ErrSessionCompleted`）、越界（`ErrPartOutOfRange`）、大小（`ErrInvalidPartSize`）」的固定优先级只报第一个；被拒绝的操作不改变任何统计。所有原因均可用 `errors.Is` 区分。

### 本地验证

```bash
# 全量测试（含竞态检测与输入/输出/判定依据日志）
go test -race -v ./upload

# 仅并发交错用例
go test -race -run TestConcurrent ./upload
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
