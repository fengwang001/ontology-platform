# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## groupcommit：批量写入的组提交器

`groupcommit` 包把多个并发写请求合并成批次一次性持久化，并把每条请求的
结果准确送回各自调用方。

### 组批与序号分配时机

- 同一时刻至多一个批次在持久化；待写请求按进入队列的顺序排队。
- 上一批结束后立即从队首起组新批：直到达到 `MaxItems` 条数上限，或再加
  一条将超过 `MaxBytes` 字节上限为止（单条恰等于上限可单独成批）。
- 序号在组批完成、交给持久化之前，按队列顺序从 1 起连续分配。

### 失败回收规则

- 持久化成功：整批全部成功，各调用方收到自己的序号。
- 持久化失败：整批以同一原因失败（不占用序号），已分配的序号被回收，
  下一批从回收处继续分配。因此已持久化的序号始终从 1 起连续、无空洞、
  无重号；某批失败不影响后续批次。
- 给定相同的入队顺序与相同的故障注入，批次划分与序号完全相同。

### 立即拒绝（不占用序号、不进入队列）

- 空负载：`ErrEmptyPayload`
- 单条超过字节上限：`ErrPayloadTooLarge`
- 关闭后提交：`ErrClosed`
- 创建参数非正或持久化函数为空：`ErrInvalidMaxItems` /
  `ErrInvalidMaxBytes` / `ErrNilPersister`

### 关闭语义

`Close` 不再接受新请求，并等待所有已入队请求全部得到结果后才返回；
重复调用是安全的。

### 本地验证

```bash
# 运行组提交器全部测试（日志含输入、输出与判定依据）
go test -v ./groupcommit

# 带竞态检测
go test -race -v ./groupcommit
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
