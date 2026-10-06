# ontology-platform

供应商寄售库存的领用结算系统：货物存放在买方仓库但所有权归供应商，
直到被领用才转移；系统按先到先出分配批次，按领用时刻的有效价格结算，
处理寄售期限、在库上限、冲销与对账单。

## 结构

- `consignment/` — 核心实现（时钟、价格簿、库存、台账、门面五个模块）
- `DESIGN.md` — 设计说明：关键取舍、被放弃的方案与本地验证方法

## 接口概览

```go
s := consignment.New()

// 写操作（携带时刻，不得小于上一次被接受操作的时刻）
s.SetCap(supplier, item, cap, t)                  // 设置在库量上限
s.AddPrice(supplier, item, from, to, price, t)    // 登记价格协议 [from, to)
batchID, err := s.Arrive(supplier, item, qty, period, t) // 到货
err = s.Return(batchID, qty, t)                   // 退回某批次
lines, err := s.Draw(item, qty, t)                // 领用，返回结算行
err = s.Reverse(lineID, qty, t)                   // 冲销某结算行

// 查询（只读，不推进时钟）
total, expired := s.OnHand(supplier, item)        // 在库量及其中已到期部分
reversed, err := s.ReversedQty(lineID)            // 某结算行已冲销数量
st, err := s.Statement(supplier, from, to)        // 对账单
now := s.Now()                                    // 当前时钟
```

错误均为哨兵错误，用 `errors.Is` 区分类别（参数非法、时钟回退、
对象不存在、无有效价格、库存不足、超上限、过量类、周期未结束等）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./consignment
go test -run TestFIFOTieBreak ./consignment

# 随机对照测试的详细日志（输入、输出与判定依据）
go test ./consignment -run TestRandomAgainstModel -v

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
