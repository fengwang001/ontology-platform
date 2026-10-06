# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 绿电证书登记簿（`greenreg`）

`greenreg/` 实现了按发电设施分期计量核发整数证书的登记簿：整批转让、
用电期注销声明、回溯计量修正引起的撤销、资格生效/终止，以及固定的拒绝次序。
设计取舍见 `greenreg/DESIGN.md`，包级文档见 `greenreg/types.go`。

关键入口：

- `greenreg.New(cfg)`：`UnitQty`（每证电量）、`MaxAgePeriods`（最大年限）。
- `RegisterFacility` / `SetTermination`：资格生效期与终止期（左闭右开）。
- `RegisterGeneration`：登记或修正某发电期电量，返回新发/被撤证书序号。
- `Transfer` / `RegisterUsage` / `Retire`：整批转让、用电登记、整批注销。
- `Events` / `Snapshot` / `SetLogger`：事件流、确定性快照、逐条判定日志。

对照模型 `NewNaive` 是独立的朴素全量重算实现，随机差分测试保证两者一致。

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
