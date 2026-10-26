# 阅间 · Reader Go

[English](README.md) | 简体中文

Go + Vue 3 + TypeScript + MySQL 的自托管阅读服务，当前版本 **v0.0.7**。Docker 从源码构建前端并嵌入单个 Go 二进制程序，运行时不需要 Node.js 或 JVM。

## 功能

- 私人书架、搜索、TXT/EPUB 导入、书签、阅读进度同步与书架分组。
- 手机紧凑书架（适配 iOS / 移动端网页交互）、主题/字体/行距设置、上下滚动与左右滑动翻页、一键换源。
- IndexedDB 书籍/章节离线缓存（不等于完整离线 PWA）。
- 个人独立书源管理（每位用户各自导入、维护并使用自己的书源，互不干扰）、SSE 流式搜索、管理员用户管理与操作审计。
- 首次令牌初始化，无公开注册，无默认管理员账号或密码。
- 登录管理员后，连续点击版本号 5 次解锁管理入口；后端接口仍严格校验管理员权限。

书源引擎不保证兼容所有 Legado 规则，详见 [ENGINE_NOTES.md](ENGINE_NOTES.md)。EPUB 转换为纯文本，不保留完整排版与图片。阅读进度为章节内相对位置。仅导入有合法使用权的内容。

---

## Docker 部署教程

### 1. 环境准备与 MySQL 初始化（完整建表 DDL）

- **Docker Engine**（`20.10+`）与 **Docker Compose v2**（`docker compose version`）。
- **外部 MySQL 8.0+**：提前创建 `utf8mb4` 编码的数据库与专用账号。
- 您既可以通过 `reader migrate` 自动建表，也可以直接在 **1Panel 数据库面板 / Navicat / MySQL 命令行** 中导入项目根目录下的 [`sql/schema.sql`](sql/schema.sql) 或执行以下完整 DDL：

```sql
-- 1. 创建数据库与专用账号
CREATE DATABASE IF NOT EXISTS reader CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'reader'@'%' IDENTIFIED BY 'your_strong_db_password';
GRANT ALL PRIVILEGES ON reader.* TO 'reader'@'%';
FLUSH PRIVILEGES;

USE reader;
SET NAMES utf8mb4;

-- 2. 完整表结构 DDL（同步保存在 sql/schema.sql 与 internal/server/schema.sql）
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INT PRIMARY KEY,
  applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS users (
  id CHAR(32) PRIMARY KEY,
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(100) NOT NULL,
  role VARCHAR(10) NOT NULL,
  status VARCHAR(10) NOT NULL DEFAULT 'active',
  must_change BOOLEAN NOT NULL DEFAULT TRUE,
  settings JSON NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sessions (
  token_hash CHAR(64) PRIMARY KEY,
  user_id CHAR(32) NOT NULL,
  csrf CHAR(64) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  INDEX (user_id),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sources (
  id CHAR(32) PRIMARY KEY,
  user_id CHAR(32) NOT NULL DEFAULT '',
  source_key CHAR(64) NOT NULL,
  name VARCHAR(255) NOT NULL,
  url TEXT NOT NULL,
  group_name VARCHAR(255) NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  rule JSON NOT NULL,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  UNIQUE (user_id, source_key),
  INDEX (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS books (
  id CHAR(32) PRIMARY KEY,
  user_id CHAR(32) NOT NULL,
  title VARCHAR(512) NOT NULL,
  author VARCHAR(255) NOT NULL DEFAULT '',
  cover TEXT NOT NULL,
  intro MEDIUMTEXT NOT NULL,
  kind VARCHAR(10) NOT NULL,
  group_name VARCHAR(255) NOT NULL DEFAULT '',
  source_id CHAR(32) NULL,
  url TEXT NOT NULL,
  identity_hash CHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE (user_id, identity_hash),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS chapters (
  id CHAR(32) PRIMARY KEY,
  book_id CHAR(32) NOT NULL,
  ordinal INT NOT NULL,
  title VARCHAR(1024) NOT NULL,
  url TEXT NOT NULL,
  UNIQUE (book_id, ordinal),
  FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS progress (
  book_id CHAR(32) PRIMARY KEY,
  chapter_id CHAR(32) NOT NULL,
  position DOUBLE NOT NULL DEFAULT 0,
  version BIGINT NOT NULL DEFAULT 1,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bookmarks (
  id CHAR(32) PRIMARY KEY,
  book_id CHAR(32) NOT NULL,
  chapter_id CHAR(32) NOT NULL,
  position DOUBLE NOT NULL,
  note VARCHAR(1000) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  INDEX (book_id),
  FOREIGN KEY (book_id) REFERENCES books(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  actor VARCHAR(64) NOT NULL,
  action VARCHAR(64) NOT NULL,
  target VARCHAR(128) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

INSERT IGNORE INTO schema_migrations (version) VALUES (1);
```

---

### 2. 方式一：Docker Compose 编排部署（推荐）

#### 第一步：克隆代码并生成密钥文件

```bash
git clone https://github.com/shiyebushihua/reader-go.git
cd reader-go
cp .env.example .env
mkdir -p secrets
chmod 700 secrets
```

在 Bash 中交互式写入数据库密码和初始化令牌，避免泄露到命令历史中：

```bash
read -r -s -p 'MySQL password: ' DB_PASSWORD; echo
printf '%s' "$DB_PASSWORD" > secrets/db_password.txt
unset DB_PASSWORD
# 初始化令牌长度不少于 24 位（32 字节 hex 为 64 位）
openssl rand -hex 32 > secrets/setup_token.txt
# 容器默认以 UID/GID 10001 运行，需赋予读取权限
sudo chown 10001:10001 secrets/*.txt
sudo chmod 400 secrets/*.txt
```

#### 第二步：修改 `.env` 配置文件

**场景 A —— HTTPS 反向代理访问（生产环境推荐）**：

```dotenv
READER_DB_HOST=mysql.example.internal
READER_DB_PORT=3306
READER_DB_NAME=reader
READER_DB_USER=reader
READER_DB_TLS_MODE=false
READER_BIND_IP=127.0.0.1
READER_PORT=8081
READER_PUBLIC_URL=https://reader.example.com
READER_COOKIE_SECURE=true
```

**场景 B —— 临时 HTTP IP 直连（仅限内网或测试环境）**：

```dotenv
READER_DB_HOST=192.168.1.10
READER_DB_PORT=3306
READER_DB_NAME=reader
READER_DB_USER=reader
READER_DB_TLS_MODE=false
READER_BIND_IP=0.0.0.0
READER_PORT=8081
READER_PUBLIC_URL=http://192.168.1.20:8081
READER_COOKIE_SECURE=false
```

> **重要注意事项**：
> - `READER_PUBLIC_URL` 必须与浏览器地址栏中的协议（`http`/`https`）、域名/IP、端口完全一致，末尾不要带路径。
> - 使用纯 HTTP 访问时，**必须设置 `READER_COOKIE_SECURE=false`**，否则浏览器不会携带登录 Cookie，会出现登录后仍提示“请先登录”。切回 HTTPS 后请恢复为 `true`。
> - `READER_DB_HOST` 不要填 `localhost`（容器内的 `localhost` 指向容器自身）。

#### 第三步：构建、迁移表结构并启动服务

```bash
docker compose config --quiet
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
docker compose ps
docker compose logs --tail=100 reader
curl -f http://127.0.0.1:8081/health/ready
```

首次打开网页时，通过以下命令查看初始化令牌，在页面中创建首位管理员账号与密码：

```bash
sudo cat secrets/setup_token.txt
```

---

### 3. 方式二：单容器 `docker build` + `docker run` 部署

如果不使用 Docker Compose，也可以直接使用 `docker` 命令构建并运行：

```bash
# 1. 构建镜像
docker build -t reader-go:0.0.6 .

# 2. 创建宿主机数据目录并授权给容器运行用户 (10001:10001)
mkdir -p /opt/reader-go/data
sudo chown -R 10001:10001 /opt/reader-go/data

# 3. 执行数据库初始化迁移 (migrate)
docker run --rm \
  -e READER_DB_HOST=192.168.1.10 \
  -e READER_DB_PORT=3306 \
  -e READER_DB_NAME=reader \
  -e READER_DB_USER=reader \
  -e READER_DB_PASSWORD='your_strong_db_password' \
  -e READER_DB_TLS_MODE=false \
  -v /opt/reader-go/data:/data \
  reader-go:0.0.6 migrate

# 4. 启动常驻服务容器
docker run -d \
  --name reader-go \
  --restart unless-stopped \
  -p 127.0.0.1:8081:8080 \
  -e READER_DB_HOST=192.168.1.10 \
  -e READER_DB_PORT=3306 \
  -e READER_DB_NAME=reader \
  -e READER_DB_USER=reader \
  -e READER_DB_PASSWORD='your_strong_db_password' \
  -e READER_DB_TLS_MODE=false \
  -e READER_SETUP_TOKEN="$(openssl rand -hex 32)" \
  -e READER_PUBLIC_URL=https://reader.example.com \
  -e READER_COOKIE_SECURE=true \
  -v /opt/reader-go/data:/data \
  reader-go:0.0.6
```

---

### 4. 方式三：1Panel 面板离线镜像 `.tar` 包构建、导入与升级

当云端服务器拉取 npm / Go 依赖较慢时，可在本地电脑构建好 `.tar` 镜像包，上传至 1Panel 面板一键导入升级：

#### 1. 在本地导出离线镜像包

```bash
# 构建 x86_64 (linux/amd64) 服务器镜像包：
docker buildx build --platform linux/amd64 -t reader-go:0.0.6 \
  --output type=docker,name=reader-go:0.0.6,dest=reader-go-0.0.6-amd64.tar .

# 或构建 ARM64 (linux/arm64) 服务器镜像包：
docker buildx build --platform linux/arm64 -t reader-go:0.0.6 \
  --output type=docker,name=reader-go:0.0.6,dest=reader-go-0.0.6-arm64.tar .
```

#### 2. 在 1Panel 中导入并升级版本

1. 登录 **1Panel 面板** $\rightarrow$ **容器** $\rightarrow$ **镜像** $\rightarrow$ 点击 **导入镜像**，选择上传 `reader-go-0.0.6-amd64.tar`（或在服务器终端执行 `docker load -i reader-go-0.0.6-amd64.tar`）。
2. 确认镜像列表中已出现 `reader-go:0.0.6`。
3. 在 **容器 $\rightarrow$ 编排**（或服务器项目目录）中，确保 `compose.yaml` 的镜像版本为 `image: reader-go:0.0.6`，然后执行：
   ```bash
   docker compose run --rm reader migrate
   docker compose up -d
   ```

---

### 5. 反向代理配置示例（Nginx / OpenResty / 1Panel 网站）

使用 Nginx 或 1Panel OpenResty 反代时，请放宽上传体积限制（用于导入 TXT/EPUB），并为流式搜索接口（`/api/v1/search/stream`）关闭代理缓冲：

```nginx
location / {
    proxy_pass http://127.0.0.1:8081;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    client_max_body_size 100m;
    proxy_read_timeout 120s;
    proxy_send_timeout 120s;
}

location /api/v1/search/stream {
    proxy_pass http://127.0.0.1:8081;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    proxy_buffering off;
    proxy_cache off;
    chunked_transfer_encoding on;
    proxy_read_timeout 120s;
}
```

> 若 1Panel 的 OpenResty 运行在容器内，OpenResty 容器里的 `127.0.0.1` 指向其自身而非宿主机，请改为宿主机 Docker 网桥地址（如 `172.17.0.1:8081`）或配置共享容器网络。

---

### 6. 环境变量一览表

| 变量名 | 默认值 | 必填 | 说明 |
| :--- | :--- | :---: | :--- |
| `READER_HTTP_ADDR` | `:8080` | 否 | 容器内部监听地址（如 `:8080` 或 `0.0.0.0:8080`）。 |
| `READER_PORT` | `8081`（Compose） | 否 | `compose.yaml` 映射到宿主机的端口（未设置 `READER_HTTP_ADDR` 时也作为容器监听端口）。 |
| `READER_BIND_IP` | `127.0.0.1` | 否 | `compose.yaml` 绑定的宿主机网卡地址。 |
| `READER_DATA_DIR` | `/data` | 否 | 存放本地导入与缓存章节正文的目录（自动创建 `books/` 与 `tmp/`）。 |
| `READER_PUBLIC_URL` | — | **是** | 浏览器访问的完整 Origin（含 `http://` 或 `https://`、域名/IP 与端口，不含路径），用于 CSRF 校验。 |
| `READER_COOKIE_SECURE` | `true` | 否 | HTTPS 设为 `true`，HTTP 测试设为 `false`。 |
| `READER_SETUP_TOKEN` / `READER_SETUP_TOKEN_FILE` | — | 首次初始化 | 首次创建管理员所需的初始化令牌（长度不少于 24 位）。 |
| `READER_DB_HOST` | `127.0.0.1` | **是** | MySQL 主机地址。 |
| `READER_DB_PORT` | `3306` | 否 | MySQL 端口。 |
| `READER_DB_NAME` | — | **是** | MySQL 数据库名（需 `utf8mb4`）。 |
| `READER_DB_USER` | — | **是** | MySQL 用户名。 |
| `READER_DB_PASSWORD` / `READER_DB_PASSWORD_FILE` | — | **是** | MySQL 密码或密钥文件路径。 |
| `READER_DB_TLS_MODE` | `true` | 否 | MySQL TLS 模式：`true`（校验证书）、`false`（可信内网关闭 TLS）或 `skip-verify`。 |
| `READER_DB_CA_FILE` | — | 否 | 自定义 MySQL TLS 根证书 PEM 文件路径。 |

---

### 7. 日常更新与数据备份

```bash
git pull --ff-only
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
```

更新前请配套备份 MySQL 数据库和 `reader_data` 数据卷。容器 `/data` 存放书籍章节正文；MySQL 存放用户、书架及阅读进度。**切勿执行 `docker compose down -v`**，否则会删除数据卷。

`.env`、`secrets/`、`data/`、日志、二进制、镜像包及私钥均已被 Git 和 Docker 构建上下文排除，请勿使用 `git add -f` 提交。

---

## 本地开发与检查

需要 Go 1.24+、Node 22 和外部 MySQL。Windows 可运行 `./build.ps1`。

```bash
cd web
npm ci
npm test
npm run build
cd ..
mkdir -p cmd/reader/static
cp -R web/dist/. cmd/reader/static/
go test ./...
go vet ./...
go build -o dist/reader ./cmd/reader
```

Go 源码中的静态占位文件仅用于编译，不是可用 UI；必须先构建并复制前端，Dockerfile 会自动完成这些步骤。普通单元测试不连接数据库；真实数据库集成测试需要显式设置 `READER_INTEGRATION=1`，仅应针对专用测试库运行。

## 许可

采用 GPL-3.0-or-later，见 [LICENSE](LICENSE) 与 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。功能及书源接口参考原 Reader 项目。真实书源由用户合法提供，不随代码发布。
