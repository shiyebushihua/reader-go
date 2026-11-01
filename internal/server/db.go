package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    must_change INTEGER NOT NULL DEFAULT 1,
    settings TEXT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    csrf TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS sources (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT '',
    source_key TEXT NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    group_name TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    rule TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, source_key)
);
CREATE INDEX IF NOT EXISTS idx_sources_user ON sources(user_id);

CREATE TABLE IF NOT EXISTS books (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    title TEXT NOT NULL,
    author TEXT NOT NULL DEFAULT '',
    cover TEXT NOT NULL,
    intro TEXT NOT NULL,
    kind TEXT NOT NULL,
    group_name TEXT NOT NULL DEFAULT '',
    source_id TEXT NULL,
    url TEXT NOT NULL,
    identity_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, identity_hash),
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS chapters (
    id TEXT PRIMARY KEY,
    book_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    UNIQUE(book_id, ordinal),
    FOREIGN KEY(book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS progress (
    book_id TEXT PRIMARY KEY,
    chapter_id TEXT NOT NULL,
    position REAL NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(book_id) REFERENCES books(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS bookmarks (
    id TEXT PRIMARY KEY,
    book_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    position REAL NOT NULL,
    note TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(book_id) REFERENCES books(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_bookmarks_book ON bookmarks(book_id);

CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES(1);
`

type DBDriver string

const (
	DriverMySQL  DBDriver = "mysql"
	DriverSQLite DBDriver = "sqlite"
)

func DetectDriver(c Config) DBDriver {
	driverEnv := strings.ToLower(strings.TrimSpace(os.Getenv("READER_DB_DRIVER")))
	if driverEnv == "sqlite" {
		return DriverSQLite
	}
	if driverEnv == "mysql" {
		return DriverMySQL
	}
	user := strings.TrimSpace(os.Getenv("READER_DB_USER"))
	if user == "" {
		return DriverSQLite
	}
	return DriverMySQL
}

func OpenDBWithConfig(c Config) (*sql.DB, DBDriver, error) {
	driver := DetectDriver(c)
	if driver == DriverSQLite {
		if err := os.MkdirAll(c.DataDir, 0700); err != nil {
			return nil, driver, fmt.Errorf("create data dir failed: %w", err)
		}
		dbPath := filepath.Join(c.DataDir, "reader.db")
		dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, driver, fmt.Errorf("open sqlite db failed: %w", err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, driver, fmt.Errorf("ping sqlite failed: %w", err)
		}
		slog.Info("using local SQLite storage", "path", dbPath)
		return db, driver, nil
	}

	pass, e := secret("READER_DB_PASSWORD")
	if e != nil {
		return nil, driver, e
	}
	cfg := mysql.NewConfig()
	cfg.User = os.Getenv("READER_DB_USER")
	cfg.Passwd = pass
	cfg.DBName = os.Getenv("READER_DB_NAME")
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(env("READER_DB_HOST", "127.0.0.1"), env("READER_DB_PORT", "3306"))
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 15 * time.Second
	cfg.WriteTimeout = 15 * time.Second
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	if cfg.User == "" || cfg.DBName == "" {
		return nil, driver, errors.New("READER_DB_USER and READER_DB_NAME are required for mysql")
	}
	tlsMode := env("READER_DB_TLS_MODE", "true")
	if tlsMode != "false" && tlsMode != "true" && tlsMode != "skip-verify" {
		return nil, driver, errors.New("READER_DB_TLS_MODE must be true, false or explicit skip-verify")
	}
	if tlsMode == "skip-verify" {
		slog.Warn("MySQL TLS identity verification explicitly disabled; use a trusted CA in production")
	}
	cfg.TLSConfig = tlsMode
	if ca := os.Getenv("READER_DB_CA_FILE"); ca != "" {
		pem, e := os.ReadFile(ca)
		if e != nil {
			return nil, driver, e
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, driver, errors.New("invalid database CA")
		}
		if e := mysql.RegisterTLSConfig("reader", &tls.Config{RootCAs: pool, ServerName: env("READER_DB_HOST", "127.0.0.1"), MinVersion: tls.VersionTLS12}); e != nil {
			return nil, driver, e
		}
		cfg.TLSConfig = "reader"
	}
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		return nil, driver, e
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(3)
	db.SetConnMaxLifetime(3 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if e = db.PingContext(ctx); e != nil {
		_ = db.Close()
		return nil, driver, e
	}
	slog.Info("using external MySQL storage", "host", cfg.Addr, "database", cfg.DBName)
	return db, driver, nil
}

func MigrateDB(ctx context.Context, db *sql.DB, driver DBDriver) error {
	if driver == DriverSQLite {
		for _, stmt := range strings.Split(sqliteSchema, ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("sqlite migration failed on '%s': %w", stmt, err)
			}
		}
		return nil
	}

	conn, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	var locked int
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK('reader_schema_migration',10)").Scan(&locked); e != nil || locked != 1 {
		return errors.New("migration lock unavailable")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK('reader_schema_migration')")
	for _, stmt := range strings.Split(schema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, e = conn.ExecContext(ctx, stmt); e != nil {
			return fmt.Errorf("schema migration failed: %w", e)
		}
	}
	return EnsureUserSourcesSchema(ctx, db)
}
