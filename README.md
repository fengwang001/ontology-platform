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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 支付卡不可能行程检测器（`impossibletravel` 包）

按卡维护最近一次被接受交易的锚点 `(t0, x0, y0)`、不可能行程拒绝
历史、冻结标志与差旅豁免窗口，判定后续交易所需的移动速度是否可行。

### 构造参数

`NewDetector(V, K, H)`：最高速度 `V`（公里/小时，1 到 10^6）、冻结
阈值 `K`（1 到 100）、拒绝窗口 `H`（秒，1 到 10^12）。越界返回
`ErrInvalidParam`。

### 判定次序

`Check(card, t, x, y)` 按固定次序只报告第一个命中的结果：

1. 参数非法（卡号为空、`t` 不在 `[0, 1e12]`、坐标绝对值超过 `1e9`）
2. 卡已冻结
3. 重复：`t、x、y` 与锚点三项全部相同
4. 乱序：`t < t0`（`t == t0` 但坐标不同不算乱序）
5. 无锚点：接受并建立锚点
6. 差旅免检：`t` 落在窗口 `[from, to)`（左闭右开）内则接受
7. 速度判定（见下）

### 速度判定（整数公式）

曼哈顿距离 `d = |x-x0| + |y-y0|`，时间差 `dt = t - t0`。当
`d*3600 <= V*dt` 时接受（恰等接受，`dt=0` 时仅 `d=0` 可接受，而
`d=0 且 dt=0` 已先被判为重复），否则拒绝为不可能行程。全程使用
int64 整数运算，无浮点误差；最大值 `V*dt = 1e6 * 1e12 = 1e18` 不溢出。

接受（含免检）后锚点更新为本笔，拒绝历史不清除；不可能行程拒绝时
锚点不变，把本笔 `t` 追加到拒绝历史。

### 拒绝窗口计数与冻结

不可能行程拒绝时，令 `cnt = 1 +` 历史中此前满足 `t_j > t - H` 的
项数（`t_j == t - H` 视为已过期；历史可能非单调，大于本笔 `t` 的
项也计入）。`cnt >= K` 时卡被冻结，该笔仍报不可能行程。冻结期间
一切 `Check` 恒报已冻结，直到 `Unfreeze`。

### 批量排序规则

`CheckBatch(card, txns)`（1 到 1000 笔）先把各笔按 `t` 升序稳定
排序（`t` 相同保持原下标次序），再逐笔套用 `Check` 的判定与状态
更新（批内中途冻结则其后各笔报已冻结），返回按原下标次序排列的
结果。批内任一笔参数非法则整批拒绝，不改变任何状态。`CheckBatch`
是一个原子步骤。

### 差旅窗口与解冻

`Declare(card, from, to)` 登记差旅窗口 `[from, to)`，要求
`0 <= from < to <= 1e13`；每张卡只保留最后一次登记，对从未出现的
卡也可登记。`Unfreeze(card)` 清除冻结标志与全部拒绝历史，锚点与
差旅窗口保持；卡不存在报 `ErrCardNotFound`，未冻结报
`ErrCardNotFrozen`，按此顺序只报第一个。

### 并发与可复现性

所有方法（含 `State` 查询）可并发调用，内部以互斥锁串行化，结果
等价于某个串行顺序；相同调用序列重放得到完全相同的判定序列与各卡
状态。不变量：同卡锚点 `t` 单调不降；拒绝历史每项不小于其记入时
的锚点 `t`；冻结当且仅当某次不可能行程拒绝时 `cnt >= K`。

### 本地验证

```bash
# 规则覆盖测试 + 2000 组随机序列与朴素模拟对照（日志含输入、输出与判定依据）
go test -v ./impossibletravel/

# 竞态检测
go test -race ./impossibletravel/
```
