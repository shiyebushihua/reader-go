package server

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type userKey struct{}
type User struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	Role               string    `json:"role"`
	Status             string    `json:"status"`
	MustChangePassword bool      `json:"mustChangePassword"`
	CreatedAt          time.Time `json:"createdAt"`
}

func current(r *http.Request) User { u, _ := r.Context().Value(userKey{}).(User); return u }
func constantEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_\-]{3,32}$`)

func validCredentials(username, password string) error {
	if !usernameRE.MatchString(username) {
		return fail(400, "USERNAME", "用户名需为 3–32 位字母、数字、下划线或横线")
	}
	return validPassword(password)
}
func validPassword(p string) error {
	if len(p) < 10 || len(p) > 72 {
		return fail(400, "PASSWORD", "密码需为 10–72 字节")
	}
	return nil
}
func (s *Server) authenticate(r *http.Request) (User, string, error) {
	var u User
	cookie, e := r.Cookie("reader_session")
	if e != nil {
		return u, "", fail(401, "UNAUTHORIZED", "请先登录")
	}
	var csrf string
	e = s.db.QueryRowContext(r.Context(), "SELECT u.id,u.username,u.role,u.status,u.must_change,u.created_at,s.csrf FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>UTC_TIMESTAMP() AND u.status='active'", digest(cookie.Value)).Scan(&u.ID, &u.Username, &u.Role, &u.Status, &u.MustChangePassword, &u.CreatedAt, &csrf)
	if errors.Is(e, sql.ErrNoRows) {
		return u, "", fail(401, "UNAUTHORIZED", "登录已过期")
	}
	return u, csrf, e
}
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, u User) error {
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE expires_at <= UTC_TIMESTAMP() LIMIT 100")
	token := id() + id()
	csrf := id() + id()
	_, e := s.db.ExecContext(r.Context(), "INSERT INTO sessions(token_hash,user_id,csrf,expires_at) VALUES(?,?,?,?)", digest(token), u.ID, csrf, time.Now().UTC().Add(7*24*time.Hour))
	if e != nil {
		return e
	}
	s.cookie(w, "reader_session", token, true, 604800)
	s.cookie(w, "reader_csrf", csrf, false, 604800)
	return nil
}
func (s *Server) cookie(w http.ResponseWriter, name, value string, httpOnly bool, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func clientHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return r.RemoteAddr
	}
	return host
}
func (s *Server) rateLimit(r *http.Request) bool {
	return s.rateLimitKey(clientHost(r))
}
func (s *Server) rateLimitKey(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var oldestKey string
	var oldestTime time.Time
	for k, a := range s.attempts {
		if now.After(a.Until) {
			delete(s.attempts, k)
			continue
		}
		if oldestKey == "" || a.Until.Before(oldestTime) {
			oldestKey = k
			oldestTime = a.Until
		}
	}
	if _, exists := s.attempts[key]; !exists && len(s.attempts) >= 10000 && oldestKey != "" {
		delete(s.attempts, oldestKey)
	}
	a := s.attempts[key]
	if a.Until.IsZero() || now.After(a.Until) {
		a = attempt{Count: 0, Until: now.Add(time.Minute)}
	}
	a.Count++
	s.attempts[key] = a
	return a.Count <= 10
}
func (s *Server) clearRateLimit(key string) {
	s.mu.Lock()
	delete(s.attempts, key)
	s.mu.Unlock()
}
func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) error {
	var n int
	if e := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users").Scan(&n); e != nil {
		return e
	}
	respond(w, map[string]bool{"required": n == 0})
	return nil
}
func (s *Server) setup(w http.ResponseWriter, r *http.Request) error {
	if !s.rateLimit(r) {
		return fail(429, "RATE_LIMIT", "尝试次数过多，请稍后重试")
	}
	var v struct{ Username, Password, SetupToken string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if len(s.cfg.SetupToken) < 24 || !constantEqual(v.SetupToken, s.cfg.SetupToken) {
		return fail(403, "SETUP_TOKEN", "初始化令牌不正确，请确认输入的令牌与服务端配置一致")
	}
	if e := validCredentials(v.Username, v.Password); e != nil {
		return e
	}
	conn, e := s.db.Conn(r.Context())
	if e != nil {
		return e
	}
	defer conn.Close()
	var lock int
	if e = conn.QueryRowContext(r.Context(), "SELECT GET_LOCK('reader_admin_setup',5)").Scan(&lock); e != nil {
		return e
	}
	if lock != 1 {
		return fail(409, "BUSY", "初始化正在执行")
	}
	defer conn.ExecContext(r.Context(), "SELECT RELEASE_LOCK('reader_admin_setup')")
	var n int
	if e = conn.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users").Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return fail(409, "INITIALIZED", "系统已经初始化")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(v.Password), 12)
	if e != nil {
		return e
	}
	u := User{ID: id(), Username: v.Username, Role: "admin", Status: "active", CreatedAt: time.Now().UTC()}
	_, e = conn.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash,role,status,must_change) VALUES(?,?,?,'admin','active',FALSE)", u.ID, u.Username, string(hash))
	if e != nil {
		return e
	}
	if e = s.issueSession(w, r, u); e != nil {
		return e
	}
	respond(w, u)
	return nil
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var v struct{ Username, Password string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	limitKey := clientHost(r) + "|" + strings.ToLower(strings.TrimSpace(v.Username))
	if !s.rateLimitKey(limitKey) {
		return fail(429, "RATE_LIMIT", "尝试次数过多，请稍后重试")
	}
	if len(v.Password) > 72 {
		return fail(401, "LOGIN", "用户名或密码错误")
	}
	var u User
	var hash string
	e := s.db.QueryRowContext(r.Context(), "SELECT id,username,role,status,must_change,created_at,password_hash FROM users WHERE username=?", v.Username).Scan(&u.ID, &u.Username, &u.Role, &u.Status, &u.MustChangePassword, &u.CreatedAt, &hash)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e != nil || u.Status != "active" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(v.Password)) != nil {
		return fail(401, "LOGIN", "用户名或密码错误")
	}
	s.clearRateLimit(limitKey)
	if e = s.issueSession(w, r, u); e != nil {
		return e
	}
	respond(w, u)
	return nil
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	c, _ := r.Cookie("reader_session")
	if c != nil {
		if _, e := s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", digest(c.Value)); e != nil {
			return e
		}
	}
	s.cookie(w, "reader_session", "", true, -1)
	s.cookie(w, "reader_csrf", "", false, -1)
	respond(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) password(w http.ResponseWriter, r *http.Request) error {
	var v struct{ CurrentPassword, NewPassword string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if e := validPassword(v.NewPassword); e != nil {
		return e
	}
	u := current(r)
	var hash string
	if e := s.db.QueryRowContext(r.Context(), "SELECT password_hash FROM users WHERE id=?", u.ID).Scan(&hash); e != nil {
		return e
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(v.CurrentPassword)) != nil {
		return fail(400, "PASSWORD", "当前密码错误")
	}
	b, e := bcrypt.GenerateFromPassword([]byte(v.NewPassword), 12)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(r.Context(), "UPDATE users SET password_hash=?,must_change=FALSE WHERE id=?", string(b), u.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id=?", u.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	u.MustChangePassword = false
	if e = s.issueSession(w, r, u); e != nil {
		return e
	}
	respond(w, u)
	return nil
}
func (s *Server) users(w http.ResponseWriter, r *http.Request) error {
	rows, e := s.db.QueryContext(r.Context(), "SELECT id,username,role,status,must_change,created_at FROM users ORDER BY created_at LIMIT 500")
	if e != nil {
		return e
	}
	defer rows.Close()
	list := []User{}
	for rows.Next() {
		var u User
		if e = rows.Scan(&u.ID, &u.Username, &u.Role, &u.Status, &u.MustChangePassword, &u.CreatedAt); e != nil {
			return e
		}
		list = append(list, u)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	respond(w, list)
	return nil
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) error {
	var v struct{ Username, Password, Role string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if e := validCredentials(v.Username, v.Password); e != nil {
		return e
	}
	if v.Role == "" {
		v.Role = "user"
	}
	if v.Role != "user" && v.Role != "admin" {
		return fail(400, "ROLE", "角色无效")
	}
	b, e := bcrypt.GenerateFromPassword([]byte(v.Password), 12)
	if e != nil {
		return e
	}
	u := User{ID: id(), Username: v.Username, Role: v.Role, Status: "active", MustChangePassword: true, CreatedAt: time.Now().UTC()}
	if _, e = s.db.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash,role) VALUES(?,?,?,?)", u.ID, u.Username, string(b), u.Role); e != nil {
		return e
	}
	s.audit(r, "user.create", u.ID)
	respond(w, u)
	return nil
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) error {
	var v struct{ Status, Role string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if v.Status != "" && v.Status != "active" && v.Status != "disabled" {
		return fail(400, "STATUS", "状态无效")
	}
	if v.Role != "" && v.Role != "admin" && v.Role != "user" {
		return fail(400, "ROLE", "角色无效")
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Lock all users in a stable order: serializes last-admin checks across requests.
	rows, e := tx.QueryContext(r.Context(), "SELECT id,role,status FROM users ORDER BY id FOR UPDATE")
	if e != nil {
		return e
	}
	admins := 0
	found := false
	var role, status string
	for rows.Next() {
		var i, rr, ss string
		if e = rows.Scan(&i, &rr, &ss); e != nil {
			rows.Close()
			return e
		}
		if rr == "admin" && ss == "active" {
			admins++
		}
		if i == r.PathValue("id") {
			found = true
			role = rr
			status = ss
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if !found {
		return fail(404, "NOT_FOUND", "用户不存在")
	}
	nr, ns := role, status
	if v.Role != "" {
		nr = v.Role
	}
	if v.Status != "" {
		ns = v.Status
	}
	if role == "admin" && status == "active" && admins <= 1 && (nr != "admin" || ns != "active") {
		return fail(409, "LAST_ADMIN", "不能禁用或降级最后一名管理员")
	}
	if _, e = tx.ExecContext(r.Context(), "UPDATE users SET role=?,status=? WHERE id=?", nr, ns, r.PathValue("id")); e != nil {
		return e
	}
	if _, e = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id=?", r.PathValue("id")); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit(r, "user.update", r.PathValue("id"))
	respond(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var v struct{ Password string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if e := validPassword(v.Password); e != nil {
		return e
	}
	b, e := bcrypt.GenerateFromPassword([]byte(v.Password), 12)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(r.Context(), "UPDATE users SET password_hash=?,must_change=TRUE WHERE id=?", string(b), r.PathValue("id"))
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "NOT_FOUND", "用户不存在")
	}
	if _, e = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id=?", r.PathValue("id")); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit(r, "user.reset_password", r.PathValue("id"))
	respond(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" {
		var b []byte
		if e := s.db.QueryRowContext(r.Context(), "SELECT settings FROM users WHERE id=?", current(r).ID).Scan(&b); e != nil {
			return e
		}
		v := map[string]any{}
		if len(b) > 0 {
			if e := json.Unmarshal(b, &v); e != nil {
				return e
			}
		}
		respond(w, v)
		return nil
	}
	var v map[string]any
	if e := decode(w, r, &v); e != nil {
		return e
	}
	allowed := map[string]bool{"theme": true, "fontSize": true, "lineHeight": true, "fontFamily": true, "width": true}
	for k, val := range v {
		if !allowed[k] {
			return fail(400, "SETTING", "不支持的阅读设置")
		}
		switch k {
		case "theme":
			str, ok := val.(string)
			if !ok || (str != "light" && str != "dark" && str != "sepia") {
				return fail(400, "SETTING", "主题无效")
			}
		case "fontFamily":
			str, ok := val.(string)
			if !ok || len(str) > 120 || strings.ContainsAny(str, "<>;{}") {
				return fail(400, "SETTING", "字体无效")
			}
		default:
			n, ok := val.(float64)
			min, max := 12.0, 40.0
			if k == "lineHeight" {
				min, max = 1.2, 3
			}
			if k == "width" {
				min, max = 320, 1200
			}
			if !ok || n < min || n > max {
				return fail(400, "SETTING", "阅读设置超出范围")
			}
		}
	}
	b, _ := json.Marshal(v)
	if _, e := s.db.ExecContext(r.Context(), "UPDATE users SET settings=? WHERE id=?", string(b), current(r).ID); e != nil {
		return e
	}
	respond(w, v)
	return nil
}
