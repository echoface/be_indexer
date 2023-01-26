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
相关的容器支持快速的检索查询; 一些容器的实现并不能支持所有的运算符类型，库中内置了：

- 默认容器(default)
因为内部是通过hashmap的索引存储结构， 所以只能支持可以转化为in/not in布尔表达; 而对于gt/lt 等运算符
则没有办法表达（eg: >90 hashmap无法枚举出所有值并存储对应的entries列表) 

- 模式匹配容器(ac_matcher)
用于支持模式匹配查询；用于内容关键词匹配逻辑（eg：找出所有文章中有：`kw in [比特币] 且 city in [US]`
的所有文章); 因为使用了aho-corasick算法实现模式匹配， 所以也决定了它只能支持: in/not in布尔表达

- 默认容器扩展范围表达容器(ext_range)
在默认容器的基础上；扩展支持范围布尔表达，从而支持`lt/gt/in` 等运算符; 对应范围查询部分的复杂度:O(log2N)
和默认容器的区别在于: 因为范围查询需要数值表达, 所以此容器相对于默认容器只能支持数值；而不能支持:
`tag in ["字符串"]`这样的表达，因为"字符串"无法parse成有意义的数值

