# 布尔表达式中的运算符说明

目前库中定一个的表达式如下：
```
形式: field [exclude] operator value

eg:
    tag in [18]                 # 等于/不等于是in/not in 的一种特殊情况 
    tag not in [20, 21, 22]

    age between [18, 60]        # age between 18 and 60     [18, 60) 左闭右开
    age not between [18, 60]    # age [-*,18) 与 [60,+*)    不属于[18, 60)

    score gt 90                 # score > 90
    score not gt 90             # score <= 90

    score lt 90                 # score < 90
    score not lt 90             # score >= 90
```

简单来讲：operator 用于描述哪些“值”; `operator`用于描述属于这些"值"还是不属于这些"值";
`[exclude]` 则描述了布尔描述的“非”逻辑;

## be_indexer 实现说明

因为索引数据在很多场景下都需要特定的业务结合与合适的索引存储容器结合，eg: 地理位置索引需要geohash
相关的容器支持快速的检索查询; 一些容器的实现并不能支持所有的运算符类型，新版读写分离架构中内置了：

- 默认容器 (FlatDict + FlatPostingList)
因为内部是通过哈希字典 (FlatDict) 的索引存储结构， 所以只能支持可以转化为 In/NotIn 布尔表达; 对于 gt/lt 等运算符需要业务方将其预处理离散化为具体的 Feature 值再传入。

- 模式匹配容器 (ACMatcher)
用于支持模式匹配查询；用于内容关键词匹配逻辑（eg：找出所有文章中有：`kw in [比特币] 且 city in [US]`
的所有文章); 因为使用了双数组 Trie (DAT) 的 Aho-Corasick 算法实现零拷贝模式匹配， 所以也决定了它主要支持字符串字串的包含/排除逻辑。

