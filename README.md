# Reader Go (阅间)

English | [简体中文](README_ZH.md)

A self-hosted reading service built with Go + Vue 3 + TypeScript + MySQL, currently at **v0.0.8**. Docker builds the frontend from source and embeds it into a single Go binary—no Node.js or JVM is required at runtime.

## Features

- Personal bookshelf, book search, TXT/EPUB import, bookmarks, reading progress sync, and shelf groups.
- Compact mobile bookshelf, theme/font/line-spacing customization, vertical scroll & horizontal slide pagination, and book source switching.
- IndexedDB offline book & chapter caching (note: not a full offline PWA).
- Per-user independent book source management (each user imports, edits, and uses their own book sources independently), streaming search (SSE), admin user management, and audit logging.
- First-run setup token bootstrap, no public registration, and no default admin credentials.
- After logging in as an administrator, click the version badge 5 times consecutively to unlock the admin console entry; backend APIs strictly enforce admin authorization regardless.

The book source engine does not guarantee compatibility with all Legado rules; see [ENGINE_NOTES.md](ENGINE_NOTES.md) for details. EPUB files are normalized into safe plain text and do not preserve complex formatting or images. Reading progress is stored as a relative ratio within the chapter. Only import content for which you hold legal usage rights.

---

## Docker Deployment Guide

### 1. Prerequisites & MySQL Setup (Full DDL)

- **Docker Engine** (`20.10+`) and **Docker Compose v2** (`docker compose version`).
- **External MySQL 8.0+**: Prepare a dedicated `utf8mb4` database and user account.
- You can initialize the tables either by running `reader migrate` OR by importing [`sql/schema.sql`](sql/schema.sql) directly in 1Panel / Navicat / MySQL CLI:

```sql
-- 1. Create database & dedicated user
CREATE DATABASE IF NOT EXISTS reader CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'reader'@'%' IDENTIFIED BY 'your_strong_db_password';
GRANT ALL PRIVILEGES ON reader.* TO 'reader'@'%';
FLUSH PRIVILEGES;

USE reader;
SET NAMES utf8mb4;

-- 2. Schema DDL (also available in sql/schema.sql & internal/server/schema.sql)
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

### 2. Method A: Docker Compose Deployment (Recommended)

#### Step 1: Clone Repository & Prepare Secrets

```bash
git clone https://github.com/shiyebushihua/reader-go.git
cd reader-go
cp .env.example .env
mkdir -p secrets
chmod 700 secrets
```

Enter secrets interactively in Bash to avoid leaking them into shell history:

```bash
read -r -s -p 'MySQL password: ' DB_PASSWORD; echo
printf '%s' "$DB_PASSWORD" > secrets/db_password.txt
unset DB_PASSWORD
# Setup token must be at least 24 characters (32-byte hex = 64 characters)
openssl rand -hex 32 > secrets/setup_token.txt
# The container runs as UID/GID 10001; file-based Compose secrets must be readable by it.
sudo chown 10001:10001 secrets/*.txt
sudo chmod 400 secrets/*.txt
```

#### Step 2: Configure `.env`

**Option 1 — HTTPS Reverse Proxy (Recommended for Production)**:

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

**Option 2 — Direct HTTP Access (Trusted LAN / Testing Only)**:

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

> **Important Notes**:
> - `READER_PUBLIC_URL` must match the exact scheme, host, and port in your browser's address bar without a trailing path.
> - Do **not** set `READER_COOKIE_SECURE=true` over plain HTTP, otherwise browsers will refuse to store session cookies after login.
> - Do **not** set `READER_DB_HOST=localhost` unless the container shares the host network namespace.

#### Step 3: Build, Migrate Database, and Start

```bash
docker compose config --quiet
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
docker compose ps
docker compose logs --tail=100 reader
curl -f http://127.0.0.1:8081/health/ready
```

On first visit, view your setup token and create the initial administrator account in the web UI:

```bash
sudo cat secrets/setup_token.txt
```

---

### 3. Method B: Standalone `docker build` & `docker run`

If you prefer running a single Docker container without Docker Compose:

```bash
# 1. Build image
docker build -t reader-go:0.0.6 .

# 2. Prepare host data directory (owned by container UID/GID 10001)
mkdir -p /opt/reader-go/data
sudo chown -R 10001:10001 /opt/reader-go/data

# 3. Run database schema migration
docker run --rm \
  -e READER_DB_HOST=192.168.1.10 \
  -e READER_DB_PORT=3306 \
  -e READER_DB_NAME=reader \
  -e READER_DB_USER=reader \
  -e READER_DB_PASSWORD='your_strong_db_password' \
  -e READER_DB_TLS_MODE=false \
  -v /opt/reader-go/data:/data \
  reader-go:0.0.6 migrate

# 4. Start the service container
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

### 4. Method C: 1Panel Offline Image `.tar` Build, Import & Upgrade

If your 1Panel server has slow outbound access to npm/Go/Docker Hub, you can build an offline image `.tar` archive locally and import it directly into 1Panel:

#### 1. Export Multi-Arch Offline Image `.tar` Locally

```bash
# Build for x86_64 (linux/amd64) Linux servers:
docker buildx build --platform linux/amd64 -t reader-go:0.0.6 \
  --output type=docker,name=reader-go:0.0.6,dest=reader-go-0.0.6-amd64.tar .

# Or build for ARM64 (linux/arm64) Linux servers:
docker buildx build --platform linux/arm64 -t reader-go:0.0.6 \
  --output type=docker,name=reader-go:0.0.6,dest=reader-go-0.0.6-arm64.tar .
```

#### 2. Import Image & Upgrade in 1Panel

1. Open **1Panel** $\rightarrow$ **Containers (容器)** $\rightarrow$ **Images (镜像)** $\rightarrow$ click **Import Image (导入镜像)** and upload `reader-go-0.0.6-amd64.tar` (or run `docker load -i reader-go-0.0.6-amd64.tar` on the server).
2. Confirm `reader-go:0.0.6` appears in the image list.
3. Update `image: reader-go:0.0.6` in `compose.yaml` (or in 1Panel **Containers $\rightarrow$ Compose**), then run:
   ```bash
   docker compose run --rm reader migrate
   docker compose up -d
   ```

---

### 5. Reverse Proxy Configuration (Nginx / OpenResty / 1Panel)

When placing Nginx / OpenResty in front of Reader Go, ensure upload body size limits are raised for EPUB/TXT imports and proxy buffering is disabled for SSE streaming search (`/api/v1/search/stream`):

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

> If 1Panel's OpenResty runs inside a container, `127.0.0.1` inside OpenResty points to the OpenResty container itself—use the host's Docker bridge IP (e.g. `172.17.0.1:8081`) or attach both containers to a shared Docker network.

---

### 6. Environment Variables Reference

| Variable | Default | Required | Description |
| :--- | :--- | :---: | :--- |
| `READER_HTTP_ADDR` | `:8080` | No | Container listen address (`host:port` or `:port`). |
| `READER_PORT` | `8081` (Compose) | No | Host port mapped in `compose.yaml` (or fallback listen port if `READER_HTTP_ADDR` is unset). |
| `READER_BIND_IP` | `127.0.0.1` | No | Host bind IP in `compose.yaml`. |
| `READER_DATA_DIR` | `/data` | No | Directory storing imported/cached book chapters (`books/`, `tmp/`). |
| `READER_PUBLIC_URL` | — | **Yes** | Exact browser origin (`http://` or `https://`, host, and port, no path) for CSRF Origin validation. |
| `READER_COOKIE_SECURE` | `true` | No | Set `true` for HTTPS, `false` for plain HTTP testing. |
| `READER_SETUP_TOKEN` / `READER_SETUP_TOKEN_FILE` | — | First Run | Initial admin setup token (minimum 24 characters). |
| `READER_DB_HOST` | `127.0.0.1` | **Yes** | MySQL hostname or IP address. |
| `READER_DB_PORT` | `3306` | No | MySQL port. |
| `READER_DB_NAME` | — | **Yes** | MySQL database name (`utf8mb4`). |
| `READER_DB_USER` | — | **Yes** | MySQL username. |
| `READER_DB_PASSWORD` / `READER_DB_PASSWORD_FILE` | — | **Yes** | MySQL password or path to secret file. |
| `READER_DB_TLS_MODE` | `true` | No | MySQL TLS mode: `true` (verify CA), `false` (trusted intranet), or `skip-verify`. |
| `READER_DB_CA_FILE` | — | No | Custom PEM CA bundle path for MySQL TLS verification. |

---

### 7. Updating and Backups

```bash
git pull --ff-only
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
```

Back up both MySQL and the `reader_data` volume before updating. The container `/data` volume stores local book chapter files, while MySQL stores users, bookshelves, and reading progress. Never run `docker compose down -v`, as it deletes data volumes.

`.env`, `secrets/`, `data/`, logs, binaries, image tarballs, and private keys are excluded from Git and the Docker build context. Never upload these files using `git add -f`.

---

## Local Development and Verification

Requires Go 1.24+, Node.js 22, and an external MySQL instance. On Windows, you can run `./build.ps1`.

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

The placeholder file in `cmd/reader/static` only satisfies Go compilation and is not a usable UI; you must build and copy the frontend first (which the `Dockerfile` handles automatically). Standard unit tests do not connect to a database; live MySQL integration tests require explicitly setting `READER_INTEGRATION=1` and should only be run against a dedicated test database.

## License

Licensed under **GPL-3.0-or-later**; see [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Features and book source interfaces reference the original Reader project. Book sources are provided lawfully by users and are not distributed with this repository.
