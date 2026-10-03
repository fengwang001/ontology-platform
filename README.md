# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## FlexRay 周期仲裁器

实现在 `flexray` 包中：

- `New(ns, nm, lt)` 创建仲裁器；`1 <= Ns <= 64`、`0 <= Nm <= 256`、`0 <= Lt <= Nm`。
- `Assign(id, base, rep)` 登记帧；`rep` 只能是 1、2、4、8、16、32、64，且 `0 <= base < rep`。
- `Post(id, length, tag)` 投递消息；静态帧 `id=1..Ns` 只有一个缓冲，动态帧 `id=Ns+1..Ns+Nm` 各维护最多 8 条的 FIFO。
- `Pending(id)` 返回当前 FIFO 标记；静态帧返回 0 或 1 个缓冲标记。
- `Stats()` 返回累计发送数、空帧数和静态缓冲覆盖数。

`Cycle()` 按固定顺序处理当前周期计数 `c`：

1. 静态段按编号 `1..Ns` 逐个扫描。未登记或不满足 `c mod rep = base` 的帧静默；激活但缓冲为空记一次空帧，并令未用时隙 `u` 加一；激活且有消息则按静态时隙号发送并清空缓冲。
2. 动态段长度为 `N = Nm + u`，初始动态编号 `k = Ns + 1`、小时隙 `i = 1`。每一步都令编号与小时隙同步推进：
   - 若 `k` 已登记且激活、队首存在、`i <= Lt`、并且 `i + L - 1 <= N`，发送队首，起始小时隙记为 `i`，随后 `i = i + L`、`k = k + 1`。
   - 否则只空过一个小时隙：`i = i + 1`、`k = k + 1`。
   - `k > Ns + Nm` 后没有动态帧，剩余小时隙继续空过；同一动态帧每周期最多发送队首一条。
3. 周期结束后 `c = (c + 1) mod 64`。

拒绝错误按以下顺序只返回第一个：

1. `ErrInvalidArgument`：构造、`base/rep`、动态长度等参数非法。
2. `ErrInvalidID`：编号不在 `1..Ns+Nm`。
3. `ErrDuplicateID`：`Assign` 重复登记同一编号。
4. `ErrNotAssigned`：`Post` 的编号未登记。
5. `ErrQueueFull`：动态 FIFO 已有 8 条消息。

被拒绝的操作不会改变登记、缓冲、FIFO、覆盖计数或周期计数。所有方法由互斥锁保护，并发调用的结果等价于某个串行调用顺序。

### 本地验证

```bash
go test ./flexray -v
go test -race ./...
```

随机对拍测试 `TestRandomNaiveModelComparison` 重放 2000 组操作序列，并与测试内逐步朴素模拟比较 `Cycle()`、`Pending()` 和 `Stats()`；使用 `-v` 时会打印前 20 组的输入、输出与判定依据。

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
