# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子图快照提取器

`ontology` 包实现本体链接图的子图快照提取：按范围对象集合与“两端在范围内才纳入”
的链接规则抽取某一确定时点的自洽快照，权限剔除先于悬挂判定，边界悬挂链接进入
附属信息并区分 `boundary` / `permission` 两种来源（边界优先）。

- 设计说明（关键取舍、被放弃方案、本地验证）：`docs/DESIGN.md`
- 使用指南：`docs/USAGE.md`
- 朴素穷举参考模型与随机差分测试：`naive_test.go`、`differential_test.go`
- 每次随机提取的输入/输出/悬挂附属信息记录：`testdata/extraction_runs.jsonl`

核心 API：`NewGraph`、`(*Graph).AddObject/AddLink/GrantExistence`、
`NewExtractor(g).Extract(principal, scope)`、`Compare`、`(*Graph).Attribute`。

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
