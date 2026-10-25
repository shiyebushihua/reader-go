package server

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in. Never runs against a configured database without this flag.
func TestMySQLReadingWorkflow(t *testing.T) {
	if os.Getenv("READER_INTEGRATION") != "1" {
		t.Skip("set READER_INTEGRATION=1 with a dedicated reader database")
	}
	db, e := OpenDB()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if e = Migrate(ctx, db); e != nil {
		t.Fatal(e)
	}
	uid, other := id(), id()
	name := "test_" + uid[:12]
	password := "test-" + id()
	hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if e != nil {
		t.Fatal(e)
	}
	for _, u := range []struct{ id, name string }{{uid, name}, {other, "test_" + other[:12]}} {
		if _, e = db.Exec("INSERT INTO users(id,username,password_hash,role,status,must_change) VALUES(?,?,?,'user','active',FALSE)", u.id, u.name, string(hash)); e != nil {
			t.Fatal(e)
		}
	}
	defer db.Exec("DELETE FROM users WHERE id IN (?,?)", uid, other)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tmp"), 0700)
	s := New(db, Config{DataDir: dir}, nil)
	call := func(method, path string, body io.Reader, cookies []*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, body)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
			if c.Name == "reader_csrf" {
				r.Header.Set("X-CSRF-Token", c.Value)
			}
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	loginBody, _ := json.Marshal(map[string]string{"username": name, "password": password})
	login := call("POST", "/api/v1/auth/login", bytes.NewReader(loginBody), nil)
	assertStatus(t, login, 200)
	cookies := login.Result().Cookies()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "测试小说.txt")
	part.Write([]byte("第一章 春天\n这是一段真实集成测试正文。\n\n第二章 远方\n第二章的内容。\n"))
	mw.Close()
	r := httptest.NewRequest("POST", "/api/v1/imports", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	for _, c := range cookies {
		r.AddCookie(c)
		if c.Name == "reader_csrf" {
			r.Header.Set("X-CSRF-Token", c.Value)
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	assertStatus(t, w, 200)
	var imported struct{ Data Book }
	if e = json.Unmarshal(w.Body.Bytes(), &imported); e != nil {
		t.Fatal(e)
	}
	bid := imported.Data.ID
	if bid == "" || imported.Data.ChapterCount < 2 {
		t.Fatalf("bad book: %s", w.Body.String())
	}
	list := call("GET", "/api/v1/books", nil, cookies)
	assertStatus(t, list, 200)
	chs := call("GET", "/api/v1/books/"+bid+"/chapters", nil, cookies)
	assertStatus(t, chs, 200)
	var chapterList struct{ Data []Chapter }
	json.Unmarshal(chs.Body.Bytes(), &chapterList)
	cid := chapterList.Data[0].ID
	content := call("GET", "/api/v1/books/"+bid+"/chapters/"+cid, nil, cookies)
	assertStatus(t, content, 200)
	if !bytes.Contains(content.Body.Bytes(), []byte("正文")) {
		t.Fatalf("missing content: %s", content.Body.String())
	}
	pbody, _ := json.Marshal(Progress{ChapterID: cid, Position: 0.45, Version: 0})
	assertStatus(t, call("PUT", "/api/v1/books/"+bid+"/progress", bytes.NewReader(pbody), cookies), 200)
	assertStatus(t, call("PUT", "/api/v1/books/"+bid+"/progress", bytes.NewReader(pbody), cookies), 409)
	bm, _ := json.Marshal(map[string]any{"chapterId": cid, "position": 0.45, "note": "测试书签"})
	assertStatus(t, call("POST", "/api/v1/books/"+bid+"/bookmarks", bytes.NewReader(bm), cookies), 200)
	otherBody, _ := json.Marshal(map[string]string{"username": "test_" + other[:12], "password": password})
	otherLogin := call("POST", "/api/v1/auth/login", bytes.NewReader(otherBody), nil)
	assertStatus(t, otherLogin, 200)
	assertStatus(t, call("GET", "/api/v1/books/"+bid, nil, otherLogin.Result().Cookies()), 404)
	assertStatus(t, call("GET", "/api/v1/admin/users", nil, cookies), 403)
	assertStatus(t, call("DELETE", "/api/v1/books/"+bid, nil, cookies), 200)
	if _, e = os.Stat(filepath.Join(dir, "books", bid)); !os.IsNotExist(e) {
		t.Fatal("files not removed")
	}
	assertStatus(t, call("POST", "/api/v1/auth/logout", nil, cookies), 200)
	assertStatus(t, call("GET", "/api/v1/books", nil, cookies), 401)
}

func TestMySQLAdminWorkflow(t *testing.T) {
	if os.Getenv("READER_INTEGRATION") != "1" {
		t.Skip("explicit integration opt-in required")
	}
	db, e := OpenDB()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	uid := id()
	name := "test_" + uid[:12]
	pw := "test-" + id()
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if _, e = db.Exec("INSERT INTO users(id,username,password_hash,role,status,must_change) VALUES(?,?,?,'admin','active',FALSE)", uid, name, string(hash)); e != nil {
		t.Fatal(e)
	}
	defer db.Exec("DELETE FROM users WHERE id=?", uid)
	defer db.Exec("DELETE FROM audit_logs WHERE actor=?", name)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tmp"), 0700)
	s := New(db, Config{DataDir: dir}, nil)
	call := func(method, path string, v any, cookies []*http.Cookie) *httptest.ResponseRecorder {
		body, _ := json.Marshal(v)
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.2:1234"
		for _, c := range cookies {
			r.AddCookie(c)
			if c.Name == "reader_csrf" {
				r.Header.Set("X-CSRF-Token", c.Value)
			}
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	login := call("POST", "/api/v1/auth/login", map[string]string{"username": name, "password": pw}, nil)
	assertStatus(t, login, 200)
	cookies := login.Result().Cookies()
	newName := "test_" + id()[:12]
	defer db.Exec("DELETE FROM users WHERE username=?", newName)
	created := call("POST", "/api/v1/admin/users", map[string]string{"username": newName, "password": pw, "role": "user"}, cookies)
	assertStatus(t, created, 200)
	var createdUser struct{ Data User }
	json.Unmarshal(created.Body.Bytes(), &createdUser)
	userLogin := call("POST", "/api/v1/auth/login", map[string]string{"username": newName, "password": pw}, nil)
	assertStatus(t, userLogin, 200)
	assertStatus(t, call("GET", "/api/v1/books", nil, userLogin.Result().Cookies()), 403)
	changed := call("PUT", "/api/v1/auth/password", map[string]string{"currentPassword": pw, "newPassword": pw + "x"}, userLogin.Result().Cookies())
	assertStatus(t, changed, 200)
	assertStatus(t, call("GET", "/api/v1/books", nil, changed.Result().Cookies()), 200)
	assertStatus(t, call("PATCH", "/api/v1/admin/users/"+createdUser.Data.ID, map[string]string{"status": "disabled"}, cookies), 200)
	assertStatus(t, call("GET", "/api/v1/books", nil, changed.Result().Cookies()), 401)
	assertStatus(t, call("POST", "/api/v1/admin/users/"+createdUser.Data.ID+"/reset-password", map[string]string{"password": pw + "z"}, cookies), 200)
	sourceURL := "https://" + id() + ".example.invalid"
	defer db.Exec("DELETE FROM sources WHERE source_key=?", digest(sourceURL))
	src := map[string]any{"bookSourceName": "集成测试源", "bookSourceUrl": sourceURL, "ruleSearch": map[string]string{"bookList": ".book", "name": "a@text", "bookUrl": "a@href"}}
	body := map[string]any{"sources": []any{src}, "overwrite": false}
	assertStatus(t, call("POST", "/api/v1/admin/sources/import", body, cookies), 200)
	duplicate := call("POST", "/api/v1/admin/sources/import", body, cookies)
	assertStatus(t, duplicate, 200)
	var imported struct {
		Data struct{ Imported, Skipped int }
	}
	json.Unmarshal(duplicate.Body.Bytes(), &imported)
	if imported.Data.Skipped != 1 || imported.Data.Imported != 0 {
		t.Fatal("duplicate not skipped")
	}
	assertStatus(t, call("GET", "/api/v1/admin/overview", nil, cookies), 200)
	assertStatus(t, call("GET", "/api/v1/admin/audit", nil, cookies), 200)
}
