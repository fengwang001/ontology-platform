# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `billing/`：物业费账单服务——账单生成与到期、宽限与滞纳金累计（封顶、分数结转）、
  部分缴款冲抵与预付、争议与裁定、滞纳金减免、三阶段催缴。设计取舍见 [DESIGN.md](DESIGN.md)。

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
