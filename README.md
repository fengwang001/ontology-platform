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

## 保单引擎（policy 包）

长期保单批改与退保现金价值引擎，位于 `policy/`：

- 保单账：生效日、年缴保费、基本保额、最低保额、犹豫期、工本费、现金价值比例表。
- 批改：保额变更 / 缴费期变更 / 受益人变更，预约—生效两阶段，支持撤销与待补缴。
- 缴费：按保单年度整年缴费，仅当前或下一年度。
- 退保：犹豫期内整单退保退实缴减工本费，犹豫期后退现金价值；部分退保按比例退现金价值并调减保费。
- 设计说明见 `policy/DESIGN.md`。

```bash
go test ./policy/ -race -v          # 全部测试（含朴素模型随机对照）
go test ./policy/ -bench . -benchmem # 性能基准
```
