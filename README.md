# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前交付：**供应商寄售库存的领用结算系统**（Go 包 `ontology`）。
货物存放在买方仓库但所有权归供应商，直到被领用才转移；系统按先到先出
分配批次，按领用时刻的有效价格结算，并处理寄售期限、在库上限、冲销与
对账单。设计细节见 [DESIGN.md](DESIGN.md)。

## 寄售系统 API

入口为 `ontology.NewSystem()`，所有方法并发安全（内部串行化，结果等价于
某个串行顺序）。时间为非负整数秒，操作时刻不得小于当前时钟，否则报
`clock_rollback`；被拒绝操作不改变任何状态与时钟。

```go
s := ontology.NewSystem()

s.SetLimit(t, supplier, product, limit)                 // 设置每供应商每商品在库上限
s.RegisterPrice(t, supplier, product, start, end, p)   // 价格协议 [start,end) 左闭右开
s.Receive(t, supplier, product, qty, duration)          // 到货，返回批次号
s.Return(t, supplier, product, batchNo, qty)            // 退回某批次
r, err := s.Consume(t, product, qty)                    // 跨供应商 FIFO 领用，返回结算行
s.Reverse(t, lineID, qty)                              // 冲销某结算行（原批次、原单价）
st, _ := s.Statement(supplier, start, end)              // 对账单（end 不得大于当前时钟）
total, expired := s.OnHand(supplier, product)           // 只读：在库量与其中已到期部分
reversed, _ := s.ReversedQty(lineID)                    // 只读：某结算行已冲销数量
```

错误类别可用 `ontology.IsError(err, ontology.KindXxx)` 区分：
`invalid_param`、`clock_rollback`、`not_found`、`no_valid_price`、
`insufficient_stock`、`over_cap`、`excess_quantity`、`period_open`、
`price_overlap`。拒绝优先级为 参数非法 → 时钟回退 → 对象不存在 →
无有效价格 → 库存不足 → 超上限 → 数量过量。

核心行为：

- FIFO 排序键：`(到货时刻, 供应商编号, 批次编号)`，跨供应商统一排序，
  跳过到期与剩余为零批次；总量不足整笔拒绝，不做部分领用。
- 到期是时刻纯函数：`到货时刻 + 寄售期限`，恰好满期即到期；到期批次占额度、
  不可领用，只能退回。
- 冲销按原结算行单价计金额，数量回原批次；冲销时刻立即到期的回库量占额度
  但不可再领用。
- 对账单左闭右开，恰在左端点归属本周期、恰在右端点归属下一周期。

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
