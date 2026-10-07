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

## 派生属性索引一致性子系统（`derived`）

`derived/` 实现跨链接传递的派生属性索引：下游实例的索引键取自其经声明
链接连接到的源实例属性，支持多级传递、唯一性破坏即时不可索引、实例级
循环链接在生效前拒绝，以及源写入 / 链接增删 / 实例删除的单处理单元
原子更新。所有变更在 COW 快照上提交，查询具备严格串行一致性；另有
独立朴素重算模型做随机对拍。

- 设计说明：`docs/derived-index-design.md`
- 对拍与审计日志：`go test -run TestRandomDifferential -v ./derived/`
