# logkv

只追加日志结构键值引擎，含启动恢复与段合并服务。设计取舍见
[DESIGN.md](DESIGN.md)。

## 用法

```go
s, err := logkv.Open(logkv.Config{
    Dir:             "/var/lib/mydb",
    MaxSegmentBytes: 64 << 20, // 活动段超过即封口并新建
    WriteHints:      true,     // 封口时生成提示信息，加速下次恢复
})
if err != nil { /* *logkv.Error，用 logkv.IsKind 判定类别 */ }
defer s.Close()

err = s.Put([]byte("k"), []byte("v"))
err = s.Delete([]byte("k"))

val, status, err := s.Get([]byte("k"))
// status: logkv.StatusFound / StatusDeleted / StatusNotFound

err = s.Merge([]uint32{1, 2, 3}) // 合并若干已封口段

st := s.Stats() // TruncatedBytes / Distortions / SelfHeals / RecordReads ...
```

## 错误类别

`Open/Get/Put/Delete/Merge` 返回的错误均为 `*logkv.Error`：

| 类别 | 含义 |
| --- | --- |
| `KindInvalidArgument` | 参数非法（空键、非法配置、合并集合重复等） |
| `KindSegmentCorruption` | 段损坏，含段号与首个损坏位置 |
| `KindDirectoryDistortion` | 目录失真（触发自愈，经 `Stats.Distortions` 报告） |
| `KindSegmentNotFound` | 合并指定的段不存在 |
| `KindActiveSegmentNotMergeable` | 活动段不可合并 |

同一时刻只报告次序最靠前的一类（即上表顺序）。

## 测试

```bash
go test ./logkv         # 全量
go test ./logkv -race   # 竞态
```
