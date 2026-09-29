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

## topk：可撤回的前 K 名维护器

位于 `topk` 包（`topk/maintainer.go`），维护一个有界元素集合中分数最高的 K 个元素。

### 排序键与并列规则

每个元素按复合键排定唯一名次：

```
(score DESC, id ASC)
```

- 分数高者在前；分数相同时，标识字典序较小者在前。
- 标识唯一，因此不存在无法区分的名次，任何输入下结果都确定且可复现。
- 门槛处（第 K 名与第 K+1 名）分数相同，取并列组里字典序最小的若干个标识。
- 内部始终按该键维护全部活跃元素的规范有序序列；`TopK(n)` 就是该序列长度为 n 的前缀，所以任意更小的 n 取到的都是同一前缀。撤回门槛内元素后，门槛外分数最高（同分则 id 最小）的活跃元素立即补位；撤回门槛外元素不影响可见结果，同时释放一个容量名额。

操作语义：

- `Upsert(id, score)`：覆盖式更新，已存在的 id 仅替换分数并重排；满容量时覆盖已有 id 仍允许。
- `Withdraw(id)`：幂等删除，未知 id 与空 id 均为空操作。
- `TopK(n)` / `Count()` / `Snapshot()`：只读并发安全，返回独立副本；不足 n 个时返回全部。

可区分的失败原因（哨兵错误，失败时不改变任何状态）：

- `ErrNonPositiveK`：`New`/`TopK` 收到非正 K；`ErrKExceedsCap`：容量小于 K。
- `ErrKExceedsLimit`：`TopK(n)` 的 n 超过配置的 K。
- `ErrEmptyID`：空标识；`ErrInvalidScore`：NaN 或无穷大分数。
- `ErrCapacityFull`：容量已满且 id 为新元素。

### 本地验证（朴素全量排序核对）

测试里的 `naiveSort` 是参考实现：把元素放入 map 后用同一排序键做一次朴素全量排序，再与维护器结果逐元素比对。

```bash
# 竞态检测 + 详细日志（打印每次操作、当前有序序列与判定依据）
go test -race -v ./topk

# 覆盖率
go test -cover ./topk
```

关键核对点：

- `TestRandomizedAgainstNaive`：300 步确定性随机增删，每步都与朴素全量排序结果及所有 `TopK(n)` 前缀逐元素核对。
- `TestThresholdTieKeepsSmallerID`：门槛并列时按 id 升序取位，撤回后门外最优元素补位。
- `TestConcurrentReadsAreElementwiseIdentical`：16 个并发读者的结果逐元素相同（配合 `-race` 运行）。
