# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 可撤回前 K 名维护器（`topk` 包）

`topk.Maintainer` 维护当前分数最高的若干元素，支持新增覆盖、撤回补位，
读取路径并发安全。

### 排序键与并列次级规则

名次由两级排序键唯一定义，不存在名次不确定的情况：

1. 主键：`Score` **降序**（分数越高越靠前）。
2. 次级键（并列时）：`ID` 按 **Go 字符串字典序（UTF-8 字节序）升序**。

因此 `TopK()` 与 `Ordered()` 的结果在任意状态下都可复现；对任意更小的
K，前 K 名都是 `Ordered()` 完整有序序列的严格前缀。元素不足 K 个时
`TopK()` 返回全部。

### 语义

- `New(k, capacity)`：K 必须为正，容量必须 `>= K`。
- `Add(id, score)`：标识已存在时为**覆盖式更新**（改分后名次立即重排）；
  标识不存在且已达容量上限时被拒绝。
- `Delete(id)`：撤回元素，门槛外分数最高者在下次读取时自动补位；
  删除不存在或空标识是**幂等空操作**。
- 非法请求在修改状态前完成校验并整体拒绝，返回可 `errors.Is` 区分的错误：
  `ErrInvalidK`、`ErrInvalidCapacity`、`ErrEmptyID`、`ErrCapacityExceeded`；
  一次失败不改变任何状态。
- `TopK()` / `Ordered()` / `Count()` 使用读写锁的读锁，可并发调用；
  返回的是副本，调用方修改不影响内部状态。

### 本地验证：与朴素全量排序核对

正确性以"朴素全量排序"为基准交叉核对：把所有元素拷出，用同一排序规则
（分数降序、ID 升序）整体 `sort`，再截取前 K，应与维护器结果逐元素一致。
测试中的 `naiveTopK` 即该朴素实现，`TestPrefixAndNaiveCrossCheck` 会对
完整序列及每个更小 K 的前缀逐一比对：

```bash
# 带竞态检测运行（含并发读一致性与读写竞态用例，-v 可查看操作/序列/判定依据日志）
go test -race -v ./topk

# 多次重复以降低并发用例的偶发性
go test -race -count=10 ./topk
```

也可手动核对：调用 `m.Ordered()` 得到完整有序序列，确认 `m.TopK()`
等于其前 K 个元素即可。

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
