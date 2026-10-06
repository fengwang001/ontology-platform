# AML 现金存款结构化拆分检测

Go 实现的反洗钱现金存款报告系统，支持客户组关联、大额报告、结构化拆分检测、冲正、确定性重放和并发线性化。

## 环境要求

- Go 1.26+（`go version` 确认）

## 快速使用

```go
import "ontology/aml"

system, err := aml.NewSystem(aml.Config{L: 1000, H: 10000, K: 3, D: 7})
```

主要操作：

- `OpenAccount(now, accountID)`：创建独立客户组。
- `Deposit(now, accountID, txID, amount)`：接受存款，最多产生一份大额或结构化报告。
- `Link(now, accountIDA, accountIDB)`：关联两个客户组，可能触发合并组报告。
- `Reverse(now, txID)`：冲正存款，不撤回报告，也不触发评估。
- `GroupAccounts`、`WindowSummary`、`Reports`：只读查询组成员、窗口集合和全部报告。

## 测试

```bash
go test ./...
go test -race -v ./aml
go vet ./...
gofmt -l .
```

覆盖率：

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

关键不变量、复杂度证明、取舍和被放弃方案见 `docs/DESIGN.md`。
