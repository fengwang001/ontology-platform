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

## 子图快照提取器

`ontology/` 包实现本体链接图的子图快照提取（`Graph.Extract`）：按范围对象集合与
“两端均在范围内才纳入”的链接规则抽取确定时点（epoch）的自洽快照，附属信息区分
范围边界悬挂链接（`DanglingByScope`）与权限剔除悬挂链接
（`DanglingByPermission`，范围边界优先），并提供并发可串行化、确定性重复结果、
与朴素穷举模型的随机差分对照及内部开销度量。

- 设计说明（关键取舍、放弃方案、验证方法）：`docs/design.md`
- API 参考：`docs/api.md`

## 代码检查

```bash
gofmt -l .
go vet ./...
```
