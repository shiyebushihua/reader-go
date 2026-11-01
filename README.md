# Reader Go

English | [简体中文](README_ZH.md)

A modern, high-performance, self-hosted web reading service built with Go + Vue 3 + TypeScript, currently at stable version **v0.0.25**. Docker builds the frontend from source and embeds it into a single Go binary—no Node.js or JVM runtime is required in production.

---

## Key Features

### 1. Immersive Modern Reading Experience
- **Fine-Grained Typography**: Multiple curated reading themes (Day, Sepia, Soft Green, Night Dark), font family selection, stepless font-size scaling, and line-height/margin tuning.
- **Dual Reading Modes**: Horizontal paginated slide mode (optimized for mobile touch ergonomics) and continuous vertical scroll mode.
- **Pure Frontend Speech Synthesizer (TTS)**: Built-in browser-native Web Speech API reader with voice selection, rate and pitch controls, auto-advancing reading loop, and background playback controls without external paid API costs.
- **Content Purification & Formatting**: Integrated text purification rule engine and paragraph normalizer supporting regular expression cleaning to filter out ads, watermarks, and noisy text.
- **Simplified / Traditional Chinese Conversion**: Seamless on-the-fly Chinese script conversion (Original / Simplified / Traditional).
- **Automatic Chapter Sub-page Pagination**: When a chapter spans multiple sub-pages, the engine automatically traverses and merges all parts into a coherent single-chapter read.
- **Dual-Tier High-Speed Caching**: Local server-side filesystem chapter caching (`cache/books/...`) combined with client browser-native IndexedDB offline caching.
- **One-Click Source Switching**: Instantly swap book sources with reading progress preserved and live source update status.

### 2. High-Performance Source Rule Engine
- **Universal Rule Parsers**: Complete support for CSS selectors, XPath 1.0, JSONPath queries, and multi-stage regex cleaning pipelines.
- **Isolated JavaScript Sandbox**: Built-in isolated JS runtime for complex dynamic calculations, decryptors, and custom extraction logic.
- **Smart Protocol Adaptation**: Native handling for GET/POST requests, custom headers, and automatic GBK/UTF-8 encoding detection.
- **Per-User Isolated Sources**: Each user maintains their own independent source rules with JSON batch import/export capabilities.
- **Streaming Search (SSE)**: Server-Sent Events stream live search results concurrently across multiple sources, displaying the first findings in milliseconds.

### 3. Smooth & Instant UI Transitions
- **Zero Blank Screen Flash on Bookshelf**: Module-level memory caching and silent background refresh render the bookshelf immediately upon exit from the reader without skeleton flashing.
- **Persistent Search View**: KeepAlive state preservation across navigation keeps search keywords, active filters, and scroll position intact.
- **Mobile-First Aesthetic**: Modern, responsive layout across phones, tablets, and wide desktop displays.

### 4. Robust Security & Administration
- **Bootstrap Token Onboarding**: No public registration and no default credentials; initial administrator setup requires an explicit setup token.
- **Discreet Admin Console**: Authenticated administrators tap the version badge 5 times consecutively to reveal management tools; all endpoints strictly enforce server-side authorization.
- **Audit Logging & Health Probes**: Comprehensive operational audit trail and `/health/live` / `/health/ready` Kubernetes-style probes.

### 5. Dual Database Persistence Support
- **External MySQL 8.0+**: Robust transactional engine designed for multi-user production environments.
- **Embedded SQLite**: Single-file, zero-configuration local database for personal deployments.

---

## Deployment Guide

### Docker Compose (Recommended)

1. **`compose.yaml`**:
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
      # For external MySQL:
      READER_DB_DRIVER: "mysql"
      READER_DB_HOST: "127.0.0.1"
      READER_DB_PORT: "3306"
      READER_DB_NAME: "reader"
      READER_DB_USER: "reader"
      READER_DB_PASSWORD: "your_mysql_password"
      # For local SQLite, uncomment below:
      # READER_DB_DRIVER: "sqlite"
    volumes:
      - ./data:/data
```

2. **Start the service**:
```bash
docker compose up -d
```

3. **Bootstrap setup**:
Open the home page in a browser, enter your `READER_SETUP_TOKEN`, and configure the initial admin username and password.

---

## Configuration Reference

| Environment Variable | Default | Required | Description |
| :--- | :--- | :---: | :--- |
| `READER_HTTP_ADDR` | `:8080` | No | Listening address and port |
| `READER_DATA_DIR` | `/data` | No | Persistent directory for book content and cache |
| `READER_PUBLIC_URL` | — | **Yes** | Public browser origin for CSRF and Origin validation |
| `READER_COOKIE_SECURE` | `true` | No | Enforces secure HTTPS cookies |
| `READER_SETUP_TOKEN` | — | First run | Initial administrator bootstrap token (>= 24 chars) |
| `READER_DB_DRIVER` | `mysql` | No | Database driver: `mysql` or `sqlite` |
| `READER_DB_HOST` | `127.0.0.1` | If MySQL | MySQL host |
| `READER_DB_PORT` | `3306` | No | MySQL port |
| `READER_DB_NAME` | — | If MySQL | MySQL database name |
| `READER_DB_USER` | — | If MySQL | MySQL username |
| `READER_DB_PASSWORD` | — | If MySQL | MySQL password |

---

## License

Licensed under **GPL-3.0-or-later**; see [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
