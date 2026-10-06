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

## 医疗费用理赔分摊结算引擎

位于 `ontology/` 包，职责拆分：

- `types.go` / `errors.go`：数据结构与六类可区分错误（固定拒绝次序）。
- `settlement.go`：单笔结算纯函数，规则次序为 剔除不赔 → 每次事故免赔（逐条消耗）
  → 类别比例赔付（向上取整）→ 年度自付封顶截断（追加赔付归属最后一条被截断明细）。
- `yearbook.go`：保单年度左闭右开定位与年度累计（仅两个标量，结算 O(明细数)）。
- `engine.go`：`RegisterPolicy` / `Submit` / `Cancel` / `YearSnapshot`，互斥锁保证并发串行等价。

快速使用：

```go
e := ontology.NewEngine()
_ = e.RegisterPolicy("p1", ontology.PolicySpec{
    InceptDay: 0, YearLen: 365,
    PerClaimDeductible: 100, AnnualDeductCap: 1000,
    InpatientRate: 80, OutpatientRate: 50, OOPCap: 500,
    ExcludedCodes: map[string]bool{"EX": true},
})
r, err := e.Submit("p1", ontology.Claim{ID: "c1", AccDay: 10, Lines: []ontology.Line{
    {Code: "A", Category: ontology.CatInpatient, Amount: 200},
}})
// r.InsurerPay / r.SelfPay / r.Lines[].CapShift 给出总额与逐条归属
```

专项验证：

```bash
# 随机差分（朴素模型对照，-v 打印输入/输出/判定依据）
go test -run TestRandomDifferential -v ./ontology
# 两档历史规模（1k vs 100k）单笔结算常数性
go test -run TestScaleComparison -v ./ontology
go test -bench=. -run '^$' ./ontology
# 并发等价性（竞态检测）
go test -race -run TestConcurrentSerializability ./ontology
```

设计取舍见 `ontology/DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
