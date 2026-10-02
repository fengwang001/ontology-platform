# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## deposit：活期存款积数计息器

`deposit` 包实现按日记录存取与年利率调整、在结息日按积数法计息的账户，
支持对已结息区间内某一天存取的冲正，并沿结息链逐区间重算。

### 积数公式与余数结转

- 年利率 `r` 以万分比整数记，按 360 天计，除数 `D = 360 * 10000 = 3600000`。
- 结息区间左闭右开：`Settle(d)` 覆盖第 `prev` 天到第 `d-1` 天，第 `d` 天
  本身不计入本次，而计入下一次。
- 积数 `N = R + Σ B_k * r_k`（`k` 遍历覆盖区间），利息 `i = ⌊N / D⌋`，
  新余数 `R = N mod D`（恒满足 `0 <= R < D`），余数结转至下一区间。
- 利息 `i` 自结息日 `d` 的日终余额起贷记，参与后续区间积数。

### 日终余额口径

第 `k` 天的日终余额 `B_k` = 第 `k` 天及之前全部已接受存取与冲正的净额
+ 结息日不超过 `k` 的各次结息贷记利息之和。同日多笔存取只按日终余额计
一次；结息日当天的存取不影响本次结息。第 `k` 天年利率 `r_k` 为日期不超
过 `k` 的最后一次 `SetRate`（没有则为 0），当日起即用新利率。

### 冲正的重算链

`Correct(d, delta)` 令第 `d` 天起（含）每天日终余额增加 `delta`，随后对
结息日严格大于 `d` 的结息记录按先后逐条重算：每条以前一条重算后的余数
作为 `R`，按改变后的 `B_k`（含前序记录新贷记的利息）与不变的 `r_k` 重
新累计 `N`，得到新利息与新余数并更新记录。重算完成后校验第 `d` 天到最
大操作日 `m` 的每一天日终余额：小于 0 报「冲正后余额为负」，大于 10^11
报「冲正后余额越限」，校验失败则整个冲正不生效。`Correct` 返回新余额、
新余数与发生变化的结息记录列表（利息或余数任一不同即列入）。

### 本地验证

```bash
# 全部单元测试（含规格示例、边界与错误区分）
go test ./deposit/

# 2000 组随机序列与逐日朴素重放对照（-v 打印输入、输出与判定依据）
go test ./deposit/ -run TestRandomAgainstNaive -v

# 竞态检测
go test ./deposit/ -race
```

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
