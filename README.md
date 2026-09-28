# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

### `replication/` — 带前像校验的复制应用组件

把携带前后像的变更逐条应用到副本表：仅当前像与当前行**完全一致**时才应用，
否则分类为冲突并跳过；非法事件、序号不连续、副本行数超限则**整批拒绝且无痕**。
支持并发读取，读者只看到整批边界状态；同一输入序列输出确定。

详见 [`replication/README.md`](replication/README.md)。

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
