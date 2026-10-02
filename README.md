# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [`rwlock/`](rwlock/README.md)：ZooKeeper 式顺序临时节点读写锁协调模型。
  序号只增不复用，读者/写者按统一规则持有或登记一次性观察；删除按观察登记
  序号（ws）升序通知并重新评估，会话过期先撤销观察再按创建 zxid 升序删除。
  并发调用等价于某个串行顺序，相同操作序列重放结果完全一致；含 2000 组随机
  序列对朴素模型的差分测试，以及通知数恒等于观察者数、树访问 O(log n) 的
  定标测试。详见 [`rwlock/README.md`](rwlock/README.md)。

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

顺序临时节点读写锁的差分日志（含输入、输出与判定依据）：

```bash
RWLOCK_DIFF_LOG=/tmp/rwlock_diff.log go test ./rwlock/ -run TestRandomDifferential2000 -v
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
