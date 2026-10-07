# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- **属性索引一致性子系统**（`ontology/` 包）：为对象实例的被索引属性维护
  辅助索引，保证属性写入与索引维护构成同一不可分割的处理单元，支持批量
  写入整体回滚、崩溃恢复判定、多索引结构同步、并发串行等价与 O(命中数)
  查询。设计与取舍详见 [DESIGN.md](DESIGN.md)。

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
