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

## 边界检查消除子系统（`bce` 包）

输入带控制流的中间代码（`bce.Program`），每个下标访问附带一次边界检查；
`bce.Analyze` 依据四类可证明事实（常量下标、常量数组长度、路径比较条件、
已通过检查）判定移除/保留，并为每个保留检查给出确定性的无法证明原因与事实来源。
设计与验证方法见 [bce/DESIGN.md](bce/DESIGN.md)，测试见 `bce/bce_test.go`。
