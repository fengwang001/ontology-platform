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

## 采购合同履约付款结算服务（`ontology/settlement`）

管理里程碑验收、预付款抵扣、质保金扣留与释放、延期违约金扣抵与合同变更。
金额以整数分计，时间以整数日序号计，比例以万分比整数计。

```go
svc := settlement.NewService()
err := svc.CreateContract(settlement.ContractParams{ /* ... */ }, now)
res, err := svc.Accept("c1", "m1", true, now)        // 验收通过即结算
rel, err := svc.ReleaseRetention("c1", "m1", now)    // 质保金释放
err  = svc.RegisterDefect("c1", "m1", "d1", 500, now)
err  = svc.CloseDefect("c1", "m1", "d1", now)
err  = svc.ChangeOrder("c1", effDay, adjs, now)      // 合同变更
err  = svc.Terminate("c1", now)                      // 终止合同
sum, err := svc.Summary("c1")                        // 守恒汇总（O(1)）
```

- 设计说明（关键取舍、被放弃的方案、本地验证方法）：[docs/settlement-design.md](docs/settlement-design.md)
- 测试：`go test ./settlement/ -v`（边界用例 + 朴素模型对照随机序列 + 并发等价）
- 基准：`go test ./settlement/ -run XXX -bench .`（验证结算与汇总为 O(1)）
