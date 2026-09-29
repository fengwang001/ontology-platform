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

## 批量重命名执行器（`rename` 包）

`rename` 包把一组「旧名 -> 新名」的映射在同一命名空间中按**同时生效**的语义
拆成单步改名执行，并支持整体撤销。互换两名是合法的；任何单步的目标名在执行
时都不存在；撤销后命名空间逐项复原。

### 执行顺序推导

1. 先做整体校验，任一违规都整体拒绝且不改任何名字，错误可用 `errors.Is` 区分：
   `ErrEmptyName`（名字为空）、`ErrDuplicateOld`（同一旧名出现两次）、
   `ErrConflictingNew`（两个旧名映射到同一新名）、`ErrOldNotFound`（旧名不存在）、
   `ErrNewNameExists`（新名已存在且不在本批被搬走的旧名中）。
2. 映射到自身视为无操作，先行剔除。
3. 把剩余映射看成有向图（每个旧名出度为 1、每个新名入度至多为 1），
   连通分量只有两种：**链**与**环**。
   - 链：从入度为 0 的链头走到链尾（链尾的新名当前空闲），执行时自链尾向
     链头**倒序**改名，先搬走占用目标的名字。
   - 环：借一个临时名破环（见下），之后按链处理，最后临时名归位。
4. 各链与环按**其中字典序最小的名字**升序依次处理，因此步骤序列与映射项的
   给出顺序无关。

### 破环与临时名规则

- 每个环**恰好**借用一个临时名：先把环中字典序最小的名字改为临时名，
  再沿环倒序改名，最后把临时名改为它应得的最终名。
- 临时名为「调用方给定前缀 + 最小的非负序号」（如 `tmp0`、`tmp1`…），
  该名字既不被命名空间占用，也不与本批出现的任何名字（含已分配的临时名）
  相同；被占用时序号顺延。

### 失败回滚与撤销语义

- 执行中某一步失败时，已执行的步骤按**逆序**撤回，命名空间逐项复原
  （错误为 `ErrStepFailed`）。
- `Execute` 返回批次 ID；`Undo(id)` 只作用于**最近一次成功**的批次且只能
  一次：其后已有新批次时返回 `ErrStaleUndo`，重复撤销返回
  `ErrAlreadyUndone`，无可撤销批次时返回 `ErrNothingToUndo`。
- 批次通过互斥锁串行生效；并发读者（`Snapshot`）只能看到某个批次之前或
  之后的完整命名空间，看不到含临时名的中间态。

### 本地验证

```bash
# 全量测试（含竞态检测与详细日志：输入、输出与判定依据）
go test -race -v ./rename/

# 覆盖场景：互换、长链、十个互不相交的环恰用十个临时名、临时名被占用时顺延、
# 第 k 步注入失败后的复原、撤销与重复撤销、各校验拒绝原因、顺序无关性、
# 并发批次与读者一致性
go test ./rename/ -run 'TestSwap|TestLongChain|TestTenDisjointCycles'
```
