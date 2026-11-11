# be_indexer static site

面向 [be_indexer](../README.md) 的静态宣传 + 技术深潜站。纯 HTML / CSS / 原生 JS，零运行时依赖、零构建步骤，可直接部署到 **Cloudflare Pages** 或 **Cloudflare Workers**（Static Assets）。

## 页面

| 路径 | 内容 |
|---|---|
| `public/index.html` | 首页：定位、设计取舍、系统架构、性能摘要、快速开始 |
| `public/architecture.html` | 架构：EntryID 位编码、K-Groups 归并、Segment v5、wildcard 零拷贝、full + delta |
| `public/containers.html` | 索引容器：FlatDict、MPH、FST、AC、ext_range、proximitygeo |
| `public/topk.html` | 精确 Top K：分数 sidecar、块上界、剪枝、延迟建堆、缺失分数 |
| `public/performance.html` | 性能数据：内存、SkipTo、wildcard、容器查找、Top K、延迟建堆、复现命令 |
| `public/start.html` | 快速上手：Schema、构建、查询、full + delta、发布、Holder、Top K |

交互组件均在 `public/assets/site.js`：

- K-Groups pivot / SkipTo 单步演示与自动播放
- EntryID 64 位交互式位布局（K / index / sign / DocID / include）
- ext_range 区间 stabbing 演示
- Aho-Corasick 状态扫描演示（`ushers`）
- 滚动进度、TOC 高亮、代码复制、移动端导航

## 技术选择

没有引入 Vue / React 或构建工具：站点以内容为主，交互组件规模可控，原生 JS 足够；换来零依赖、零构建、直接静态部署，Cloudflare 侧也只需要托管 `public/`。如果后续要扩展成多作者内容站或接入 CMS，可以再迁移到 Astro / Vue / React。

## 本地预览

```bash
cd site
python3 -m http.server 8788 --directory public
# 打开 http://localhost:8788/
```

或使用任意静态文件服务器。没有 `npm install`，也没有构建产物。

## 部署到 Cloudflare Pages

### 方式一：Git 集成（推荐）

1. 把仓库推到 GitHub。
2. Cloudflare Dashboard → Workers & Pages → Create → Pages → Connect to Git。
3. 选择仓库，配置：
   - **Root directory**: `site`
   - **Build command**: 留空
   - **Build output directory**: `public`
4. Save and Deploy。

### 方式二：Direct Upload

```bash
cd site
npx wrangler pages deploy public --project-name be-indexer-site
```

首次会要求登录 Cloudflare 并创建项目。

## 部署到 Cloudflare Workers（Static Assets）

`wrangler.jsonc` 已配置 `assets.directory = "./public"`，不需要 Worker 脚本。

```bash
cd site
npx wrangler deploy
```

本地模拟 Workers 运行时：

```bash
cd site
npx wrangler dev
```

## 配置说明

- `public/_headers`：安全头、CSP 与静态资源缓存。CSP 允许 `style-src 'unsafe-inline'`，因为页面使用少量内联 style 属性；脚本全部来自同源 `assets/site.js`，没有内联脚本。
- `wrangler.jsonc`：`html_handling: auto-trailing-slash`、`not_found_handling: 404-page`。
- `public/og.png`：1200×630 社交分享图。
- `public/favicon.svg`：站点图标。
- `public/robots.txt`：允许全站抓取。

## 内容来源与口径

文案与数据来自仓库中的 `README.md`、`docs/architecture/`、`docs/architecture/indexes/`、`docs/design/topk.md`、`docs/guide/` 与 `static/design/`。

性能数字均标注了机器、日期或“历史/合成”属性。它们是机制解释与相对参考，不是线上 SLA；部署选型应以真实业务分布、P99、峰值 RSS、冷启动预算为准。

## 修改指南

- 设计变量集中在 `public/assets/site.css` 顶部的 `:root`。
- 新增页面时复制现有页面的 header / footer，并把导航中的当前页设为 `aria-current="page"`。
- 交互组件通过 `data-*` 属性初始化；不存在对应节点时会自动跳过，不会报错。
- 部署前建议检查所有相对链接：

```bash
cd site/public
python3 - <<'PY'
import re, pathlib
for f in pathlib.Path('.').rglob('*.html'):
    for href in re.findall(r'(?:href|src)="([^"#]+)', f.read_text()):
        if href.startswith(('http', 'mailto:', 'data:')) or href.startswith('//'):
            continue
        target = (f.parent / href).resolve()
        if not target.exists():
            print('MISSING', f, href)
PY
```
