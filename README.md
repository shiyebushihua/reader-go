# 阅间 · Reader Go

Go + Vue 3 + TypeScript + MySQL 的自托管阅读服务，当前版本 **v0.0.5**。Docker 从源码构建前端并嵌入 Go 程序，运行时不需要 Node 或 JVM。

## 功能

- 私人书架、搜索、TXT/EPUB 导入、书签、阅读进度与分组。
- 手机紧凑书架、主题/字体/行距设置、上下滚动与左右翻页、换源。
- IndexedDB 书籍/章节离线缓存（不等于完整离线 PWA）。
- 书源管理、流式搜索、管理员用户管理与审计。
- 首次令牌初始化，无公开注册，无默认管理员账号或密码。
- 登录管理员后，连续点击版本号 5 次解锁管理入口；接口仍校验管理员权限。

书源引擎不保证兼容所有 Legado 规则，详见 [ENGINE_NOTES.md](ENGINE_NOTES.md)。EPUB 转换为纯文本，不保留完整排版。阅读进度为章节相对位置。仅导入有合法使用权的内容。

## 从 Git 克隆部署（Linux / CentOS / 1Panel）

### 1. 准备环境

- Docker Engine 和 Docker Compose v2（`docker compose version`）。
- 可访问的外部 MySQL 8.0+，提前创建 UTF-8 数据库及专用账号。初始化时账号需要该库的建表、索引和读写权限。
- 服务器能访问 Docker Hub、npm registry、Go module proxy。无需宿主机安装 Go/Node。
- 示例域名和数据库账号仅为占位值，必须按自己的环境填写。

```bash
git clone https://github.com/shiyebushihua/reader-go.git
cd reader-go
cp .env.example .env
mkdir -p secrets
chmod 700 secrets
```

编辑 `.env`，填写真实数据库地址、端口、库名、用户和浏览器访问来源。数据库密码不要写入仓库。

在 Bash 中交互填写密码，避免出现在命令历史：

```bash
read -r -s -p 'MySQL password: ' DB_PASSWORD; echo
printf '%s' "$DB_PASSWORD" > secrets/db_password.txt
unset DB_PASSWORD
openssl rand -hex 32 > secrets/setup_token.txt
# 容器以 UID/GID 10001 运行，Compose 文件型 secret 需要它能够读取。
sudo chown 10001:10001 secrets/*.txt
sudo chmod 400 secrets/*.txt
```

### 2. 选择访问方式（重要）

**推荐 HTTPS 反向代理**，默认设置：

```dotenv
READER_BIND_IP=127.0.0.1
READER_PORT=8081
READER_PUBLIC_URL=https://reader.example.com
READER_COOKIE_SECURE=true
```

将 HTTPS 站点代理到宿主机 `127.0.0.1:8081`，保留原始 Host，允许书籍上传并配置至少 120 秒读取超时。流式搜索需关闭代理响应缓冲。如果 1Panel/OpenResty 本身在容器内，`127.0.0.1` 指向代理容器，需另行配置共享网络或可达的宿主地址，不要直接照抄回环地址。

**临时 HTTP 直连**（登录流量不加密，仅用于可信网络测试）：

```dotenv
READER_BIND_IP=0.0.0.0
READER_PORT=8081
READER_PUBLIC_URL=http://YOUR_SERVER_HOST:8081
READER_COOKIE_SECURE=false
```

把 `YOUR_SERVER_HOST` 替换成自己的地址，并在防火墙中限制访问范围。HTTP 不能使用 `READER_COOKIE_SECURE=true`，否则登录后 Cookie 不会发送，表现为“请先登录”。切回 HTTPS 时恢复 `true`。

`READER_PUBLIC_URL` 必须与地址栏协议、主机和端口一致，不含路径。MySQL 地址不能填容器自己的 localhost。`READER_DB_TLS_MODE=true` 默认验证证书；`false` 仅用于可信隔离网络，`skip-verify` 不验证证书身份，不推荐生产使用。

### 3. 构建、初始化和启动

```bash
docker compose config --quiet
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
docker compose ps
docker compose logs --tail=100 reader
curl -f http://127.0.0.1:8081/health/ready
```

首次访问网站，使用以下命令读取初始化令牌，然后自行创建管理员账号和密码：

```bash
sudo cat secrets/setup_token.txt
```

不提供默认账号。初始化完成后不能重复创建首位管理员。数据库不包含任何预置生产账号、书籍或书源。

### 更新与备份

```bash
git pull --ff-only
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
```

更新前配套备份 MySQL 和 `reader_data` 数据卷。容器 `/data` 存放书籍正文；MySQL 存放用户、书架及进度。不要执行 `docker compose down -v`，它会删除数据卷。不要只备份数据库而丢弃正文卷。

`.env`、`secrets/`、`data/`、日志、二进制、镜像包及私钥均被 Git 和 Docker 构建上下文排除。不要使用 `git add -f` 上传这些文件。已经泄露的凭据应轮换，删除文件不能撤销既有泄露。

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
