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

## 循环赛赛程与赛果登记

`roundrobin` 包提供 `NewRegistrar(N, L)`、`Fixtures`、`Record`、`Withdraw`、`Points`、`GoalDiff`、`Played` 和 `Pending`。合法范围为 `2 <= N <= 64`、`L in {1,2}`。

### 圆法赛程

- 奇数队数时补虚拟队 `N+1`，令参赛位置数 `m` 为不小于 `N` 的最小偶数。
- 初始位置数组为 `p=[1,2,...,m]`；每轮按 `p[i]` 与 `p[m-1-i]` 镜像配对。
- 每轮后执行 `[p[0], p[m-1], p[1], ..., p[m-2]]`，即固定 `p[0]`，把末位移到下标 1。
- 单循环共 `m-1` 轮；双循环的第 `m-1+r` 轮复用第 `r` 轮对阵并交换主客。
- 含虚拟队的对阵标记 `Bye=true`，真实队伍轮空，不能登记比分，也不计场次。
- 主客规则：`i=0` 时奇数轮 `a` 主场、偶数轮 `b` 主场；`i>=1` 时 `r+i` 为偶数则 `a` 主场，否则 `b` 主场。

### 比分、积分与退赛

- `Record(round, home, away, homeGoals, awayGoals)` 只接受赛程中存在、主客方向完全一致且非轮空的比赛。
- 拒绝顺序固定为：无效轮次/主客方向/轮空；比分小于 0 或大于 100；已被技术判定；已登记。
- 胜方得 3 分，平局双方各 1 分；进球、失球、场次和净胜球同步累计。
- `Withdraw(t)` 只接受真实队号且不可重复退赛；该队所有尚未登记的比赛立即按对手 3:0 获胜判定，已登记比分保持不变。
- 技术判定计入双方场次、积分、进球与失球；退赛队每场技术判负的净胜球变化为 -3。
- 所有公开方法由同一把锁保护，并发调用的结果等价于某个合法串行顺序。

### 本地验证

```bash
# 如果系统 Go 构建缓存只读，可指定临时缓存
GOCACHE=/tmp/go-cache-ontology go test ./...

# 竞态检测
GOCACHE=/tmp/go-cache-ontology go test -race ./...

# 查看包含输入、输出和判定依据的详细日志
GOCACHE=/tmp/go-cache-ontology go test -v ./roundrobin
```

测试覆盖 N=4、5、6 的逐轮手工赛程，N=2 到 64 与独立朴素圆法重算的完整对照，轮空拒绝，双循环主客互换，积分/净胜球恒等式，退赛技术判负，拒绝不改状态，确定性重放和并发安全。
