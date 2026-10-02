# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## mergebase：提交图合并基选择器

`mergebase` 包（`mergebase/`）提供并发安全的提交图登记与合并基查询。

### 定义

- **世代号（generation）**：根提交为 1，其余为 1 加全部父提交世代号的最大值，`Gen(id)` 查询。沿父边世代号严格递减。
- **祖先**：提交是它自己的祖先；`IsAncestor(a, b)` 判定 a 是否为 b 的祖先（含相等）。
- **合并基（merge base）**：`MergeBases(a, b)` 返回公共祖先集合 CA（同时是 a 与 b 祖先的全部提交）中的极大元——即不是 CA 内其他任一提交的祖先的那些提交。结果按 id 字节序升序；a 是 b 的祖先时恰为 `{a}`；无公共祖先时返回空集而非错误。

### 剪枝依据

`MergeBases` 采用 paint-down 遍历：从 a、b 分别向根部传播 P1/P2 标记， frontier 用以世代号为键的最大堆维护，每次扩展世代号最高的提交。被两侧同时到达的提交即为公共祖先，记入结果并将其祖先方向标记为 STALE；当 frontier 中不存在非 STALE 提交时遍历终止。由于世代号沿父边严格递减，合并基之下的历史不会被触及，遍历规模只取决于分支分叉点到合并基的距离，与历史总长度无关。候选结果再经一次「是否为其他候选的祖先」过滤（同样按世代号剪枝），保证恰为 CA 的极大元。测试 `TestTraversalIndependentOfHistoryLength` 用包内非导出计数器 `visited` 证明：N=1000 与 N=100000 的线性主干上求分叉分支的合并基，遍历提交数完全相同（均为 22）。

### 错误优先级

`Commit(id, parents)` 校验只报第一个错误，顺序为：id 为空（`ErrEmptyID`）→ id 已登记（`ErrDuplicateID`）→ 父列表超过 8 个（`ErrTooManyParents`）→ 父列表含重复（`ErrDuplicateParent`）→ 父未登记（`ErrUnknownParent`，按下标最小者）。`Gen`/`IsAncestor`/`MergeBases` 遇到未登记 id 报 `ErrUnknownCommit` 并指明是哪个 id（a 先于 b），与登记期错误可区分。被拒绝的操作不改变已登记的提交。

### 本地验证

```bash
go test ./mergebase            # 全部用例
go test -race ./mergebase      # 并发竞态检测
go test -v ./mergebase -run TestRandomDifferential   # 2000 组随机 DAG 与朴素定义对拍，打印输入/输出/判定依据
go test -v ./mergebase -run TestTraversalIndependentOfHistoryLength  # 遍历规模与历史长度无关
```

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
