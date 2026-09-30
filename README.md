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

## 无等待原子快照（`snapshot` 包）

`snapshot` 提供 n 个单元（1 ≤ n ≤ 16）的单写者原子快照对象：
单元 i 仅允许写者 i 通过 `Update(i, i, v)` 更新，任意执行体可通过
`Scan()` 取得全部单元的整体快照。每次 `Scan` 的返回值都可线性化到
其调用区间内的某一时刻，即等于该时刻各单元的真实值向量。

### 算法与有界性

实现采用 Afek 等人的单写者原子快照算法：

- 每个单元发布不可变记录 `(值, 序号, 内嵌快照)`，写者更新前先自己做
  一遍完整扫描，把结果随新值一起原子发布（`atomic.Pointer` + CAS）。
- 扫描以「收集」（依次读全部单元）为基本步：
  - 若相邻两次收集完全一致（干净的双重收集），该结果在两次收集之间
    的任意时刻都成立，直接返回；
  - 否则，若某单元的序号相对首次收集前进了至少 2，说明该写者在本
    扫描期间完成了两次更新，其第二次更新内嵌的快照必然开始于本扫描
    开始之后、结束于当前之前，落在本扫描的调用区间内，可直接借用。
- 有界性：在不触发借用规则的前提下，每个单元的序号最多被观察到
  前进 1 次；n 个单元最多制造 n 次「不一致的相邻收集」，因此单次
  扫描的收集次数不超过 n+1，必然满足 2n+1 的上界，与写者是否持续
  更新无关（无等待）。

### 何时借用他人结果

仅当扫描观察到某单元 i 的序号 `seq >= 首次收集时的序号 + 2` 时，
才借用该单元当前记录中内嵌的快照作为本次扫描的返回值；其余情况
一律使用自己收集到的干净双重收集结果。借用是安全的，因为被借用的
快照对应的更新区间完全包含在本次扫描的调用区间之内。

### 只读统计

`MaxCollects()` 返回迄今为止所有 `Scan` 调用中单次收集次数的最大值，
测试用它断言上界 `2n+1`。

### 本地验证

```bash
# 功能 + 竞态检测 + 详细日志（日志含输入、输出与判定依据）
go test -race -v ./snapshot

# 单项验证
go test -race -run TestCollectBoundUnderChurn -v ./snapshot   # 持续更新下的收集次数上界
go test -race -run TestCausalChain -v ./snapshot              # 因果链
go test -race -run TestMonotonicity -v ./snapshot             # 单调性
go test -race -run TestConcurrentExplainability -v ./snapshot # 并发压力下逐快照可解释性校验
```
