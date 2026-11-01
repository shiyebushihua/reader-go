# 阅间 · Reader Go

[English](README.md) | 简体中文

基于 Go + Vue 3 + TypeScript 开发的现代自托管在线阅读服务，当前稳定版本 **v0.0.25**。Docker 从源码多阶段构建前端并嵌入单一 Go 二进制程序中，运行时无任何 JVM 或 Node.js 运行时依赖，开箱即用，轻量高效。

---

## 核心功能特性

### 1. 现代化沉浸式阅读体验
- **精细排版定制**：提供多套精选阅读主题（日间亮白、雅致羊皮纸、柔和绿意、深邃夜间等），支持字体库切换、字号无级缩放、行高与页面边距精细微调。
- **双模翻页交互**：支持左右平铺滑动翻页（适配移动端单手操作）与上下无缝滚动阅读。
- **纯前端语音朗读 (TTS)**：基于浏览器 Web Speech API 原生实现，支持系统语音库自由选择、语速/语调动态调节、自动连续朗读与后台播放控制，无需额外付费接口。
- **正文净化排版**：内置文本净化规则引擎与段落格式化算法，支持自定义正则表达式实时过滤干扰广告与乱序冗余文本。
- **简繁体中文自由转换**：支持原文、简体、繁体无缝转换，满足不同阅读习惯。
- **章节自动分页补全**：当章节正文存在多页分页时，后台引擎自动探测并完整拼接所有子页，呈现连贯的一体化阅读内容。
- **双级高速缓存体系**：服务端磁盘章节缓存（`cache/books/...`）与客户端浏览器原生 IndexedDB 离线缓存相结合，断网亦可畅读。
- **一键快捷换源**：在阅读界面随时呼出换源抽屉，毫秒级检测多源更新状态，保留阅读进度无感切换。

### 2. 高性能规则解析引擎
- **多语法全能解析**：全面支持 CSS 复合选择器、XPath 1.0 路径匹配、JSONPath 提取以及多段正则表达式清洗管道。
- **嵌入式 JavaScript 脚本沙箱**：内置安全隔离的 JavaScript 执行环境，原生支持自定义脚本计算、加解密逻辑与复杂动态内容提取。
- **编码与网络自适应**：原生支持 GET / POST 请求、自定义 Headers、GBK / UTF-8 等字符编码自动探测与转换。
- **多用户独立书源管理**：每位用户拥有专属的书源列表空间，支持本地/网络 JSON 规则一键批量导入、启用禁用与独立维护。
- **SSE 流式并发搜书**：基于 Server-Sent Events 实现异步并行多源检索，首批结果即时推送，告别长时间空白等待。

### 3. 极速平稳的前端交互
- **零白屏书架即时渲染**：采用模块级内存缓存策略与静默后台更新机制，从阅读器或详情页返回书架时 0 延迟呈现，彻底消除白屏与骨架屏闪烁。
- **搜索状态持久保持**：引入组件状态持久化保持机制，在书架、搜索与管理面板之间自由切换，保留搜索词、筛选条件与当前浏览位置。
- **移动端优先设计**：遵循现代移动交互规范，界面清爽自然，无多余装饰，适配从智能手机、平板到桌面宽屏的全设备视口。

### 4. 企业级安全与用户体系
- **首登引导安全机制**：无公开对外注册接口，无任何预置弱口令，首次启动需通过安全引导令牌（Setup Token）初始化管理员账号。
- **隐蔽管理入口**：登录管理员账号后连续点击左上角版本徽标 5 次唤出管理后台；后端接口层层校验，权限隔离严密。
- **操作审计与运维监控**：内置详尽的系统操作审计日志、服务就绪与探活健康检查接口（`/health/live`、`/health/ready`）。

### 5. 双数据库引擎持久化支持
- **生产级 MySQL 8.0+**：适用于多并发、多用户的云端部署环境，提供完整的事务与严格数据一致性。
- **内置本地 SQLite**：无需配置或安装独立数据库服务，单文件开箱即用，极简运维。

---

## 快速上手与部署

### 方式一：Docker Compose 生产部署（推荐）

1. **准备 `compose.yaml` 文件**：
```yaml
services:
  reader:
    image: reader-go:latest
    container_name: reader-go
    restart: unless-stopped
    ports:
      - "8081:8080"
    environment:
      READER_HTTP_ADDR: ":8080"
      READER_DATA_DIR: "/data"
      READER_PUBLIC_URL: "https://reader.example.com"
      READER_COOKIE_SECURE: "true"
      READER_SETUP_TOKEN: "your_random_bootstrap_token_minimum_24_chars"
      # 若使用外部 MySQL：
      READER_DB_DRIVER: "mysql"
      READER_DB_HOST: "127.0.0.1"
      READER_DB_PORT: "3306"
      READER_DB_NAME: "reader"
      READER_DB_USER: "reader"
      READER_DB_PASSWORD: "your_mysql_password"
      # 若使用本地 SQLite（取消注释即可）：
      # READER_DB_DRIVER: "sqlite"
    volumes:
      - ./data:/data
```

2. **启动服务**：
```bash
docker compose up -d
```

3. **初始化管理员**：
首次启动后，访问系统首页，页面将引导输入 `READER_SETUP_TOKEN` 完成首任管理员账号创建。

---

### 方式二：本地直接运行

本地只需 Go 1.24+ 与 Node.js 22：
```bash
# 1. 编译前端
cd web
npm install
npm run build
cd ..

# 2. 拷贝前端静态资源
mkdir -p cmd/reader/static
cp -r web/dist/* cmd/reader/static/

# 3. 运行服务
go run ./cmd/reader
```

---

## 环境变量配置速查

| 环境变量 | 默认值 | 必填 | 说明 |
| :--- | :--- | :---: | :--- |
| `READER_HTTP_ADDR` | `:8080` | 否 | 服务监听地址 |
| `READER_DATA_DIR` | `/data` | 否 | 数据持久化存储目录（缓存、TXT/EPUB 文件） |
| `READER_PUBLIC_URL` | — | **是** | 外部完整访问地址（用于 CSRF 与安全 Origin 校验） |
| `READER_COOKIE_SECURE` | `true` | 否 | 是否启用 Secure Cookie（HTTPS 环境设为 true） |
| `READER_SETUP_TOKEN` | — | 首次 | 首次运行初始化引导 Token（至少 24 位字符） |
| `READER_DB_DRIVER` | `mysql` | 否 | 数据库驱动类型：`mysql` 或 `sqlite` |
| `READER_DB_HOST` | `127.0.0.1`| MySQL 时必填 | MySQL 主机地址 |
| `READER_DB_PORT` | `3306` | 否 | MySQL 端口 |
| `READER_DB_NAME` | — | MySQL 时必填 | MySQL 数据库名称 |
| `READER_DB_USER` | — | MySQL 时必填 | MySQL 用户名 |
| `READER_DB_PASSWORD` | — | MySQL 时必填 | MySQL 密码 |

---

## 开源协议

本项目基于 **GPL-3.0-or-later** 协议开源，详见 [LICENSE](LICENSE) 与 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。用户需自行合法管理并提供书源，本项目不内置任何侵权内容或第三方规则。
