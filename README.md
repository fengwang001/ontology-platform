# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `subro`：保险代位追偿回收款分配与差额调整系统。按被保险人优先补足原则
  分配多次回收款，支持责任比例调整、补充赔付、放弃追偿，并在每次被接受
  的操作后以最小差额调整记录结清三方应得与已发放。设计取舍见
  [DESIGN.md](DESIGN.md)。
- `naivemodel`：独立朴素模型，仅用于测试中与 `subro` 对照。

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
