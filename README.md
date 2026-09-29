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

## 会话窗口切分器（`ontology` 包）

`ontology/session.go` 提供按活动间隙（inactivity gap）把每个键的事件流切分为
会话（session）的能力，支持事件乱序 / 迟到到达。

### 相连判定与边界规则

- 两个事件时间戳差的绝对值 `|t1 - t2| <= gap` 即“相连”；**恰好等于阈值
  `gap` 仍属于同一会话**，只有严格大于 `gap` 才断开。
- 相连是传递关系，会话就是相连关系的**连通分量**。时间戳升序后只需比较相邻
  事件：相邻差 `> gap` 处断开，其余归为同一段。
- **相同时刻**的事件差为 0，必然相连；重复时刻自动去重。
- 乱序 / 迟到事件按时间位置插入，与其左、右相邻会话分别判定：
  - 两侧都相连：把左右两个会话与新事件**合并为一个会话**（跨会话合并）；
  - 只与一侧相连：并入该侧会话（更新该会话的 `Start` / `End`）；
  - 两侧都不连：自成一个新会话。
- 每个键独立切分，互不影响。

### 非法输入（整体拒绝、状态不变）

`NewSplitter(gap, maxKeys)` 与 `AddEvents(events)` 对非法输入返回可通过
`errors.Is` 区分的哨兵错误：

- `ErrNonPositiveGap`：间隙阈值 `gap <= 0`；
- `ErrEmptyKey`：事件的键为空字符串；
- `ErrKeyCapacityExceeded`：批次会引入超出 `maxKeys` 上限的新键。

`AddEvents` 是原子操作：先整批校验，再在内部状态的深拷贝副本上应用，
全部成功后才提交。任一条事件非法，整批拒绝，已有状态（包括批次中其他合法
事件涉及的键）保持不变。

### 并发与可复现性

- 内部以 `sync.RWMutex` 保护，`Sessions` / `AllSessions` / `SelfCheck`
  可与写入并发调用；所有查询返回结果的深拷贝，调用方修改返回值不影响内部状态。
- 切分结果只依赖事件集合本身，与到达顺序无关；同一批事件按任意顺序、整批或
  逐条到达，会话集合逐字段一致（`TestOrderIndependence` 以 200 组随机排列
  验证，`TestConcurrentReadsAndWrites` 在 `-race` 下验证并发读写与稳定快照的
  并发读取一致性）。

### 本地验证方法（排序后批量切分对照）

`ontology/reference.go` 中的 `SplitSorted(sortedTs, gap)` 是参考实现：将事件
按时间戳升序排序去重后，一遍扫描按相邻差 `> gap` 切分。增量切分器的
`SelfCheck()` 会对每个键取出全部事件、排序后与 `SplitSorted` 的结果逐字段
比对，同时校验内部不变量（段内相邻差 `<= gap`、段间差 `> gap`、区间与事件
列表一致）。

```bash
# 常规测试（单测日志会打印输入事件、每个会话的区间与相连判定依据）
go test -v ./ontology

# 竞态检测 + 覆盖率
go test -race -cover ./ontology
```
