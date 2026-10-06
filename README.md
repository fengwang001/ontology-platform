# 机票改签差价与退票阶梯结算系统

核心实现位于 `booking` 包：

- `config.go`：阈值、费率、档位与向上取整手续费。
- `service.go`：时钟、并发锁、购票、退票、改签、航司取消。
- `quote.go`：退票/改签的统一校验与 O(1) 报价。
- `voucher.go`：代金券归属、到期、用尽与部分抵扣。
- `ticket.go`、`results.go`、`errors.go`：领域模型、结果和分类错误。

金额单位为分，时刻单位为秒，均使用非负 `int64`。

## 本地验证

如默认 Go 缓存目录只读，可设置：

```bash
export GOCACHE=/tmp/go-cache-ontology
```

```bash
go test ./... -count=1
go test -race ./...
go vet ./...
gofmt -l .
go test -run TestRandomOperationsAgainstNaiveModel -v ./booking
go test -run '^$' -bench 'Quote(Refund|Change)' ./booking
```

设计取舍与性能证明见 `DESIGN.md`。
