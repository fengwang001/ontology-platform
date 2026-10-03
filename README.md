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

## 单核 EDF 过载接纳控制器

实现在 `admission` 包中。`New()` 创建时钟初值为 0 的控制器：

- `Submit(id, now, C, d, M, v)`：按规则推进时钟并尝试接纳作业，返回是否接纳、本次驱逐编号和拒绝原因。
- `Advance(now)`：只推进时钟并结算在此期间完成的待处理作业。
- `Pending()`：按 EDF 序返回 `(编号, 剩余量, 已开始标记)`。
- `Value()`：返回累计结算价值。
- `Evicted()`：按实际驱逐先后返回编号。

### 可行性判定

待处理作业按 `(d, 编号字节序)` 升序排列。从当前时钟 `t` 开始扫描，依次累加剩余量得到完成时刻
`f_i=t+Σ_(j≤i) r_j`。当且仅当每个作业均满足

```text
f_i - d_i ≤ M_i
```

时可行；恰好等于容忍延迟也通过。每次可行性判定完整扫描当前待处理序列一遍，驱逐后不重新排序，只复用原 EDF 序。

### 价值密度与驱逐

过载时，只在尚未开始的作业（包含新作业）中选择价值密度 `v/剩余量` 最小者。实现不使用浮点数，而是交叉相乘比较：

```text
v1*r2 < v2*r1
```

密度相等时编号字节序更大的作业先驱逐。每驱逐一个作业后重新做一次可行性判定，一旦可行立即接纳新作业。已开始作业即使之后被 EDF 抢占，也永久不可驱逐。

### 驱逐恢复与拒绝原因

提交先在副本上推进和判定。若驱逐到新作业自身，或再无可驱逐作业仍不可行，则本次为过载拒绝；副本中本步试探性驱逐的所有作业全部恢复，不进入 `Evicted()` 记录。

拒绝原因按以下优先级只返回第一个：

1. `ErrInvalidArguments`
2. `ErrClockRolledBack`
3. `ErrDuplicateID`
4. `ErrImpossible`
5. `ErrOverloaded`

任何拒绝都不会落盘试探性时间推进、作业完成结算、待处理集合、累计价值或驱逐记录；后续成功操作会重新补做该推进。

### 延迟折价

作业在完成时刻 `f` 结算：

```text
floor(v * (M + 1 - max(0, f-d)) / (M + 1))
```

因此 `f≤d` 时得到完整价值 `v`，延迟为 `M` 时仍得到 `floor(v/(M+1))`，超过 `M` 的调度在接纳判定阶段已被拒绝。

### 并发与复杂度

控制器内部使用读写互斥锁，所有提交、推进和查询都等价于某个合法串行顺序。一次 `Submit` 最多排序一次；可行性判定次数至多为“本步驱逐个数 + 1”；每次判定线性扫描当前待处理序列。

### 本地验证

```bash
# 全量测试
go test ./...

# 查看确定性边界用例和 2000 组随机对拍日志
go test -v ./admission

# 竞态检测
go test -race ./admission
```

随机测试内置逐单位运行的朴素 EDF 模拟器，对每组操作的输入、输出、接纳/驱逐依据、待处理集合和累计价值逐项对拍。若本机 Go 缓存目录不可写，可使用 `GOCACHE=/tmp/go-build-cache` 前缀。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
