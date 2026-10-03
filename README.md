# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## matcher：云承诺折扣用量匹配器

`matcher` 包按小时把各族用量行依次匹配到折扣率不同的承诺额度上，
支持预付摊销、分级覆盖与跳过小时的补算。所有方法可并发调用，
结果等价于某个串行顺序（内部以互斥锁串行化）。

### 承诺登记

`AddCommit(id, fam, start, n, h, up, d)`：

- `id` 0..10^6 且不得重复；`fam` 0..99（0 为通用，可覆盖任意族，否则只覆盖同族）。
- 有效区间为 `[start, start+n)`（左闭右开），右端恰等于小时即过期。
- `h` 为每小时承诺额，有效期内无论用量多少都计费。
- `up` 为预付总额，摊销规则：前 `n-1` 个有效小时各摊 `floor(up/n)`，
  最后一个有效小时摊尾项 `up-(n-1)*floor(up/n)`。
- `d` 为折扣万分比，折后价为按需价乘 `(10000-d)/10000`。
- 校验顺序：参数非法 > id 重复 > 起始已过（`start <= lastHour`），只报第一个；
  被拒绝的操作不改变承诺表与 `lastHour`。

### 单小时处理（Apply）

`Apply(t, lines)` 逐小时处理 `lastHour+1..t` 并各返回一份报告
（跳过的小时没有用量行，承诺照常计费、全部未使用），处理完 `lastHour=t`。
校验顺序：参数非法（`t<0`、`t>lastHour+10000`、行数超 1000、族或按需价越界）
> 时间回退（`t <= lastHour`），只报第一个。

每小时取有效承诺，按 **d 降序、id 升序** 排序后依次处理。对每个承诺令
剩余额度 `R=h`、`m=10000-d`，按输入顺序扫描族相符且剩余按需价 `p'>0` 的行：

- 整行覆盖：若 `R >= e`，其中 `e = ceil(p'*m/10000)`（向上取整），
  则 `R -= e`，`covered += p'`，`p' = 0`。
- 部分覆盖：若 `0 < R < e`，被覆盖的按需价 `q = floor(R*10000/m)`
  （向下取整，可证 `1 <= q < p'`），`p' -= q`，`covered += q`，`R = 0`，
  该承诺停止扫描；剩余 `p'` 仍可被排在后面的承诺覆盖。

小时账单 = 各有效承诺的 `(h + 该小时摊销)` 之和 + 全部用量行剩余 `p'` 之和。
每个承诺的每小时报告含 `used = h - R`、`unused = R`（恒有 `used+unused == h`）
与 `covered`。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照、并发测试）
go test ./matcher/

# 查看随机对照日志（打印每组输入、输出摘要与判定依据）
go test ./matcher/ -run TestRandomAgainstNaive -v

# 竞态检测
go test -race ./matcher/
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
