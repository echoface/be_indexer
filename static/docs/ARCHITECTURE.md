# 架构设计文档

本文档详细介绍了be_indexer的架构设计、核心算法和实现细节。

## 目录

1. [整体架构](#整体架构)
2. [核心算法](#核心算法)
3. [索引实现](#索引实现)
4. [容器系统](#容器系统)
5. [解析器系统](#解析器系统)
6. [Roaringidx实现](#roaringidx实现)
7. [性能优化策略](#性能优化策略)

---

## 整体架构

### 组件概览

```
┌─────────────────────────────────────────────────────────┐
│                     应用层 (Application)                   │
├─────────────────────────────────────────────────────────┤
│  Document → Conjunction → BooleanExpr → BoolValues    │
├─────────────────────────────────────────────────────────┤
│                  索引构建层 (Builder Layer)                 │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │ IndexerBuilder│  │ FieldConfig │  │   Parser     │   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
├─────────────────────────────────────────────────────────┤
│                  容器层 (Container Layer)                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │   Default    │  │ AC Matcher   │  │Range Holder  │   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
├─────────────────────────────────────────────────────────┤
│                  存储层 (Storage Layer)                    │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │ Posting List │  │  Bitmap      │  │   Trie       │   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
├─────────────────────────────────────────────────────────┤
│                  检索层 (Retrieval Layer)                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │    Scanner   │  │   Cursor     │  │  Collector   │   │
│  └──────────────┘  └──────────────┘  └──────────────┘   │
└─────────────────────────────────────────────────────────┘
```

### 数据流

#### 索引构建流程

```
1. Document
   ↓
2. 解析Conjunction
   ↓
3. 为每个BooleanExpr分配EntryID
   ↓
4. 通过Parser将值转换为ID
   ↓
5. 将EntryID添加到对应的Container
   ↓
6. 构建索引结构
```

#### 检索流程

```
1. Assignments (查询条件)
   ↓
2. 解析查询值并转换为ID
   ↓
3. 从容器中获取匹配的EntryID列表
   ↓
4. 使用多路归并算法合并结果
   ↓
5. 过滤Exclude条件
   ↓
6. 返回匹配的Document ID列表
```

---

## 核心算法

### DNF (Disjunctive Normal Form) 布尔表达式

be_indexer基于DNF布尔表达式，即多个Conjunction的OR组合：

```
(DNF) = C1 OR C2 OR C3 OR ...
其中 Ci = E1 AND E2 AND E3 AND ...
```

**示例：**
```
(age IN [18,25] AND city IN [beijing]) OR (vip = true)

Document 1: (age:20 AND city:beijing)  → 匹配
Document 2: (vip:true)                  → 匹配
Document 3: (age:30 AND city:shanghai)  → 不匹配
```

### EntryID编码

EntryID采用60位编码，包含以下信息：

```
|--- ConjID (60bit) ---| empty(3bit) | incl(1bit) |
| size(8) | index(8) | negSign(1) | docID(43) | 000 | I/E |
```

- **ConjID (60bit)**: 连接标识符
  - size (8bit): Conjunction中Include表达式的数量
  - index (8bit): Document中的Conjunction索引
  - negSign (1bit): 文档ID符号位
  - docID (43bit): 文档ID值（[-2^43, 2^43]）
- **empty (3bit)**: 保留位，固定为0
- **incl (1bit)**: Include/Exclude标志位

### 多路归并检索算法

```
输入: 多个已排序的EntryID列表
输出: 匹配的Document ID集合

1. 将每个字段的EntryID列表转换为Cursor
2. 按EntryID排序所有Cursor
3. 找到最小的EntryID (minEID)
4. 检查所有Cursor是否都包含minEID
5. 如果是：
   a. 检查minEID是否为Include类型
   b. 如果是，添加对应的Document ID到结果集
   c. 所有Cursor前进到minEID的下一个位置
6. 如果否：
   a. 找到包含minEID的Cursor
   b. 该Cursor前进到minEID的下一个位置
7. 重复步骤3-6，直到所有Cursor到达末尾
8. 过滤Exclude类型的EntryID
```

**时间复杂度：** O(N log M)，其中N是总EntryID数量，M是字段数量

### SkipTo优化

为了加速Cursor移动，提供了两种SkipTo算法：

1. **线性Skip**：适用于小范围跳跃
```go
func (ec *EntriesCursor) linearSkipTo(id EntryID) EntryID {
    for ec.cursor < ec.idSize && ec.entries[ec.cursor] < id {
        ec.cursor++
    }
    return ec.curEID
}
```

2. **指数+二分搜索**：适用于大范围跳跃
```go
func (ec *EntriesCursor) SkipTo(id EntryID) EntryID {
    // 指数搜索确定范围
    bound := 1
    oc := ec.cursor
    rightSideIndex := oc + bound
    for rightSideIndex < ec.idSize && ec.entries[rightSideIndex] < id {
        ec.cursor = rightSideIndex
        bound = bound << 1
        rightSideIndex = oc + bound
    }
    // 二分搜索精确定位
    for ec.cursor < rightSideIndex && ec.entries[ec.cursor] < id {
        bound = (ec.cursor + rightSideIndex) >> 1
        if ec.entries[bound] >= id {
            rightSideIndex = bound
        } else {
            ec.cursor = bound + 1
        }
    }
    return ec.curEID
}
```

---

## 索引实现

### Indexer类型

#### 1. KGroups BE Index（默认）

**文件：** `be_indexer_kgroups.go`

**特点：**
- 基于论文实现的布尔表达式索引
- 使用分组技术优化内存使用
- 支持高效的DNF查询

**核心结构：**
```go
type KGroupsBEIndex struct {
    indexBase

    groups []KGroup
    // ...
}

type KGroup struct {
    size        int
    includeData map[EntryID]Entries
    excludeData map[EntryID]Entries
}
```

#### 2. Compacted BE Index（紧凑型）

**文件：** `be_indexer_compact.go`

**特点：**
- 内存使用更紧凑
- 性能比默认实现提升约12%
- 适用于对内存敏感的场景

**优化点：**
- 压缩EntryID存储
- 优化数据结构布局
- 减少内存碎片

### 索引编译

```go
func (bi *indexBase) compileIndexer() error {
    // 1. 验证字段配置
    // 2. 优化容器结构
    // 3. 构建倒排索引
    // 4. 压缩数据结构
    // 5. 计算统计信息
}
```

---

## 容器系统

### EntriesHolder接口

```go
type EntriesHolder interface {
    Name() string
    CreateHolder(desc *FieldDesc) EntriesHolder
    IndexingBETx(desc *FieldDesc, expr *BoolValues) (TxData, error)
    DecodeTxData(data []byte) (TxData, error)
}
```

### 容器类型

#### 1. DefaultEntriesHolder（默认容器）

**用途：** 通用场景，适用于大多数字符串和数值匹配

**实现：**
- 使用HashMap存储EntryID列表
- O(1)查询时间复杂度
- 支持Include和Exclude

**数据结构：**
```go
type DefaultEntriesHolder struct {
    field *FieldDesc
    data  map[uint64]Entries  // valueID -> EntryID列表
}
```

#### 2. AhoCorasickMatcherHolder（AC自动机）

**用途：** 字符串模式匹配，适用于关键词、广告匹配等场景

**特点：**
- 基于Aho-Corasick算法
- 支持多模式同时匹配
- 基于双数组Trie树实现，性能优异

**实现细节：**
```go
type AhoCorasickMatcherHolder struct {
    ac *ac_automaton.AcAutomaton
    // ...
}
```

**匹配示例：**
```
文本: "advertisement promotion"
模式: ["ad", "promo"]

结果: "advertisement" → 匹配 "ad"
       "promotion"    → 匹配 "promo"
```

**使用场景：**
- 关键词广告匹配
- 内容标签匹配
- 敏感词过滤

#### 3. ExtendRangeHolder（扩展范围容器）

**用途：** 范围查询优化，适用于数值范围匹配

**优化点：**
- 针对 >, <, between 操作符优化
- 减少存储空间
- 加速范围查询

### 容器注册机制

```go
// 注册自定义容器
be_indexer.RegisterEntriesHolder("my_custom_holder", func() be_indexer.EntriesHolder {
    return NewMyCustomHolder()
})

// 使用自定义容器
builder.ConfigField("my_field", be_indexer.FieldOption{
    Container: "my_custom_holder",
})
```

---

## 解析器系统

### FieldValueParser接口

```go
type FieldValueParser interface {
    Name() string
    ParseAssign(v interface{}) ([]uint64, error)  // 解析查询值
    ParseValue(v interface{}) ([]uint64, error)   // 解析文档值
}
```

### 解析器类型

#### 1. CommonParser（通用解析器）

**功能：** 支持字符串和数值的通用解析

**类型支持：**
- 整数：int, int8, int16, int32, int64
- 无符号整数：uint, uint8, uint16, uint32, uint64
- 浮点数：float32, float64
- 字符串：string
- JSON Number

**转换流程：**
```
输入值 → 类型检查 → 格式转换 → 哈希/编码 → uint64 ID
```

#### 2. HashStrParser（哈希字符串解析器）

**特点：**
- 使用哈希将字符串转换为数值ID
- 适用于大量字符串值的场景
- 节省内存

**注意事项：**
- 哈希冲突概率极低，但理论上存在
- 不支持字符串范围查询

#### 3. NumberParser（数值解析器）

**特点：**
- 专门处理数值类型
- 支持数值范围查询
- 自动类型转换

#### 4. RangeParser（范围解析器）

**用途：** 专门处理范围查询

**支持操作：**
- 大于 (GT)
- 小于 (LT)
- 介于 (Between)

#### 5. GeoHashParser（地理哈希解析器）

**用途：** 地理信息索引

**特性：**
- 支持精度控制
- 基于GeoHash算法
- 适用于地理位置查询

**示例：**
```
精度5: 约5km精度
精度6: 约1.2km精度
精度7: 约152m精度
```

---

## Roaringidx实现

### 架构设计

roaringidx是基于Roaring Bitmap的倒排索引实现，适用于大规模文档检索。

**核心优势：**
- 内存占用低
- 集合运算速度快
- 适用于稀疏数据

### Roaring Bitmap

Roaring Bitmap是一种高效的位图压缩格式：

- **小整数（< 4096）**：使用数组存储
- **中等整数（4096-2^16）**：使用16位位图
- **大整数（> 2^16）**：使用32位位图

**性能对比：**
```
数据集: 1000万个文档，10个字段
普通倒排索引: ~500MB
Roaring Bitmap: ~100MB
检索速度: 提升30-50%
```

### 倒排索引结构

```go
type IvtBEIndexer struct {
    data map[be_indexer.BEField]BEContainer
    docMaxConjSize int
}

type BEContainer interface {
    Add(docID be_indexer.DocID, valueIDs []uint64, include bool) error
    Query(valueIDs []uint64, include bool) (*roaring.Bitmap, error)
    Compile() error
}
```

### 容器类型

#### 1. DefaultContainer（默认容器）

```go
type DefaultContainer struct {
    includeBitmap map[uint64]*roaring.Bitmap
    excludeBitmap map[uint64]*roaring.Bitmap
}
```

#### 2. ACContainer（AC自动机容器）

```go
type ACContainer struct {
    patterns map[uint64][]string  // valueID -> pattern列表
    ac       *ac_automaton.AcAutomaton
    // ...
}
```

#### 3. RangeContainer（范围容器）

```go
type RangeContainer struct {
    ranges map[ValueOpt]map[uint64]*roaring.Bitmap
    // ...
}
```

### 检索流程

```
1. 解析Assignments，获取查询值和对应的容器
2. 对每个字段，获取匹配的EntryID列表（Bitmap）
3. 对Include条件，使用AND操作合并所有字段的Bitmap
4. 对Exclude条件，从结果中减去对应的Bitmap
5. 返回最终的Document ID列表
```

**集合运算示例：**
```
age IN [20,30]: Bitmap_A = {1, 5, 10, 15, 20}
city IN [beijing]: Bitmap_B = {1, 10, 25, 30}

结果 = Bitmap_A AND Bitmap_B = {1, 10}
```

---

## 性能优化策略

### 1. 数据结构优化

#### 内存对齐
```go
// 优化前
type Entry struct {
    DocID int64
    Incl  bool
    // 可能存在内存对齐空洞
}

// 优化后
type Entry struct {
    EID EntryID  // 已编码的数据，包含DocID和Incl信息
}
```

#### 紧凑编码
```go
// 使用位操作编码多个字段
func NewEntryID(id ConjID, incl bool) EntryID {
    if !incl {
        return EntryID(id << 4)  // 使用低位存储标志位
    }
    return EntryID((id << 4) | 0x01)
}
```

### 2. 算法优化

#### Cursor排序优化
```go
// 使用插入排序代替快速排序（数据量小）
func (s FieldCursors) Sort() {
    for i := 1; i < x; i++ {
        for j := i; j > 0 && s[j].GetCurEntryID() < s[j-1].GetCurEntryID(); j-- {
            s[j], s[j-1] = s[j-1], s[j]
        }
    }
}
```

#### 缓存优化
```go
// 复用对象减少GC压力
var collectorPool = sync.Pool{
    New: func() interface{} {
        return NewDocIDCollector()
    },
}
```

### 3. I/O优化

#### 批量处理
```go
// 批量添加文档
builder.AddDocument(docs...)  // 一次性处理多个文档

// 而不是
for _, doc := range docs {
    builder.AddDocument(doc)  // 逐个处理（性能差）
}
```

#### 延迟计算
```go
// 索引编译阶段进行优化
func (bi *indexBase) compileIndexer() error {
    // 1. 合并相似项
    // 2. 去除冗余数据
    // 3. 压缩存储
}
```

### 4. 内存优化

#### 对象池
```go
func PickCollector() *DocIDCollector {
    return collectorPool.Get().(*DocIDCollector)
}

func PutCollector(c *DocIDCollector) {
    c.Reset()
    collectorPool.Put(c)
}
```

#### 预分配容量
```go
// 预分配切片容量
entries := make(Entries, 0, expectedSize)
```

---

## 总结

be_indexer的架构设计充分体现了高性能布尔表达式索引的要求：

1. **清晰的层次结构**：从文档到检索的完整链路
2. **灵活的容器系统**：支持多种数据类型的优化存储
3. **高效的检索算法**：多路归并+SkipTo优化
4. **可扩展的设计**：支持自定义容器和解析器
5. **多种实现**：满足不同场景的性能和内存需求

这种设计使得be_indexer能够广泛应用于广告、电商、内容推荐等需要高效规则匹配的场景。
