# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 麻醉与精神类药品专用账册

本仓库当前交付物为 `ledger` 包（账册系统）与 `naive` 包（独立朴素
对照模型）。操作覆盖入库、双人复核领用、结清闭环、差额处理与销毁
见证；设计取舍、被放弃方案与验证方法见 [DESIGN.md](DESIGN.md)。

```bash
go test ./...          # 全量测试（含 1500 组随机序列差分对照）
go test -race ./...    # 竞态检测
go test ./ledger/ -run xxx -bench .   # 复杂度小/大规模对照基准
```

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
