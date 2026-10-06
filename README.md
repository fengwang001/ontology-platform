# 三单匹配与付款放行

Go 内存领域服务，实现采购订单、收货、发票三单匹配与付款放行。支持超收容差、价格容差、多次收货/多张发票、收货冲销、供应商冻结、折扣期、账期与逾期判定。

## 环境要求

- Go 1.26+（`go version` 确认）

## 模块

- `model.go`：领域类型。
- `errors.go`：可区分错误码。
- `validation.go`：参数校验。
- `store.go`：并发安全内存存储与单调时钟。
- `policy.go`：三单匹配、收货冲销、折扣和账期规则。
- `logger.go`：日志接口。
- `docs/design.md`：设计取舍、复杂度证明与被放弃方案。

## 快速示例

```bash
svc := matching.NewService()
_ = svc.RegisterSupplier(matching.Supplier{ID: "S1"}, 0)
_ = svc.CreatePurchaseOrder(matching.PurchaseOrder{
    ID: "O1", SupplierID: "S1",
    OverReceiptPermille: 100,
    PriceTolerancePermille: 100,
    PaymentPeriodSeconds: 30,
    DiscountPeriodSeconds: 10,
    DiscountPermille: 20,
    Lines: []matching.PurchaseOrderLine{{
        LineID: "L1", Product: "P1",
        Quantity: 10, UnitPriceCents: 1000,
    }},
}, 1)

_, _ = svc.RecordReceipt(matching.GoodsReceipt{
    OrderID: "O1", LineID: "L1", Quantity: 10, Time: 2,
})
invoice, _ := svc.SubmitInvoice(matching.Invoice{
    InvoiceID: "I1", SupplierID: "S1", OrderID: "O1", Time: 3,
    Lines: []matching.InvoiceLine{{LineID: "L1", Quantity: 10, UnitPriceCents: 1000}},
})
payment, _ := svc.PayInvoice(matching.Payment{InvoiceID: "I1", Time: 13})
```

放行结果为 `approved`，保留结果为 `held`；保留发票可调用 `ReevaluateInvoice(invoiceID, time)` 在补收货后重新判定。

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
go test -run '^$' -bench BenchmarkSubmitOneLineInvoice -benchtime=1000x ./...
```

当前沙箱验证使用：

```bash
GOCACHE=/tmp/go-build-cache GOPATH=/tmp/gopath /usr/local/go/bin/go test ./...
GOCACHE=/tmp/go-build-cache GOPATH=/tmp/gopath /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/go-build-cache GOPATH=/tmp/gopath /usr/local/go/bin/go vet ./...
```

测试包含固定种子的大规模随机朴素模型对照；发票判定复杂度为仅与发票行数相关的 `O(k)`，证明见 `docs/design.md`。
