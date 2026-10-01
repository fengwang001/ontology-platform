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

## heap 包：带可见性映射的堆表与仅索引扫描

`heap` 包实现按页组织的堆表，每页维护一个**全可见位**，扫描时全可见页上的索引条目直接产出、免回表。

### 全可见位的置位与清除

- **置位**：仅由 `Vacuum(页, h)` 设置。物理移除该页中 `xmax≠0 且 xmax<h` 的行后，
  若剩余每一行都满足 `xmin<h 且 xmax=0` 则置位（空页为真），否则为假。
- **清除**：`Insert` 或 `Delete` 触及某页时立即清除该页的全可见位（无论新写入的
  `xmin`/`xmax` 与已用的 h 大小关系如何）。
- 不变式：任何时刻全可见位为真的页上，每一行都满足 `xmin < 全局最大已用 h 且 xmax = 0`。

### 快照与 Vacuum 的约束

- `Snapshot(s)` 要求 `s>0` 且 `s ≥ 全局最大已用 h`，否则拒绝且不占号；编号从 1 起连续递增。
- `Vacuum(页, h)` 要求 `h>0`、页合法，且不存在未注销且值小于 h 的快照；成功后
  `全局最大已用 h = max(旧值, h)`。
- 被拒绝的操作不改变任何状态；错误按参数列出的顺序只报第一个，各原因对应
  可区分的哨兵错误（见 `heap/errors.go`）。

### 回表计数口径

`Scan(lo, hi, 快照编号)` 按 `(key, 页, 槽)` 升序遍历索引中 `lo≤key<hi` 的条目：

- 条目所在页全可见位为真：直接产出，计入**免回表次数**（`Skips`）。
- 否则：计入**回表次数**（`Fetches`），仅当该行 `xmin<s 且 (xmax=0 或 xmax≥s)` 时产出。

产出集合与「总是回表」的朴素判定逐项相同。

### 本地验证

```bash
# 单元测试 + 2000 组随机操作序列与朴素判定对拍（含竞态检测）
go test -race ./heap

# 查看对拍日志（输入、输出与判定依据）
go test -race -v -run TestRandomDifferential ./heap
```
