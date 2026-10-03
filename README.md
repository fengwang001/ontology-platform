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

## 用户级广告频次控制器

实现位于 `frequency`，构造参数依次为：

- `Wg`：全局窗口长度，范围 `[1, 10^9]`。
- `Cg`：全局窗口上限，范围 `[1, 10^6]`。
- `K`：全局曝光对系列日上限的收紧步长，范围 `[1, 10^6]`。
- `Cc`：系列日上限，范围 `[1, 10^6]`。
- `g0`：创意基础连播间隔，范围 `[1, 10^9]`。

`Admit`、`Peek` 和 `AdmitBatch` 均校验用户、系列、创意非空以及 `now ∈ [0, 10^15]`；构造参数越界返回错误。拒绝原因只返回参数校验后的首个失败原因：

1. 时钟回退：用户已有记录且 `now < t_last`。
2. 创意连播间隔。
3. 全局窗口上限。
4. 系列日上限。

### 判定公式与次序

对每个用户按如下顺序判定，任一项失败立即拒绝且不写入状态：

1. **创意连播间隔**：若最近一次已放行创意等于当前创意，令末尾连续同一创意次数为 `s`，该计数不随全局窗口过期重置，则要求
   `now - t_last >= g0 * min(s, 3)`。
   最近一次创意不同则无连播要求；不同创意放行后，新的连播计数重新从 1 开始。
2. **全局窗口**：统计放行前满足 `t + Wg > now` 的曝光数 `cg`，要求 `cg < Cg`。
   因此 `t + Wg == now` 的记录已过期；`now - t == Wg - 1` 时仍计入。
3. **系列日上限**：`day = floor(now / 86400)`，统计同一用户、同一系列、同一日的已放行数 `n_c`。
   有效上限是 `E = max(1, Cc - floor(cg / K))`，其中 `cg` 使用第二项在本次放行前算出的窗口内个数。
   要求 `n_c < E`。

`Decision.Evidence` 与 `BatchResult.Evidence` 返回判定时的 `s`、经过时间、要求间隔、`cg`、`day`、`n_c` 与 `E`，可复现拒绝依据。

### 状态、并发与批量

- 每个用户一把互斥锁；不同用户互不阻塞、互不影响，同一用户的操作等价于某个串行顺序。
- 全局窗口记录按时间有序保存，成功提交时弹出并压缩已过期前缀；保留规模受 `Cg` 约束，不随累计曝光数增长。
- 系列计数只保留当前日的 map；进入新自然日后清空旧 map。创意连播计数和最近时刻跨日保留。
- `Peek` 只复制状态并判定，不弹出共享状态、不推进时钟、不增加计数。
- `AdmitBatch` 列表长度必须为 `1..1000`。请求在私有状态副本上按顺序判定，前面的放行影响后续请求；任一条失败，返回首个失败下标和原因并整体回滚，全部成功才一次提交。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看随机对照日志：2000 组随机序列与朴素模拟器逐条对照
go test -run TestRandomSequencesAgainstNaiveSimulation -v ./frequency

# 查看 1000 / 100000 两档回收弹出次数与保留记录数
go test -run TestReclamationDoesNotGrowWithAdmits -v ./frequency

gofmt -w frequency
go vet ./...
```

如当前 shell 找不到 Go 或默认构建缓存不可写，可使用：

```bash
GOCACHE=/tmp/go-cache GOTOOLCHAIN=local /usr/local/go/bin/go test ./...
```
