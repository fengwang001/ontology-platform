# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 死代码裁剪分析器（`dce/`）

模块打包器的死代码裁剪分析器位于 `dce/`：给定模块图、入口集合与副作用声明，
求被引入模块、必须保留的声明及原因（入口导出 / 被引用 / 副作用），并区分
缺失导出、歧义导出、重导出循环等错误。

- 设计说明（关键取舍、被放弃方案、复杂度论证、本地验证）：`docs/DCE_DESIGN.md`
- 端到端示例：`go run ./dce/cmd/example`
- 随机差分（独立朴素迭代模型，400 张随机图，含日志落盘）：
  `DCE_DIFF_LOG=/tmp/diff.log go test -run TestRandomDifferential -v ./dce/dce_test`

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
