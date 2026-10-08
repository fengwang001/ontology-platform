# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `replay`：灾难恢复场景的差异记录重放校验组件。输入已知良好快照与
  结构/实例差异记录，按固定优先级判定三类互不相同的失败（差异记录
  自身不自洽、重放前提环境不满足、重放结果与声明目标不等价），
  全部通过则确证重放结果与声明目标等价。设计说明见
  `docs/replay-validation-design.md`。

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
