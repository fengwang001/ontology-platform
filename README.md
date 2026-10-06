# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 电子印章用印控制

核心实现位于 `seal` 包，提供印章创建、停用/恢复、授权发放/收回、申请提交、分级审批、双人在场确认、执行、回执核销和保管人作废接口。关键语义、复杂度取舍与放弃方案见 [DESIGN.md](DESIGN.md)。

独立朴素模型位于 `seal/naive`，保留全部历史执行并线性扫描重算限额与冻结状态，用于固定种子随机差分测试。

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
go test ./... -count=1

# 随机操作序列对照（日志包含每步输入、输出和判定依据）
go test ./seal/naive -run TestRandomDifferential -count=1 -v

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
