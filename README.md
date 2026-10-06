# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

根包提供小区车位错时共享管理：`NewManager(spots, Config)` 创建服务，支持月租登记/续费、共享时段设置、月租归位与等候、临停准入、自动腾让与出场计费。所有操作接收单调整数分钟时钟，错误按“参数非法 → 时钟回退 → 对象不存在 → 状态不允许 → 无可用位 → 月租状态”的固定优先级返回。

主要方法为 `RegisterMonthly`、`RenewMonthly`、`SetShare`、`MonthlyEnter`、`VisitorEnter`、`Exit`；设计、复杂度证明和取舍见 `DESIGN.md`。

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
