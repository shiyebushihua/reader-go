package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"readergo/internal/engine"
)

//go:embed schema.sql
var schema string

const Version = "0.0.10"
const DefaultVersion = Version

type Config struct {
	Addr, DataDir, PublicURL, SetupToken, AppVersion string
	CookieSecure                                     bool
}
type Server struct {
	db       *sql.DB
	cfg      Config
	engine   *engine.Engine
	mux      *http.ServeMux
	imports  chan struct{}
	search   chan struct{}
	mu       sync.Mutex
	attempts map[string]attempt
}
type attempt struct {
	Count int
	Until time.Time
}
type apiError struct {
	status        int
	code, message string
}

func (e *apiError) Error() string                 { return e.message }
func fail(status int, code, message string) error { return &apiError{status, code, message} }
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func secret(k string) (string, error) {
	if f := os.Getenv(k + "_FILE"); f != "" {
		b, e := os.ReadFile(f)
		return strings.TrimSpace(string(b)), e
	}
	return os.Getenv(k), nil
}
func LoadConfig() (Config, error) {
	token, e := secret("READER_SETUP_TOKEN")
	if e != nil {
		return Config{}, e
	}
	if token != "" && len(token) < 24 {
		return Config{}, errors.New("READER_SETUP_TOKEN must be at least 24 characters")
	}
	addr := env("READER_HTTP_ADDR", "")
	if addr == "" {
		if port := env("READER_PORT", ""); port != "" {
			addr = ":" + port
		} else {
			addr = ":8080"
		}
	}
	c := Config{Addr: addr, DataDir: env("READER_DATA_DIR", "./data"), PublicURL: os.Getenv("READER_PUBLIC_URL"), SetupToken: token, CookieSecure: env("READER_COOKIE_SECURE", "true") == "true", AppVersion: env("READER_APP_VERSION", DefaultVersion)}
	if c.PublicURL != "" {
		u, e := url.Parse(c.PublicURL)
		if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return c, errors.New("READER_PUBLIC_URL must be an http(s) origin")
		}
		c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	}
	for _, p := range []string{"books", "tmp"} {
		if e := os.MkdirAll(filepath.Join(c.DataDir, p), 0700); e != nil {
			return c, e
		}
	}
	return c, nil
}
func OpenDB() (*sql.DB, error) {
	pass, e := secret("READER_DB_PASSWORD")
	if e != nil {
		return nil, e
	}
	c := mysql.NewConfig()
	c.User = os.Getenv("READER_DB_USER")
	c.Passwd = pass
	c.DBName = os.Getenv("READER_DB_NAME")
	c.Net = "tcp"
	c.Addr = net.JoinHostPort(env("READER_DB_HOST", "127.0.0.1"), env("READER_DB_PORT", "3306"))
	c.ParseTime = true
	c.Loc = time.UTC
	c.Timeout = 5 * time.Second
	c.ReadTimeout = 15 * time.Second
	c.WriteTimeout = 15 * time.Second
	c.Params = map[string]string{"charset": "utf8mb4"}
	if c.User == "" || c.DBName == "" {
		return nil, errors.New("READER_DB_USER and READER_DB_NAME are required")
	}
	tlsMode := env("READER_DB_TLS_MODE", "true")
	if tlsMode != "false" && tlsMode != "true" && tlsMode != "skip-verify" {
		return nil, errors.New("READER_DB_TLS_MODE must be true, false or explicit skip-verify")
	}
	if tlsMode == "skip-verify" {
		slog.Warn("MySQL TLS identity verification explicitly disabled; use a trusted CA in production")
	}
	c.TLSConfig = tlsMode
	if ca := os.Getenv("READER_DB_CA_FILE"); ca != "" {
		pem, e := os.ReadFile(ca)
		if e != nil {
			return nil, e
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid database CA")
		}
		if e := mysql.RegisterTLSConfig("reader", &tls.Config{RootCAs: pool, ServerName: env("READER_DB_HOST", "127.0.0.1"), MinVersion: tls.VersionTLS12}); e != nil {
			return nil, e
		}
		c.TLSConfig = "reader"
	}
	db, e := sql.Open("mysql", c.FormatDSN())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(3)
	db.SetConnMaxLifetime(3 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func EnsureUserSourcesSchema(ctx context.Context, db *sql.DB) error {
	var hasCol int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='sources' AND COLUMN_NAME='user_id'").Scan(&hasCol); err != nil {
		return err
	}
	if hasCol == 0 {
		if _, err := db.ExecContext(ctx, "ALTER TABLE sources ADD COLUMN user_id CHAR(32) NOT NULL DEFAULT '' AFTER id"); err != nil {
			return err
		}
	}
	// Backfill any legacy global sources (user_id='') to the primary admin user or first user
	var ownerID string
	_ = db.QueryRowContext(ctx, "SELECT id FROM users ORDER BY (role='admin') DESC, created_at ASC LIMIT 1").Scan(&ownerID)
	if ownerID != "" {
		_, _ = db.ExecContext(ctx, "UPDATE sources SET user_id=? WHERE user_id=''", ownerID)
	}
	// Drop legacy global UNIQUE(source_key) index if present and ensure per-user UNIQUE(user_id, source_key)
	var oldIdx int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='sources' AND INDEX_NAME='source_key' AND NON_UNIQUE=0
	`).Scan(&oldIdx); err == nil && oldIdx > 0 {
		_, _ = db.ExecContext(ctx, "ALTER TABLE sources DROP INDEX source_key")
	}
	var newIdx int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='sources' AND COLUMN_NAME='user_id' AND SEQ_IN_INDEX=1 AND NON_UNIQUE=0
	`).Scan(&newIdx); err == nil && newIdx == 0 {
		_, _ = db.ExecContext(ctx, "ALTER TABLE sources ADD UNIQUE INDEX idx_user_source_key (user_id, source_key), ADD INDEX idx_sources_user (user_id)")
	}
	return nil
}
func Migrate(ctx context.Context, db *sql.DB) error {
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
func New(db *sql.DB, c Config, assets fs.FS) *Server {
	s := &Server{db: db, cfg: c, engine: engine.New(), mux: http.NewServeMux(), imports: make(chan struct{}, 1), search: make(chan struct{}, 4), attempts: map[string]attempt{}}
	s.routes()
	s.mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]string{"status": "ok", "version": Version})
	})
	s.mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
		defer c()
		var v int
		e := db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v)
		if e != nil || v != 1 {
			writeError(w, fail(503, "NOT_READY", "数据库未就绪或版本不兼容"))
			return
		}
		f, e := os.CreateTemp(filepath.Join(s.cfg.DataDir, "tmp"), "health-")
		if e != nil {
			writeError(w, fail(503, "NOT_READY", "存储不可写"))
			return
		}
		f.Close()
		os.Remove(f.Name())
		respond(w, map[string]string{"status": "ready"})
	})
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeError(w, fail(404, "NOT_FOUND", "接口不存在")) })
	if assets != nil {
		fileServer := http.FileServer(http.FS(assets))
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, fail(404, "NOT_FOUND", "接口不存在"))
				return
			}
			clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
			if clean != "" && clean != "." {
				if f, err := assets.Open(clean); err == nil {
					f.Close()
					if strings.HasPrefix(clean, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					fileServer.ServeHTTP(w, r)
					return
				}
			}
			req := r.Clone(r.Context())
			req.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			fileServer.ServeHTTP(w, req)
		})
	}
	return s
}
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdn.jsdelivr.net; img-src 'self' data: https:; connect-src 'self'; font-src 'self' data: https://fonts.gstatic.com https://cdn.jsdelivr.net; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		defer func() {
			if v := recover(); v != nil {
				slog.Error("request panic", "path", r.URL.Path)
				writeError(w, fail(500, "INTERNAL", "服务发生错误"))
			}
		}()
		s.mux.ServeHTTP(w, r)
	})
}

type handler func(http.ResponseWriter, *http.Request) error

func (s *Server) route(pattern string, auth, admin bool, h handler) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if r.Method != "GET" && r.Method != "HEAD" {
			if !s.sameOrigin(r) {
				writeError(w, fail(403, "ORIGIN", "请求来源不被允许"))
				return
			}
		}
		if auth {
			u, csrf, e := s.authenticate(r)
			if e != nil {
				writeError(w, e)
				return
			}
			if admin && u.Role != "admin" {
				writeError(w, fail(403, "FORBIDDEN", "需要管理员权限"))
				return
			}
			if u.MustChangePassword && r.URL.Path != "/api/v1/auth/password" && r.URL.Path != "/api/v1/auth/me" && r.URL.Path != "/api/v1/auth/logout" {
				writeError(w, fail(403, "PASSWORD_CHANGE_REQUIRED", "请先修改临时密码"))
				return
			}
			if r.Method != "GET" && r.Method != "HEAD" && !constantEqual(r.Header.Get("X-CSRF-Token"), csrf) {
				writeError(w, fail(403, "CSRF", "安全令牌已失效，请刷新页面"))
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), userKey{}, u))
		}
		if e := h(w, r); e != nil {
			writeError(w, e)
		}
	})
}
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	if s.cfg.PublicURL != "" {
		return origin == s.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	d := json.NewDecoder(r.Body)
	if e := d.Decode(v); e != nil {
		return fail(400, "INVALID_JSON", "请求 JSON 格式错误或过大")
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return fail(400, "INVALID_JSON", "请求包含额外内容")
	}
	return nil
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"data": v})
}
func respondWithMeta(w http.ResponseWriter, v any, meta any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"data": v, "meta": meta})
}
func writeError(w http.ResponseWriter, e error) {
	var ae *apiError
	if !errors.As(e, &ae) {
		slog.Error("request failed", "error", e.Error())
		ae = &apiError{500, "INTERNAL", "操作失败，请稍后重试"}
		var me *mysql.MySQLError
		if errors.As(e, &me) && me.Number == 1062 {
			ae = &apiError{409, "DUPLICATE", "记录已存在"}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(ae.status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": ae.code, "message": ae.message}})
}
func id() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func digest(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func (s *Server) audit(r *http.Request, action, target string) {
	u := current(r)
	if _, e := s.db.ExecContext(r.Context(), "INSERT INTO audit_logs(actor,action,target) VALUES(?,?,?)", u.Username, action, target); e != nil {
		slog.Error("audit failed", "action", action)
	}
}
