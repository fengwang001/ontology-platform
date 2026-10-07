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

## 采购合同履约付款结算服务（settlement）

`settlement/` 包实现里程碑验收、预付款抵扣、质保金扣留与释放、
延期违约金扣抵与合同变更的结算服务。金额以整数分计，时间以整数日序号计，
比例以万分比整数给出；所有操作可并发调用（内部串行化），
相同操作序列重放得到完全相同的结果。

```bash
go test -race ./settlement/        # 单元测试 + 朴素模型随机对照 + 并发等价
go test -run TestNaiveModelDifferential -v ./settlement/  # 每步输入/输出/判定依据日志
```

设计说明（关键取舍、被放弃的方案、验证方法）见 `docs/design-settlement.md`。
