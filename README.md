# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## pagetable：三级基数页表映射器

`pagetable` 包在 512 个虚拟页（vpn 0–511）的地址空间上实现三级基数页表，物理页号 pfn 取 0 到 2^20−1。

### 布局与叶子语义

- 根表 8 槽，每槽覆盖 64 页；L1 表 8 槽，每槽覆盖 8 页；L0 表 8 槽，每槽覆盖 1 页。
- 根槽、L1 槽、L0 槽都可以是叶子（分别映射 64、8、1 页），非叶槽指向下一级表，空槽表示未映射。
- 每个叶子带可写位 W 与共享的访问位 A、脏位 D（初值 0，一个叶子只有一组 A/D，大页整体共用）。
- 结构由叶子集合唯一决定：除根表外，所有槽都为空的表立即回收并向上级联；任意时刻 `Tables()`（U）等于由叶子集合推出的表数，`Mapped()` 等于全部叶子大小之和。

### 劈分与合并的 A/D 规则

- 部分相交的叶子先劈成 8 个下一层叶子：第 i 个子叶子 pfn 为原 pfn 加 i 乘子叶子大小，W、A、D 原样继承；劈分不改变任何页的 W。
- `Promote` 把 8 个全是叶子（大小为 size/8）、pfn 连续且起始 pfn 按 size 对齐、W 全相同的子槽合并为一个叶子；新叶子的 A、D 分别为 8 个子叶子 A、D 的逻辑或，W 不变，旧表回收。
- 劈分后再合并不会恢复原来的 A/D（合并取的是当前子叶子的或）。

### 峰值配额校验

- 构造参数 P（1 到 10^6，不含根表）为表页配额，越界整体拒绝。
- `Map` 按路径上缺失的表数预检；`Unmap` 先规划出劈分所需新表数 s，s>0 时必须满足 U+s ≤ P 才执行——同一次操作随后才回收的旧表不得抵扣峰值。被拒绝的操作不改变任何叶子、A/D 位、表、U 与 Epoch。

### 复杂度计数器

- `Translate` 至多考察 3 个槽；`Promote` 至多考察 8 个槽；`Unmap` 只考察与区间相交的槽，任意区间不超过 40 个，被完整覆盖的表或叶子整体释放不逐槽遍历（满载映像上 `Unmap(0,512)` 不超过 8 个）。均以包内非导出计数器在测试中验证。

### 本地验证

```bash
go test ./pagetable/          # 确定性用例 + 2000 组随机对照（对照 512 项逐页朴素模拟）
go test -race ./pagetable/    # 并发与一致性快照检查
go test -run TestWorkedExample -v ./pagetable/   # 规格中的两个范例
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
