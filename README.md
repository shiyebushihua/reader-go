# Reader Go (阅间)

English | [简体中文](README_ZH.md)

A self-hosted reading service built with Go + Vue 3 + TypeScript + MySQL, currently at **v0.0.6**. Docker builds the frontend from source and embeds it into a single Go binary—no Node.js or JVM is required at runtime.

## Features

- Personal bookshelf, book search, TXT/EPUB import, bookmarks, reading progress sync, and shelf groups.
- Compact mobile bookshelf, theme/font/line-spacing customization, vertical scroll & horizontal slide pagination, and book source switching.
- IndexedDB offline book & chapter caching (note: not a full offline PWA).
- Book source management, streaming search (SSE), admin user management, and audit logging.
- First-run setup token bootstrap, no public registration, and no default admin credentials.
- After logging in as an administrator, click the version badge 5 times consecutively to unlock the admin console entry; backend APIs strictly enforce admin authorization regardless.

The book source engine does not guarantee compatibility with all Legado rules; see [ENGINE_NOTES.md](ENGINE_NOTES.md) for details. EPUB files are normalized into safe plain text and do not preserve complex formatting or images. Reading progress is stored as a relative ratio within the chapter. Only import content for which you hold legal usage rights.

## Deployment from Git Clone (Linux / CentOS / 1Panel)

### 1. Prerequisites

- Docker Engine and Docker Compose v2 (`docker compose version`).
- A reachable external MySQL 8.0+ instance with a pre-created UTF-8 (`utf8mb4`) database and dedicated user account. During migration, the account requires table creation, index, and read/write privileges on that database.
- Outbound access to Docker Hub, npm registry, and Go module proxy during image build. Neither Go nor Node.js needs to be installed on the host.
- Example domains and database accounts below are placeholders—replace them with your actual environment values.

```bash
git clone https://github.com/shiyebushihua/reader-go.git
cd reader-go
cp .env.example .env
mkdir -p secrets
chmod 700 secrets
```

Edit `.env` with your real database host, port, database name, user, and public browser origin. Never commit database passwords to the repository.

Enter secrets interactively in Bash to avoid leaking them into shell history:

```bash
read -r -s -p 'MySQL password: ' DB_PASSWORD; echo
printf '%s' "$DB_PASSWORD" > secrets/db_password.txt
unset DB_PASSWORD
openssl rand -hex 32 > secrets/setup_token.txt
# The container runs as UID/GID 10001; file-based Compose secrets must be readable by it.
sudo chown 10001:10001 secrets/*.txt
sudo chmod 400 secrets/*.txt
```

### 2. Choose Your Access Mode (Important)

**Recommended: HTTPS Reverse Proxy** (default configuration):

```dotenv
READER_BIND_IP=127.0.0.1
READER_PORT=8081
READER_PUBLIC_URL=https://reader.example.com
READER_COOKIE_SECURE=true
```

Proxy your HTTPS site to host `127.0.0.1:8081`, preserve the original `Host` header, allow book upload body sizes (up to 100 MiB+), and configure at least a 120-second read timeout. Disable proxy response buffering for streaming search (`/api/v1/search/stream`). If 1Panel/OpenResty itself runs inside a container, `127.0.0.1` refers to the proxy container's loopback—configure a shared Docker network or reachable host address instead of copying `127.0.0.1` verbatim.

**Temporary Direct HTTP Access** (unencrypted login traffic; for trusted network testing only):

```dotenv
READER_BIND_IP=0.0.0.0
READER_PORT=8081
READER_PUBLIC_URL=http://YOUR_SERVER_HOST:8081
READER_COOKIE_SECURE=false
```

Replace `YOUR_SERVER_HOST` with your server address and restrict access via firewall. Do not set `READER_COOKIE_SECURE=true` over plain HTTP, otherwise the browser will refuse to send session cookies after login (resulting in repeated "Please log in" errors). Restore `true` when switching back to HTTPS.

`READER_PUBLIC_URL` must match the exact scheme, host, and port in the browser address bar without a path suffix. Do not set the MySQL host to `localhost` unless MySQL is in the same network namespace as the container. `READER_DB_TLS_MODE=true` verifies server certificates by default; `false` is only for trusted isolated networks, and `skip-verify` disables certificate identity verification (not recommended for production).

### 3. Build, Migrate, and Start

```bash
docker compose config --quiet
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
docker compose ps
docker compose logs --tail=100 reader
curl -f http://127.0.0.1:8081/health/ready
```

On first visit, read the setup token using the command below and create your initial administrator account and password in the web UI:

```bash
sudo cat secrets/setup_token.txt
```

No default account is provided. Once initialized, the initial admin setup cannot be repeated. The database contains no preloaded production accounts, books, or book sources.

### Updating and Backups

```bash
git pull --ff-only
docker compose build --pull
docker compose run --rm reader migrate
docker compose up -d
```

Back up both MySQL and the `reader_data` volume before updating. The container `/data` volume stores local book chapter files, while MySQL stores users, bookshelves, and reading progress. Never run `docker compose down -v`, as it deletes data volumes. Do not back up only the database while discarding the `/data` volume.

`.env`, `secrets/`, `data/`, logs, binaries, image tarballs, and private keys are excluded from Git and the Docker build context. Never upload these files using `git add -f`. Any leaked credentials must be rotated immediately; deleting a file does not revoke an existing leak.

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
