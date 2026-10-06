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

## 取消责任判定与退款分摊（`cancel` 包）

`cancel/` 实现订单在各履约阶段被用户/商家/平台/骑手取消时的责任裁决、
争议窗口与退款四方拆分。入口为 `cancel.NewService(cancel.Config{...})`，
主要操作：`CreateOrder`、`Accept`、`Assign`、`Pickup`、`Deliver`、
`Cancel`、`Claim`、`Waive`、`ExpireDue`。

- 时刻为整数秒且全局单调，回退返回 `ErrClockRollback`；被拒绝操作零副作用。
- 错误均为 `*cancel.Error`，用 `cancel.Code(err)` 取 `ErrCode` 程序化区分。
- 每笔落地取消恒有
  `UserRefund + Merchant + Rider + Platform == 用户实付 + 优惠券面额`。
- 设计取舍见 [DESIGN.md](DESIGN.md)；随机序列与朴素模型差分见 `cancel/diff_test.go`。
