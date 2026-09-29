# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 会话窗口切分器（session 包）

`session.Partitioner` 按活动间隙把每个键的事件切成会话窗口，支持乱序、
迟到事件，切分结果与到达顺序无关，且并发安全。

### 相连判定与边界规则

- **相连判定**：同一键下两个事件时间差绝对值不超过间隙阈值 `gap` 即相连，
  即 `|t1 - t2| <= gap`；**恰好等于间隙时仍属同一会话**（闭区间判定）。
- **会话定义**：会话是相连关系的连通分量，只取决于事件时间集合，
  因此同一批事件按任意顺序到达，切分结果始终一致且可复现。
- **乱序插入**：事件时间 `t` 插入时与既有会话比较——
  - 与左右两侧会话都相连 ⇒ 把两侧合并为一个会话；
  - 只与一侧相连 ⇒ 并入该侧（区间相应扩展）；
  - 两侧都不相连 ⇒ 自成新会话 `[t, t]`。
- **相同时刻**：时间相同的事件差为 0，必相连，归入同一会话并计入事件数。

### 原子拒绝与可区分错误

`Add` 先整批校验再应用，任一事件非法则整批拒绝且不改变任何状态。
拒绝原因可用 `errors.Is` 区分：

- `ErrInvalidGap`：间隙阈值非正（构造时拒绝）；
- `ErrInvalidCapacity`：键容量上限非正（构造时拒绝）；
- `ErrEmptyKey`：事件键为空；
- `ErrCapacityExceeded`：引入的新键使键数量超出容量上限（已有键不受影响）。

### 并发

所有方法均可并发调用：写入串行化，`Sessions` / `AllSessions` / `Keys` /
`Check` 等查询与自检可并发执行，并发读取结果逐字段一致。
`Check` 自检内部不变量（区间有序、相邻区间间隔大于 `gap`、计数守恒、
键数不超容量）。

### 本地验证方法：排序批量切分对照

会话是连通分量，因此可以用"按事件时间排序后一趟批量切分"作为参考实现，
与增量切分结果逐字段对照：

```go
// 参考实现：同一键的事件时间排序后一趟切分
func referenceSplit(times []int64, gap int64) [][2]int64 {
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	var out [][2]int64
	start, end := times[0], times[0]
	for _, t := range times[1:] {
		if t-end <= gap { // 闭区间：恰好等于间隙仍相连
			end = t
		} else {
			out = append(out, [2]int64{start, end})
			start, end = t, t
		}
	}
	return append(out, [2]int64{start, end})
}
```

验证步骤：

```bash
# 顺序无关性：同一批事件的正序/逆序/随机打乱结果互相一致，
# 并与参考实现逐字段对照
go test -run TestPartitioner_OrderIndependence -v ./session

# 带竞态检测的全量测试（含并发读取一致性）
go test -race -v ./session
```

单测日志会打印每条输入事件、每个会话的区间 `[start,end]` 与逐步判定依据
（左右邻会话、距离与 `gap` 的比较、并入/合并/新建结论）。

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
