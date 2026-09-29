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

## 分片上传会话（`upload` 包）

`upload.Registry` 提供分片上传会话的登记与完成校验，所有方法均可并发调用。

### 编号与大小规则

- 创建会话 `Create(id, total, maxSize)` 时声明总片数 `N=total` 与单片上限 `maxSize`，两者必须为正。
- 分片编号取 `1..N`；非末片大小必须等于上限，末片大小在 `1..上限` 之间。

### 覆盖记账

- 同号再传视为覆盖：字节数减旧加新，覆盖次数加一。
- 恒等式：`Overwrites == Uploads - Chunks`（覆盖次数 = 成功上传数 - 已登记的不同编号数），
  `Bytes == 当前各片大小之和`，在并发与失败下均成立。

### 完成与冻结

- `Complete(id)` 时若有缺片，按编号升序一次列全（`missing` 返回值与 `Error.Missing`）并拒绝；
  已登记分片保留，可补传后再次完成。
- 完成成功后会话冻结：后续上传与重复完成均按 `completed` 拒绝，`Stats` 查询仍如实返回统计。
- 与完成并发的上传，要么计入完成结果，要么以 `completed` 被拒。

### 错误优先级

多因同时成立时按固定优先级只报第一个：**不存在 > 已完成 > 越界 > 大小**
（创建参数非正报 `invalid_argument`，缺片报 `missing_chunks`）。
被拒绝的操作不改变任何统计。错误为 `*upload.Error`，可用 `Reason` 字段区分原因。

### 本地验证

```bash
# 竞态检测 + 详细日志（日志含输入、输出与判定依据）
go test -race -v ./upload

# 指定用例
go test -run TestConcurrentUploadComplete -v ./upload
```
