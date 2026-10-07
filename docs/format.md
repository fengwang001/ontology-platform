# 快照磁盘格式 v1

## 目录布局

```
<export-dir>/
├── manifest.json
├── chunk_person.json
├── chunk_org.json
└── ...
```

## manifest.json

```json
{
  "version": 1,
  "types": ["org", "person"]
}
```

- `types`：本次导出覆盖的全部对象类型；类型名排序后写入。请求未列出的类型按
  “范围外”处理（最高拒绝优先级）。

## chunk_<type>.json

```json
{
  "type": "person",
  "version": 1,
  "declared_count": 2,
  "payload_hex": "5b0a7b...",
  "payload_sha256_hex": "c02d30f6..."
}
```

- `type`：必须与文件名中的类型一致，否则按完整性失败处理。
- `declared_count`：块头声明的记录条数；与实际解析条数不符 → 整块数量不一致。
- `payload_hex`：规范 JSON 记录数组的十六进制编码。记录结构：

```json
{
  "id": "p1",
  "props": {"name": "Ada"},
  "links": [
    {"field": "works_for", "target_type": "org", "id": "o1"}
  ]
}
```

- `payload_sha256_hex`：`SHA-256(decode_hex(payload_hex))` 的十六进制摘要。
  认证域仅为载荷字节本身；hex 解码失败或摘要不符都判定为块完整性失败。

## 校验语义摘要

| 现象 | 结论 |
|---|---|
| 文件缺失/信封损坏/类型名不符 | 块完整性失败 |
| hex 不可解码 / SHA-256 不符 / 载荷非记录数组 | 块完整性失败 |
| 摘要匹配但解析条数 ≠ 声明条数 | 数量不一致（整块不可信，记录全部不采信） |
| 引用指向未覆盖的类型 | 悬空引用 |
| 引用指向的块不可信 | 因目标块不可信而无法校验（保守结论） |
| 可信目标块中不存在该 id | 悬空引用 |
| 可信目标块中存在该 id | 可解析 |

所有判定仅依赖文件字节；重复只读校验得到相同结论。
