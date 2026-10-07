# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `ontology/`：本体链接图与 `HasCycle` 环检测器——仅在调用者当前存在性/遍历
  权限过滤后的可见子图上判定环（自环、双向往返环计入），结果与证据与遍历
  起始对象、访问顺序、创建历史无关，并发下满足快照一致语义。设计与取舍见
  `ontology/DESIGN.md`，随机对照的逐次调用日志见
  `ontology/testdata/has_cycle_calls.csv`。

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
