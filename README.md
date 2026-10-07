# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 运行端到端示例场景
go run ./cmd/demo
```

## 住院病区感染病例接触者追踪与隔离期判定

根包 `ontology` 提供完整引擎，模块划分与设计取舍见 [docs/DESIGN.md](docs/DESIGN.md)。

### 操作

- 住宿：`Admit`（入住）、`Discharge`（出住）、`BackfillStay`（追补已结束区间）
- 病例：`RegisterCase`、`RegisterIsolation`、`CorrectOnset`、`RevokeCase`
- 查询：`Status`（患者隔离状态）、`CaseContacts`（病例密接/次密接清单）
- 诊断：`LastOpStats`（上一操作扫描记录数/推导病例数）、`Clock`

### 语义要点

- 时间为整数分钟 [0, 10^7]，区间左闭右开，未出住视为持续到 now；
  时钟单调，被拒绝的操作不改变状态与时钟。
- 传染期为 [发病-48h, 隔离时刻)；密接为传染期内同病房累计 >=120 分钟；
  次密接为与某密接在其暴露期 [首个重叠起点, 确诊登记时刻) 内累计 >=120 分钟。
- 隔离期：密接 = 最后接触 + 7 天，次密接 = 最后接触 + 3 天，恰到即解除；
  多病例叠加取仍在期内者中最严等级，解除时刻取各来源最晚。
- 错误可区分且按优先级只报第一个：参数非法 > 时钟回退 > 对象不存在 >
  状态不符 > 住宿冲突（`ontology.KindOf` 提取类别）。

### 示例

```go
e := ontology.NewEngine()
_ = e.BackfillStay("P", "W1", 1000, 2000, 5000)
_ = e.BackfillStay("X", "W1", 1000, 1120, 5000)
_ = e.RegisterCase("C1", "P", 1500, 5000)
st, _ := e.Status("X", 5000) // 密切接触者隔离中，解除时刻 1120+10080
ct, _ := e.CaseContacts("C1", 5000)
```

`naive` 包为独立朴素参考实现，用于差分测试对照主引擎。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 1500 组随机序列与朴素模型对照（日志写入 testdata/differential.log）
go test -run TestDifferentialAgainstNaive -v .

# 两档规模性能对照与基准
go test -run TestScale -v .
go test -run xxx -bench . .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
