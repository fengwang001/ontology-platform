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

## 邻键锁有序整数键集合（`nkl` 包）

`nkl` 实现基于邻键锁（next-key locking）的有序整数键集合，事务扫描范围时
同时锁住范围内的记录与其间的空隙，保证同一事务对同一范围的重复扫描不出现
幻读，且任何被拒绝的操作不留下任何锁。

### 锁模型

- **记录锁**：分共享（`Shared`）与排他（`Exclusive`），仅共享与共享兼容；
  事务自己的锁不与自己冲突，共享升级排他要求别的事务不持该键的锁。
- **空隙锁**：记为键值开区间 `(p, s)`，彼此永不冲突，端点在加锁时固定；
  不存在的前驱 / 后继分别取负无穷 / 正无穷。

### 各操作加锁规则

- `Scan(tx, lo, hi, mode)`：对闭区间 `[lo, hi]` 内每个现存键加 `mode` 记录锁，
  并对开区间 `(p, s)` 加空隙锁——`p` 是小于 `lo` 的最大现存键、`s` 是大于
  `hi` 的最小现存键（`s` 本身不加记录锁）。范围内任一键的记录锁与他人冲突
  则整个扫描失败、报持锁事务、不留任何锁。
- `Get(tx, k, mode)`：`k` 存在则只加 `mode` 记录锁；不存在则只对 `k` 所在的
  `(p, s)` 加空隙锁，`p`、`s` 为 `k` 的现存前驱与后继。
- `Insert(tx, k)`：`k` 已存在报「键已存在」；`k` 落在其他事务持有的任一空隙
  锁区间内报「间隙被占」并给出持锁事务；否则插入并令插入者持 `k` 的排他
  记录锁。
- `Commit(tx)`：释放该事务的全部锁。
- `Abort(tx)`：释放该事务的全部锁，并撤销它的全部插入。

### 空隙划分规则

现存键把键值域划分为若干开区间空隙：相邻现存键 `a < b` 之间是空隙 `(a, b)`，
最小键之下是 `(-∞, min)`，最大键之上是 `(max, +∞)`。扫描与未命中点读按上述
规则取覆盖目标范围 / 目标键的那一段空隙加锁；插入落入他事务已锁的任一空隙
即被拒绝，从而阻止幻读。

### 错误优先级

事务不存在 → 事务已提交 / 已中止 → 模式不是共享或排他 → 范围颠倒，
按此顺序只报第一个并整体拒绝；冲突失败同样不改变任何状态。

### 本地验证

```bash
# 若 GOCACHE 默认路径不可写，可先指定缓存目录
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

# 运行 nkl 包全部测试（含竞态检测）
go test -race ./nkl/

# 查看每个用例打印的输入、输出与判定依据
go test -race -v ./nkl/
```
