# ontology-platform

多仓库存可承诺量（ATP）查询与订单承诺系统：在现货、计划入库、带到期
时刻的预留与仓库优先序的共同作用下，对订单行选择发货仓，必要时在限定
范围内拆分，并精确区分永久缺货与暂时缺货。

设计说明（关键取舍、被放弃的方案、验证方法）见 [DESIGN.md](DESIGN.md)。

## 环境要求

- Go 1.26+（`go version` 确认）

## 模块

- `inventory/`：系统本体（类型契约、库存桶、系统外观、订单规划）
- `inventory/naive/`：独立编写的朴素参照模型，用于随机对照测试
- `cmd/inventorydemo/`：端到端演示程序

## 运行

```bash
# 演示程序
go run ./cmd/inventorydemo

# 编译
go build ./...
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（随机对照日志打印输入、输出与判定依据）
go test -race -v ./...

# 单个包 / 单个用例
go test ./inventory
go test -run TestDifferential ./inventory -v

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
