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

## 半开区间增强索引（`intervalindex`）

`intervalindex` 包提供并发安全的 int64 半开区间 `[lo, hi)` 索引。

- `Insert(id, lo, hi)`：加入带正整数编号的区间，要求 `lo < hi`，编号唯一；同一 `(lo, hi)` 可对应多个编号。
- `Remove(id)`：按编号删除。
- `Stab(x)`：返回满足 `lo <= x < hi` 的全部编号。
- `Overlap(a, b)`：返回与查询区间 `[a, b)` 相交的全部编号。
- `Len()`：当前区间个数。

**相交判定（半开区间，端点精确）**

- 点刺：`lo <= x && x < hi`，故 `x == lo` 命中、`x == hi` 不命中。
- 区间相交：`lo < b && a < hi`。因此 `[1,3)` 与 `[3,5)` 仅相邻、不相交；它们在公共端点 3 处被严格区分。
- 查询零宽或反向（`a >= b`）整体拒绝，返回 `ErrInvalidQuery`。

**输出顺序**：`Stab` 与成功的 `Overlap` 均按 `(lo, 编号)` 升序返回（`hi` 不参与排序）。

**可区分的拒绝原因**（均为哨兵错误，用 `errors.Is` 判断；被拒绝的操作不改变索引）：

- `ErrNonPositiveID`：编号非正；
- `ErrEmptyInterval`：`lo >= hi`；
- `ErrDuplicateID`：编号已存在；
- `ErrIDNotFound`：`Remove` 的编号不存在；
- `ErrInvalidQuery`：`Overlap` 的 `a >= b`。

`Insert` 按“非正编号 → 空区间 → 重复编号”的固定顺序只报告第一个原因。

**数据结构与复杂度保证**

区间存放在按 `(lo, id)` 排序的 treap 中，每个子树聚合 `minLo`、`maxLo`、`maxHi`。查询沿树做有序遍历并用聚合值整体剪枝：

- `Insert` / `Remove`：期望 `O(log n)`；
- `Stab` / `Overlap`：期望 `O(log n + k)`，`k` 为命中数；无命中时仅考察 `O(log n)` 条区间，不随区间总数线性增长——删除任意区间（包括当前最大 `hi` 的区间）后该上界仍成立，因为聚合值随删除重建。

包内未导出计数器（`examinedCount` / `resetExamined`）累计查询实际考察的区间记录数（被剪枝子树不计）。`TestNoHitExaminedBounded` 在 1,000 与 100,000 个相邻区间下对比该计数器，并在删除当前最大 `hi` 的区间后复验；本地实测无命中考察量约为 9 → 15 条（区间数扩大 100 倍）。

**并发与可复现**

所有方法可并发调用：写操作互斥、读操作共享，结果等价于某个串行顺序。treap 优先级由 `(lo, hi, id)` 经确定性 SplitMix64 导出，相同调用序列重放得到完全相同的树形态与结果（见 `TestDeterministicReplay`）。

**本地验证**

```bash
# 全量测试（含 2000 组随机操作与朴素扫描对拍）
go test ./intervalindex/ -v

# 竞态检测
go test -race ./...

# 只看考察量上界对比日志
go test ./intervalindex/ -run TestNoHitExaminedBounded -v

# 只看 2000 组对拍的输入/输出/判定依据日志
go test ./intervalindex/ -run TestRandomDifferential -v
```

`TestRandomDifferential` 使用固定随机种子重放 2000 组混合操作（插入/删除/点刺/区间查询，含各类非法输入），逐条与逐个扫描的朴素实现对拍，日志打印每步的输入、双方输出与判定谓词；另覆盖 `x` 恰等于 `lo`/`hi`、相邻区间不相交、同端点多编号顺序、零宽查询被拒、包含与被包含、删除后再插入同编号等边界用例。
