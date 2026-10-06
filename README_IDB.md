# 浏览器内嵌数据库事务作用域调度内核（`idb`）

`idb` 包是一个可精确复现的事务内核，对标 IndexedDB 的核心语义，由五部分协作：

- **数据库版本**：名字 + 整数版本；`Open(name, version)` 按版本高低直开、拒绝或
  触发版本变更；`DeleteDatabase` 删除后版本视为零。
- **对象仓库集合**：键为非空字节串、仓库内唯一；仅添加模式键冲突；仓库的
  创建/删除只允许在版本变更事务内进行。
- **事务作用域**：非空仓库名集合；模式为只读/读写/版本变更；越界操作被拒绝。
- **调度队列**：按创建序考虑，只看“重叠 + 创建序”决定不得越过；只读重叠可并行，
  其余重叠串行，不重叠恒可并行，版本变更独占。
- **连接生命周期**：版本变更/删除要求独占，先通知既有连接；连接关闭取消未开始
  事务、放行已开始事务，关闭期间拒绝新建事务。

## 关键语义

- 只读事务在开始那一刻确定快照，期间不受后续提交影响。
- 读写事务读到自己未提交的写；提交时写入一次性可见，中止时全部丢弃。
- 事务无未完成请求时自动提交（与显式提交等价）；版本变更事务显式
  `Commit`/`Abort`。请求失败默认中止事务，`WithIgnorable()` 可抑制。
- 八类错误可区分，拒绝次序固定：参数非法 → 版本过低 → 状态不允许 →
  仓库不存在 → 作用域不符 → 事务已结束 → 键冲突；被拒操作不改变任何状态。

## 使用

```go
k := idb.New(idb.WithLogger(log.New(os.Stderr, "", log.LstdFlags)))
defer k.Close()

c, _ := k.Open("app", 1)
vt := c.VersionChange()
_ = vt.Wait()
_ = c.CreateObjectStore("users")
_ = vt.Commit()

// 同步风格（内部用 Hold/Unhold 形成同步请求窗口）
err := idb.WithTx(c, idb.ReadWrite, []string{"users"}, func(tx *idb.Transaction) error {
	return idb.SyncPut(tx, "users", "u1", "alice", false)
})
```

## 验证

```bash
go test ./idb                                   # 覆盖面用例 + 朴素模型对照
go test -race -count=10 ./idb                  # 并发竞态与稳定性
go test -bench "IndependentOf" -run '^$' ./idb # 两条性能性质
```

- 覆盖面：三种版本关系、升级阻塞与解除、通知后排队、并行/串行/不越过、
  快照隔离、自动提交、可忽略错误、关闭取消、升级中止恢复、键冲突、删除归零。
- `TestDifferentialAgainstNaiveModel`：与独立编写的顺序化朴素模型对 400 组随机
  操作序列逐读结果、逐错误类别、逐最终键值比对。
- 性能：准入只扫描未结束事务（`TestAdmissionDoesNotScanFinished` 结构性断言）；
  提交只追加本事务写过的键（MVCC 版本链），均不随历史事务数/无关键数增长。
- 日志：`WithLogger` 打印每次输入、输出与调度判定依据，`go test -v` 可见。

设计取舍、被放弃方案与可验证性见 `DESIGN.md`。
