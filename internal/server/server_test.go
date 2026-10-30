package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"readergo/internal/engine"
	"regexp"
	"testing"
	"time"
)

func testServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tmp"), 0700)
	return New(db, Config{DataDir: dir}, nil), m
}
func session(m sqlmock.Sqlmock, role string, must bool) {
	m.ExpectQuery("SELECT u.id,u.username,u.role").WithArgs(digest("session")).WillReturnRows(sqlmock.NewRows([]string{"id", "username", "role", "status", "must_change", "created_at", "csrf"}).AddRow("user1", "reader", role, "active", must, time.Now(), "csrf"))
}
func request(s *Server, method, path, body string, logged, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	if logged {
		r.AddCookie(&http.Cookie{Name: "reader_session", Value: "session"})
	}
	if csrf {
		r.Header.Set("X-CSRF-Token", "csrf")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func assertStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
}
func TestUnauthenticated(t *testing.T) {
	s, m := testServer(t)
	for _, p := range []string{"/api/v1/books", "/api/v1/admin/users", "/api/v1/sources"} {
		assertStatus(t, request(s, "GET", p, "", false, false), 401)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestAdminAuthorization(t *testing.T) {
	s, m := testServer(t)
	session(m, "user", false)
	assertStatus(t, request(s, "GET", "/api/v1/admin/users", "", true, false), 403)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestCSRF(t *testing.T) {
	s, m := testServer(t)
	session(m, "user", false)
	assertStatus(t, request(s, "PUT", "/api/v1/settings", `{"theme":"dark"}`, true, false), 403)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestForcedPassword(t *testing.T) {
	s, m := testServer(t)
	session(m, "user", true)
	assertStatus(t, request(s, "GET", "/api/v1/books", "", true, false), 403)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestCrossOrigin(t *testing.T) {
	s, m := testServer(t)
	r := httptest.NewRequest("POST", "http://reader.local/api/v1/auth/login", bytes.NewBufferString(`{}`))
	r.Header.Set("Origin", "https://evil.invalid")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	assertStatus(t, w, 403)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestCrossUserBook(t *testing.T) {
	s, m := testServer(t)
	session(m, "user", false)
	m.ExpectQuery(regexp.QuoteMeta(bookQuery+"WHERE b.id=? AND b.user_id=?")).WithArgs("someone-elses-book", "user1").WillReturnError(sql.ErrNoRows)
	assertStatus(t, request(s, "GET", "/api/v1/books/someone-elses-book", "", true, false), 404)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestProgressConflict(t *testing.T) {
	s, m := testServer(t)
	session(m, "user", false)
	m.ExpectBegin()
	m.ExpectQuery("SELECT id FROM books").WithArgs("b1", "user1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("b1"))
	m.ExpectQuery("SELECT id FROM chapters").WithArgs("c1", "b1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("c1"))
	m.ExpectQuery("SELECT version FROM progress").WithArgs("b1").WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(3))
	m.ExpectRollback()
	assertStatus(t, request(s, "PUT", "/api/v1/books/b1/progress", `{"chapterId":"c1","position":0.2,"version":1}`, true, true), 409)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestProgressInsert(t *testing.T) {
	s, m := testServer(t)
	session(m, "user", false)
	m.ExpectBegin()
	m.ExpectQuery("SELECT id FROM books").WithArgs("b1", "user1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("b1"))
	m.ExpectQuery("SELECT id FROM chapters").WithArgs("c1", "b1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("c1"))
	m.ExpectQuery("SELECT version FROM progress").WithArgs("b1").WillReturnError(sql.ErrNoRows)
	m.ExpectExec("INSERT INTO progress").WithArgs("b1", "c1", 0.2, int64(1)).WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	w := request(s, "PUT", "/api/v1/books/b1/progress", `{"chapterId":"c1","position":0.2,"version":0}`, true, true)
	assertStatus(t, w, 200)
	var result struct{ Data Progress }
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil || result.Data.Version != 1 {
		t.Fatal("incorrect version")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestLastAdmin(t *testing.T) {
	s, m := testServer(t)
	session(m, "admin", false)
	m.ExpectBegin()
	m.ExpectQuery("SELECT id,role,status FROM users ORDER BY id FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "role", "status"}).AddRow("user1", "admin", "active"))
	m.ExpectRollback()
	assertStatus(t, request(s, "PATCH", "/api/v1/admin/users/user1", `{"status":"disabled"}`, true, true), 409)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestInvalidSource(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"bookSourceName":"test","bookSourceUrl":"file:///etc/passwd"}`, `{"bookSourceName":"test","bookSourceUrl":"https://user:pass@example.com"}`} {
		if _, e := parseSource([]byte(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	v, e := parseSource([]byte(`{"bookSourceName":"test","bookSourceUrl":"https://example.com","enabled":false}`))
	if e != nil || v.Enabled {
		t.Fatal("source validation")
	}
}
func TestCredentials(t *testing.T) {
	if validCredentials("admin", "0123456789") != nil {
		t.Fatal("valid rejected")
	}
	for _, p := range []string{"short", string(bytes.Repeat([]byte("x"), 73))} {
		if validPassword(p) == nil {
			t.Fatal("invalid accepted")
		}
	}
	if constantEqual("", "") || constantEqual("x", "y") || !constantEqual("x", "x") {
		t.Fatal("constant compare")
	}
}
func TestDecodeRejectTrailing(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", bytes.NewBufferString(`{} {}`))
	var v map[string]any
	if decode(w, r, &v) == nil {
		t.Fatal("accepted trailing JSON")
	}
}
func TestHealthAndHeaders(t *testing.T) {
	s, _ := testServer(t)
	w := request(s, "GET", "/health/live", "", false, false)
	assertStatus(t, w, 200)
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP")
	}
}
func TestInvalidSetting(t *testing.T) {
	s, m := testServer(t)
	session(m, "user", false)
	assertStatus(t, request(s, "PUT", "/api/v1/settings", `{"fontSize":999}`, true, true), 400)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestSetupRequiresValidToken(t *testing.T) {
	s, m := testServer(t)
	assertStatus(t, request(s, "POST", "/api/v1/setup", `{"username":"admin","password":"password1234","setupToken":"short"}`, false, false), 403)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestUserScopedSources(t *testing.T) {
	s, m := testServer(t)
	// Regular non-admin user can import their own book source and cannot delete another user's source
	session(m, "user", false)
	m.ExpectBegin()
	m.ExpectQuery("SELECT id FROM sources WHERE user_id=\\? AND source_key=\\?").WithArgs("user1", digest("https://example.com")).WillReturnError(sql.ErrNoRows)
	m.ExpectExec("INSERT INTO sources\\(id,user_id,source_key,name,url,group_name,enabled,rule\\)").WithArgs(sqlmock.AnyArg(), "user1", digest("https://example.com"), "个人源", "https://example.com", "", true, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	m.ExpectExec("INSERT INTO audit_logs").WillReturnResult(sqlmock.NewResult(1, 1))
	assertStatus(t, request(s, "POST", "/api/v1/sources/import", `{"sources":[{"bookSourceName":"个人源","bookSourceUrl":"https://example.com"}],"overwrite":false}`, true, true), 200)

	session(m, "user", false)
	m.ExpectExec("DELETE FROM sources WHERE id=\\? AND user_id=\\?").WithArgs("other-user-source", "user1").WillReturnResult(sqlmock.NewResult(0, 0))
	assertStatus(t, request(s, "DELETE", "/api/v1/sources/other-user-source", "", true, true), 404)
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestSearchRelevanceFilterAndRanking(t *testing.T) {
	raw := []engine.Result{
		{SourceID: "s1", Title: "毫不相干的凑数书", Author: "路人甲", URL: "https://example.com/1"},
		{SourceID: "s2", Title: "凡人修仙传之仙界篇", Author: "忘语", URL: "https://example.com/2"},
		{SourceID: "s3", Title: "《凡人修仙传》", Author: "忘语", URL: "https://example.com/3"},
		{SourceID: "s4", Title: "修真杂谈", Author: "凡人修仙传粉丝", URL: "https://example.com/4"},
	}
	ranked := filterAndRankResults(raw, "凡人修仙传")
	if len(ranked) != 3 {
		t.Fatalf("expected 3 relevant results, got %d: %+v", len(ranked), ranked)
	}
	if ranked[0].URL != "https://example.com/3" || ranked[1].URL != "https://example.com/2" || ranked[2].URL != "https://example.com/4" {
		t.Fatalf("unexpected ranking order: %+v", ranked)
	}
}
