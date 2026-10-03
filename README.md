# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 原位差分重建排序器

根包提供 `NewSorter(maxSize, maxInstr)` 与 `Sorter.Plan(n, delta)`。`delta` 按新文件顺序由 `Copy(src, len)` 和 `Add(data)` 组成，指令长度按 `int64` 紧排，依次得到各自的 `dst` 与新文件长度 `m`。

### 执行模型

- 初始缓冲区前 `n` 字节为旧文件，缓冲区长度为 `max(n,m)`，第 `m` 字节之后不参与结果。
- 普通 `Copy(src,dst,len)` 按 `memmove` 语义执行；重叠等价于先完整读取源区再写入目的区。
- `Add(dst,data)` 在所有 Copy 完成后写入字面量；返回的 `Add.data` 是独立副本。
- `src == dst` 的 Copy 不改变缓冲区，直接丢弃，不参与建图或统计。

### 依赖图与破环

- 对不同 Copy A、B，若 A 的源区间 `[src,src+len)` 与 B 的目的区间 `[dst,dst+len)` 相交，则添加 A→B，表示 A 必须先执行。
- 同一条 Copy 的源、目的自相交不产生自环。
- 拓扑执行时，在入度为 0 的 Copy 中选 `dst` 最小者。
- 没有入度为 0 的节点时，重新计算当前剩余图的强连通分量；只考虑大小至少 2 的分量，并在其中选择 `(len,dst)` 字典序最小者暂存。
- 最终计划顺序固定为：按 `dst` 升序的全部 Stash（slot 从 0 开始）、非暂存 Copy、按 `dst` 升序的 Unstash、按 `dst` 升序的 Add。
- `StashBytes` 与 `StashedCopyCount` 只统计本次计划中被暂存的 Copy；`Edges` 是丢弃原地 Copy 后实际建出的边数。

### 统计与复杂度

- `Stats()` 返回已受理计划数、累计暂存字节和累计参与建图的 Copy 数；被拒绝的调用不更新统计。
- 所有方法可并发调用；内部用互斥保证累计统计等价于某个串行顺序。
- 由于非自交 Copy 的目的区间按新文件平铺且互不重叠，一条源区间相交的目的节点构成连续段。建图用二分定位连续段和差分计算入度，不枚举所有 Copy 对；区间比较次数为 `O(k log k)`。SCC 遍历通过段树报告入边，避免物化极端稠密图。

### 参数错误

按顺序返回第一个错误：

- `n < 0` 或 `n > MaxSize`：`ErrSize`。
- Copy 越界、`len < 1`、空 Add、无法识别的指令或指令数超过 `MaxInstr`：`ErrBadDelta`。
- 所有指令累计长度 `m > MaxSize`：`ErrSize`。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
go test ./...
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 2000 组随机指令流（对照朴素建边/重算 SCC，并在真实缓冲区执行）
go test -v -run TestRandomAgainstNaiveAndBuffer

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

如果 `go` 不在 `PATH`，本机工具链位于 `/usr/local/go/bin`。沙箱中可使用 `GOCACHE=/tmp/go-cache` 指定可写构建缓存。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
