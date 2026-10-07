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

## 子图快照校验器

`snapshot` 包提供子图快照记录序列的完整性校验与损坏定位：
逐条扫描对象/链接记录，在第一条损坏记录处截断，返回最大可恢复前缀、
损坏原因分类与是否完整。设计说明见 `docs/snapshot-validator.md`。

```go
res := snapshot.Validate(records)
// res.Complete / res.PrefixLen / res.Corruption / res.BadIndex
```
