# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前交付为 `ontology` 包：健康险等待期与既往症除外判定引擎，
设计见 [DESIGN.md](DESIGN.md)。核心类型：`NewEngine()` 返回引擎，
入口为 `AddCode`（维护编码目录）、`RegisterPolicy`（登记保单/续保）、
`SubmitClaim`（逐诊断理赔判定），错误为 `ErrCodeDuplicate`、
`ErrParentNotFound`、`ErrInvalidParam`、`ErrOverlap`、`ErrClaimExists`、
`ErrNotInsured`、`ErrInsuredNotFound`、`ErrCodeNotFound`。

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
