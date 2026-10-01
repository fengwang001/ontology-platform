# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多粒度意向锁

`Manager` 管理多棵资源树，`Register(id, parent)` 登记节点，`parent == ""` 表示根。事务号必须为正整数；五种模式由 `IS`、`IX`、`S`、`SIX`、`X` 表示。

**强度偏序**

- `IS < IX < SIX < X`
- `IS < S < SIX < X`
- `IX` 与 `S` 不可比
- `join(a, b)` 是同时不小于二者的最小模式；例如 `join(IX, S) = SIX`，可比模式取较强者

**相容矩阵**

| 持有模式 | 相容的其他事务模式 |
| --- | --- |
| `IS` | `IS`、`IX`、`S`、`SIX` |
| `IX` | `IS`、`IX` |
| `S` | `IS`、`S` |
| `SIX` | `IS` |
| `X` | 无 |

**Lock 转换与校验**

1. 参数和节点登记按错误优先级校验。
2. 若事务已持有 `h`，且 `join(h, requested) == h`（即请求模式不更强），立即成功且不改变状态，也不再检查祖先或其他持有者。
3. 否则目标模式是 `join(h, requested)`；未持锁时就是请求模式。
4. 目标为 `IS` 或 `S` 时，每个真祖先必须持有至少 `IS`，五种模式都满足。
5. 目标为 `IX`、`SIX` 或 `X` 时，每个真祖先必须持有至少 `IX`，只有 `IX`、`SIX`、`X` 满足；`S` 不满足。
6. 从根向下找第一个不满足要求的祖先，返回可 `errors.Is(err, ErrMissingIntent)` 识别的错误，并用 `errors.As` 取出 `Ancestor` 与 `Required`。
7. 祖先通过后检查同节点其他事务；冲突错误可 `errors.Is(err, ErrConflict)`，并通过 `errors.As` 取出事务号最小的冲突持有者及其模式。
8. 被拒绝的请求不会写入任何持锁状态。

`Unlock` 只释放指定节点；若该事务仍在其真后代持锁则拒绝，以保留祖先意向不变量。`ReleaseAll` 在一个临界区内删除事务的全部锁，不暴露中间态。`Held` 返回 `(mode, held, err)`，`Holders` 返回按事务号升序排列的 `[]Holder`。

其他错误哨兵包括 `ErrInvalidTransaction`、`ErrInvalidMode`、`ErrInvalidNodeID`、`ErrNodeNotFound`、`ErrNodeExists`、`ErrParentNotFound`、`ErrLockNotHeld`、`ErrDescendantLocked`。

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

# 运行 2000 组随机操作与逐步朴素模型对拍，并打印输入、输出和判定依据
go test -run TestRandomSequencesAgainstNaiveModel -v

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
