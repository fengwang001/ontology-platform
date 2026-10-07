# 门诊药房处方调剂：库存预留与欠药补发系统

以整数分钟逻辑时钟驱动的药房库存引擎，支持整盒发药取整、部分发药与欠药
登记、预留超时级联释放、到货公平分配与处方有效期判定，全部行为可精确复现。

## 环境要求

- Go 1.26+（`go version` 确认）

## 快速开始

```bash
go run ./cmd/pharmacy-demo
```

```go
e, _ := pharmacy.NewEngine(pharmacy.Config{R: 10}) // 预留取药窗口 10 分钟
e.RegisterDrug(0, "阿莫西林", 4, false)            // 整盒 4，不可拆零
e.Inbound(0, "阿莫西林", 6)
e.AcceptPrescription(1, pharmacy.RxInput{
    ID: "rx1", Patient: "张三", IssueTime: 1,
    Lines: []pharmacy.LineInput{{DrugID: "阿莫西林", Qty: 5}},
})                                                 // 预留 4，欠药 1
e.Dispense(2, "rx1")                               // 取走有效预留，欠药保持
e.Inbound(3, "阿莫西林", 4)                        // 到货按欠药登记次序补发
rx, _ := e.QueryPrescription(4, "rx1")
drug, _ := e.QueryDrug(4, "阿莫西林")
```

## 测试与验证

```bash
go test ./...                # 单元 + 差分(默认1500组随机序列) + 性能证明
go test -race ./...          # 竞态检测
go test ./pharmacy/ -run XXX -bench . -benchtime 20000x  # 两档规模基准
gofmt -l . && go vet ./...
```

差分测试可用环境变量调节：

```bash
PHARMACY_FUZZ_SEQS=5000 PHARMACY_FUZZ_SEED=1 \
  PHARMACY_FUZZ_LOG=/tmp/fuzz.log go test ./pharmacy/ -run TestDifferentialFuzz
```

日志逐行打印每步输入、正式实现与朴素模型的输出及判定依据。

## 文档

- 设计说明（关键取舍、被放弃的方案、性能证明与验证方法）：[docs/design.md](docs/design.md)

## 代码结构

- `pharmacy/` — 引擎（类型、错误、事件推进、回滚日志、公开操作、欠药队列、事件堆）
- `pharmacy/naive/` — 独立朴素模型，仅用于差分测试
- `cmd/pharmacy-demo/` — 演示程序
