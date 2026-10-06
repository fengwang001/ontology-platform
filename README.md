# ECS 感知递归解析缓存

对同一 (名字, 记录类型) 按应答适用范围缓存多份结果，按客户端地址选最长
覆盖前缀，并合并同源网段的并发上游查询。时钟与上游解析器由调用方注入。

## 环境要求

- Go 1.26+（`go version` 确认）

## 包结构

- `ecs.go`：对外类型（`Query` / `Answer` / `Result` / `Kind` / 地址族）、
  错误（`ErrInvalidArgument`、`ErrUpstreamFailure`）、`Clock` 与 `Upstream`
  接口及函数式适配器。
- `prefix.go`：地址族校验、前缀清零、前缀覆盖判定、名字规范化、内部键。
- `cache.go`：条目/桶结构、惰性过期、最长前缀命中、K 容量淘汰。
- `resolve.go`：查询主流程（校验 → 命中 → 并发合并 → 上游 → 分发/重试）。
- `DESIGN.md`：设计取舍、被放弃方案、复杂度论证与本地验证方法。

## 快速上手

```go
clk := ontology.ClockFunc(func() time.Time { return time.Now() })
up := ontology.UpstreamFunc(func(q ontology.Query) (ontology.Answer, error) {
    // 调用真实递归解析器，或在此注入测试替身
    return ontology.Answer{
        Kind: ontology.KindRecords, Records: rdata,
        TTL: 300, ScopePrefix: 24, // 结果适用于 /24
    }, nil
})
c := ontology.NewCache(64 /* 同名字同类型条目上限 K */, clk, up)

res, err := c.Query(ontology.Query{
    Name: "example.com.", Rrtype: 1,
    Family: ontology.FamilyV4, Client: ontology.Addr{203, 0, 113, 7},
    SrcPrefix: 24, // 向上游声明的源前缀；0 表示不暴露地址
})
```

否定结果（`KindNoName` / `KindNoType`）与正常结果使用相同的范围缓存与命中
规则；同键新写入整体覆盖旧结果。TTL 为 0 的应答返回但不缓存。

## 测试

```bash
# 若 Go 缓存目录位于只读分区：
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

go test -count=1 ./...            # 单元测试 + 1200 组随机对照
go test -race -count=1 ./...      # 竞态检测
go test -v -run TestDifferential  # 随机对照，逐步日志见 differential.log
go vet ./... && gofmt -l .
```

随机对照（`TestDifferentialAgainstNaiveModel`）用一份独立的朴素全表扫描
模型，对 1200 组、每组 40 步随机序列逐拍比较返回值、上游调用次数与存活
条目快照；覆盖：TTL 恰到期边界、长范围过期回落、范围 0 与范围大于源前缀、
族隔离、否定/正常互相覆盖、淘汰三级并列、并发合并与范围外重查、拒绝次序。
