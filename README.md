# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 本体链接图分页遍历器

`ontology` 包实现了链接图上的分页邻域遍历：

- 从单个起点按链接方向扩展，`MaxDepth` 与 `MaxFanout` 两个上限独立生效；
- 三态截断标记（完整 / 因扇出截断 / 因深度截断），扇出原因优先；
- 续读标记锚定 MVCC 快照，图变更后续读不漂移、不报错；
- 权限过滤先于扇出计数，不可见对象不占名额；
- 内部访问度量仅随深度×扇出增长，与总图规模无关。

设计取舍、被放弃方案与验证方法见 [`docs/design.md`](docs/design.md)，
用法见 `ontology/example_test.go`。

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
