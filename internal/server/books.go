package server

import (
	"bytes"
	"context"
	"net/url"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"readergo/internal/engine"
	"readergo/internal/importer"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)


type Progress struct {
	ChapterID string  `json:"chapterId"`
	Position  float64 `json:"position"`
	Version   int64   `json:"version"`
}
type Book struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Author       string    `json:"author"`
	Cover        string    `json:"cover"`
	Intro        string    `json:"intro"`
	Kind         string    `json:"kind"`
	Group        string    `json:"group"`
	SourceID     string    `json:"sourceId"`
	URL          string    `json:"url"`
	ChapterCount int       `json:"chapterCount"`
	Progress     *Progress `json:"progress"`
	CreatedAt    time.Time `json:"createdAt"`
	SourceName   string    `json:"sourceName,omitempty"`
}
type Chapter struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Index  int    `json:"index"`
	Cached bool   `json:"cached"`
}

const bookQuery = "SELECT b.id,b.title,b.author,b.cover,b.intro,b.kind,b.group_name,COALESCE(b.source_id,''),b.url,b.created_at,(SELECT COUNT(*) FROM chapters c WHERE c.book_id=b.id),p.chapter_id,p.position,p.version FROM books b LEFT JOIN progress p ON p.book_id=b.id "

type scanner interface{ Scan(...any) error }

func scanBook(row scanner) (Book, error) {
	var b Book
	var ch sql.NullString
	var pos sql.NullFloat64
	var ver sql.NullInt64
	e := row.Scan(&b.ID, &b.Title, &b.Author, &b.Cover, &b.Intro, &b.Kind, &b.Group, &b.SourceID, &b.URL, &b.CreatedAt, &b.ChapterCount, &ch, &pos, &ver)
	if ch.Valid {
		b.Progress = &Progress{ch.String, pos.Float64, ver.Int64}
	}
	return b, e
}
func (s *Server) book(ctx context.Context, uid, bid string) (Book, error) {
	b, e := scanBook(s.db.QueryRowContext(ctx, bookQuery+"WHERE b.id=? AND b.user_id=?", bid, uid))
	if errors.Is(e, sql.ErrNoRows) {
		return b, fail(404, "BOOK_NOT_FOUND", "书籍不存在")
	}
	return b, e
}
func (s *Server) books(w http.ResponseWriter, r *http.Request) error {
	rows, e := s.db.QueryContext(r.Context(), bookQuery+"WHERE b.user_id=? ORDER BY COALESCE(p.updated_at,b.created_at) DESC LIMIT 1000", current(r).ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	list := []Book{}
	for rows.Next() {
		b, e := scanBook(rows)
		if e != nil {
			return e
		}
		list = append(list, b)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if len(list) > 0 {
		sids := make([]string, 0, len(list))
		for _, b := range list {
			if b.SourceID != "" {
				sids = append(sids, b.SourceID)
			}
		}
		if len(sids) > 0 {
			nameMap := s.resolveSourceNames(r.Context(), sids)
			for i := range list {
				if name, ok := nameMap[list[i].SourceID]; ok && name != "" {
					list[i].SourceName = name
				} else if list[i].Kind == "local" {
					list[i].SourceName = "本地导入"
				}
			}
		} else {
			for i := range list {
				if list[i].Kind == "local" {
					list[i].SourceName = "本地导入"
				}
			}
		}
	}
	respond(w, list)
	return nil
}
func (s *Server) resolveSourceNames(ctx context.Context, sourceIDs []string) map[string]string {
	res := make(map[string]string)
	if len(sourceIDs) == 0 {
		return res
	}
	seen := make(map[string]bool)
	uniqueIDs := make([]string, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	if len(uniqueIDs) == 0 {
		return res
	}
	placeholders := strings.Repeat("?,", len(uniqueIDs))
	placeholders = strings.TrimSuffix(placeholders, ",")
	query := "SELECT id, name FROM sources WHERE id IN (" + placeholders + ")"
	args := make([]any, len(uniqueIDs))
	for i, id := range uniqueIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var sid, name string
			if err := rows.Scan(&sid, &name); err == nil {
				res[sid] = name
			}
		}
	}
	return res
}
func (s *Server) getBook(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if b.SourceID != "" {
		names := s.resolveSourceNames(r.Context(), []string{b.SourceID})
		if n, ok := names[b.SourceID]; ok && n != "" {
			b.SourceName = n
		}
	} else if b.Kind == "local" {
		b.SourceName = "本地导入"
	}
	respond(w, b)
	return nil
	return nil
}
func (s *Server) updateBook(w http.ResponseWriter, r *http.Request) error {
	if _, e := s.book(r.Context(), current(r).ID, r.PathValue("id")); e != nil {
		return e
	}
	var v struct{ Group string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if len(v.Group) > 255 {
		return fail(400, "GROUP", "分组名称过长")
	}
	if _, e := s.db.ExecContext(r.Context(), "UPDATE books SET group_name=? WHERE id=? AND user_id=?", v.Group, r.PathValue("id"), current(r).ID); e != nil {
		return e
	}
	return s.getBook(w, r)
}
func (s *Server) deleteBook(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if _, e = s.db.ExecContext(r.Context(), "DELETE FROM books WHERE id=? AND user_id=?", b.ID, current(r).ID); e != nil {
		return e
	}
	_ = os.RemoveAll(filepath.Join(s.cfg.DataDir, "cache", "books", b.ID))
	if e = os.RemoveAll(filepath.Join(s.cfg.DataDir, "books", b.ID)); e != nil {
		return fail(500, "FILE_CLEANUP", "记录已移除，但文件清理失败，请联系管理员")
	}
	respond(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) chapters(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	rows, e := s.db.QueryContext(r.Context(), "SELECT id,title,ordinal FROM chapters WHERE book_id=? ORDER BY ordinal LIMIT 20000", b.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	cachedMap := make(map[string]bool)
	targetDir := filepath.Join(s.cfg.DataDir, "cache", "books", b.ID)
	if b.Kind != "online" {
		targetDir = filepath.Join(s.cfg.DataDir, "books", b.ID)
	}
	if entries, err := os.ReadDir(targetDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
				cid := strings.TrimSuffix(entry.Name(), ".txt")
				cachedMap[cid] = true
			}
		}
	}
	list := []Chapter{}
	for rows.Next() {
		var c Chapter
		if e = rows.Scan(&c.ID, &c.Title, &c.Index); e != nil {
			return e
		}
		if cachedMap[c.ID] {
			c.Cached = true
		}
		list = append(list, c)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	respond(w, list)
	return nil
}
func (s *Server) content(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	var ch Chapter
	var url string
	e = s.db.QueryRowContext(r.Context(), "SELECT id,title,ordinal,url FROM chapters WHERE id=? AND book_id=?", r.PathValue("chapterId"), b.ID).Scan(&ch.ID, &ch.Title, &ch.Index, &url)
	if errors.Is(e, sql.ErrNoRows) {
		return fail(404, "CHAPTER_NOT_FOUND", "章节不存在")
	}
	if e != nil {
		return e
	}
	var content string
	if b.Kind == "online" {
		cacheFile := filepath.Join(s.cfg.DataDir, "cache", "books", b.ID, ch.ID+".txt")
		cachedData, readErr := os.ReadFile(cacheFile)
		hasIncomplete := readErr == nil && len(cachedData) > 0 && isIncompleteChapterText(string(cachedData))
		if readErr == nil && len(cachedData) > 0 && !hasIncomplete {
			content = string(cachedData)
		} else {
			src, e := s.source(r.Context(), current(r).ID, b.SourceID, true)
			if e != nil {
				if len(cachedData) > 0 {
					content = string(cachedData)
				} else {
					return e
				}
			} else {
				var nextChapterURL string
				_ = s.db.QueryRowContext(r.Context(), "SELECT url FROM chapters WHERE book_id=? AND ordinal > ? ORDER BY ordinal ASC LIMIT 1", b.ID, ch.Index).Scan(&nextChapterURL)
				content, e = s.engine.Content(r.Context(), engine.Source{ID: src.ID, Rule: src.Rule}, url, nextChapterURL)
				if e != nil {
					if len(cachedData) > 0 {
						content = string(cachedData)
					} else {
						return fail(422, "CONTENT", "正文抓取失败，书源可能已失效或使用不支持的规则")
					}
				} else {
					_ = os.MkdirAll(filepath.Dir(cacheFile), 0700)
					_ = os.WriteFile(cacheFile, []byte(content), 0600)
				}
			}
		}
		content = engine.PurifyContent(content)
		go s.preloadUpcomingChapters(b.ID, b.SourceID, ch.Index, 3)
	} else {
		f, e := os.Open(filepath.Join(s.cfg.DataDir, "books", b.ID, ch.ID+".txt"))
		if e != nil {
			return fail(404, "FILE_NOT_FOUND", "章节文件不存在，请重新导入")
		}
		defer f.Close()
		data, e := io.ReadAll(io.LimitReader(f, 10<<20))
		if e != nil {
			return e
		}
		content = string(data)
	}
	respond(w, map[string]any{"id": ch.ID, "title": ch.Title, "content": content, "format": "text"})
	return nil
}
func (s *Server) onlineBook(w http.ResponseWriter, r *http.Request) error {
	var v struct {
		SourceID                         string `json:"sourceId"`
		URL, Title, Author, Cover, Intro string
	}
	if e := decode(w, r, &v); e != nil {
		return e
	}
	src, e := s.source(r.Context(), current(r).ID, v.SourceID, true)
	if e != nil {
		return e
	}
	if len(v.URL) > 4096 || v.URL == "" {
		return fail(400, "URL", "书籍地址无效")
	}
	detail, e := s.engine.Details(r.Context(), engine.Source{ID: src.ID, Rule: src.Rule}, v.URL)
	if e != nil {
		return fail(422, "DETAILS", "详情或目录解析失败，请检查书源兼容情况")
	}
	if detail.Title == "" {
		detail.Title = v.Title
	}
	if detail.Author == "" {
		detail.Author = v.Author
	}
	if detail.Cover == "" {
		detail.Cover = v.Cover
	}
	if detail.Intro == "" {
		detail.Intro = v.Intro
	}
	b := Book{ID: id(), Title: detail.Title, Author: detail.Author, Cover: detail.Cover, Intro: detail.Intro, Kind: "online", SourceID: src.ID, URL: v.URL, CreatedAt: time.Now().UTC(), ChapterCount: len(detail.Chapters)}
	if e = validateBook(b); e != nil {
		return e
	}
	if len(detail.Chapters) == 0 || len(detail.Chapters) > 20000 {
		return fail(422, "CHAPTERS", "目录为空或章节过多")
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = insertBook(r.Context(), tx, current(r).ID, b, digest(src.ID+"\n"+v.URL)); e != nil {
		return e
	}
	records := make([]chapterRecord, len(detail.Chapters))
	for i, c := range detail.Chapters {
		records[i] = chapterRecord{ID: id(), Ordinal: i, Title: c.Title, URL: c.URL}
	}
	if e = batchInsertChapterRecords(r.Context(), tx, b.ID, records); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, b)
	return nil
}
func validateBook(b Book) error {
	if b.Title == "" || len(b.Title) > 2000 || len(b.Author) > 1000 || len(b.Intro) > 1<<20 || len(b.Cover) > 4096 {
		return fail(400, "BOOK_FIELDS", "书籍元信息无效或过长")
	}
	return nil
}
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func insertBook(ctx context.Context, tx *sql.Tx, uid string, b Book, identity string) error {
	var sid any
	if b.SourceID != "" {
		sid = b.SourceID
	}
	_, e := tx.ExecContext(ctx, "INSERT INTO books(id,user_id,title,author,cover,intro,kind,source_id,url,identity_hash) VALUES(?,?,?,?,?,?,?,?,?,?)", b.ID, uid, clip(b.Title, 500), clip(b.Author, 250), b.Cover, b.Intro, b.Kind, sid, b.URL, identity)
	return e
}

type chapterRecord struct {
	ID      string
	Ordinal int
	Title   string
	URL     string
}

func batchInsertChapterRecords(ctx context.Context, tx *sql.Tx, bookID string, records []chapterRecord) error {
	const batchSize = 500
	for i := 0; i < len(records); i += batchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := i + batchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[i:end]
		var query strings.Builder
		query.WriteString("INSERT INTO chapters(id,book_id,ordinal,title,url) VALUES ")
		args := make([]any, 0, len(batch)*5)
		for j, c := range batch {
			if j > 0 {
				query.WriteString(",")
			}
			query.WriteString("(?,?,?,?,?)")
			args = append(args, c.ID, bookID, c.Ordinal, clip(c.Title, 1000), c.URL)
		}
		if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) importBook(w http.ResponseWriter, r *http.Request) error {
	select {
	case s.imports <- struct{}{}:
		defer func() { <-s.imports }()
	default:
		return fail(429, "IMPORT_BUSY", "已有导入正在处理，请稍后重试")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 101<<20)
	mr, e := r.MultipartReader()
	if e != nil {
		return fail(400, "UPLOAD", "请使用文件上传")
	}
	var tmp *os.File
	var filename string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(413, "UPLOAD_LIMIT", "上传格式错误或超过大小限制")
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		filename = part.FileName()
		ext := strings.ToLower(filepath.Ext(filename))
		if ext != ".txt" && ext != ".epub" {
			part.Close()
			return fail(400, "FORMAT", "仅支持 TXT 和 EPUB")
		}
		tmp, e = os.CreateTemp(filepath.Join(s.cfg.DataDir, "tmp"), "upload-*")
		if e != nil {
			return e
		}
		defer os.Remove(tmp.Name())
		n, err := io.Copy(tmp, io.LimitReader(part, (100<<20)+1))
		tmp.Close()
		part.Close()
		if err != nil || n > 100<<20 {
			return fail(413, "UPLOAD_LIMIT", "文件超过 100 MiB 或上传中断")
		}
		break
	}
	if tmp == nil {
		return fail(400, "UPLOAD", "请选择文件")
	}
	parsed, e := importer.Parse(r.Context(), tmp.Name(), filename)
	if e != nil {
		return fail(422, "IMPORT_PARSE", "解析失败："+clip(e.Error(), 250))
	}
	if len(parsed.Chapters) == 0 || len(parsed.Chapters) > 20000 {
		return fail(422, "CHAPTERS", "章节数量无效")
	}
	b := Book{ID: id(), Title: parsed.Title, Author: parsed.Author, Kind: parsed.Kind, ChapterCount: len(parsed.Chapters), CreatedAt: time.Now().UTC()}
	if e = validateBook(b); e != nil {
		return e
	}
	file, e := os.Open(tmp.Name())
	if e != nil {
		return e
	}
	hash := sha256.New()
	_, e = io.Copy(hash, file)
	file.Close()
	if e != nil {
		return e
	}
	identity := hex.EncodeToString(hash.Sum(nil))
	dir := filepath.Join(s.cfg.DataDir, "books", b.ID)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(dir)
		}
	}()
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = insertBook(r.Context(), tx, current(r).ID, b, identity); e != nil {
		return e
	}
	records := make([]chapterRecord, len(parsed.Chapters))
	for i, c := range parsed.Chapters {
		if e = r.Context().Err(); e != nil {
			return e
		}
		cid := id()
		if len(c.Content) > 10<<20 {
			return fail(422, "CHAPTER_SIZE", "单章超过 10 MiB 限制")
		}
		if e = os.WriteFile(filepath.Join(dir, cid+".txt"), []byte(c.Content), 0600); e != nil {
			return e
		}
		records[i] = chapterRecord{ID: cid, Ordinal: i, Title: c.Title, URL: ""}
	}
	if e = batchInsertChapterRecords(r.Context(), tx, b.ID, records); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	committed = true
	respond(w, b)
	return nil
}
func (s *Server) refreshBook(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if b.Kind != "online" {
		return fail(400, "LOCAL_BOOK", "本地书籍无需刷新")
	}
	src, e := s.source(r.Context(), current(r).ID, b.SourceID, true)
	if e != nil {
		return e
	}
	detail, e := s.engine.Details(r.Context(), engine.Source{ID: src.ID, Rule: src.Rule}, b.URL)
	if e != nil {
		return fail(422, "REFRESH", "目录刷新失败")
	}
	if len(detail.Chapters) == 0 || len(detail.Chapters) > 20000 {
		return fail(422, "CHAPTERS", "目录数量无效")
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var lock string
	if e = tx.QueryRowContext(r.Context(), "SELECT id FROM books WHERE id=? AND user_id=? FOR UPDATE", b.ID, current(r).ID).Scan(&lock); e != nil {
		return e
	}
	rows, e := tx.QueryContext(r.Context(), "SELECT id,url,title,ordinal FROM chapters WHERE book_id=? ORDER BY ordinal", b.ID)
	if e != nil {
		return e
	}
	existing := map[string]string{}
	oldCount := 0
	for rows.Next() {
		var cid, url, title string
		var ord int
		if e = rows.Scan(&cid, &url, &title, &ord); e != nil {
			rows.Close()
			return e
		}
		existing[url] = cid
		oldCount++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(r.Context(), "UPDATE chapters SET ordinal=ordinal+100000 WHERE book_id=?", b.ID); e != nil {
		return e
	}
	kept := map[string]bool{}
	for i, c := range detail.Chapters {
		cid := existing[c.URL]
		if cid != "" && !kept[cid] {
			_, e = tx.ExecContext(r.Context(), "UPDATE chapters SET ordinal=?,title=? WHERE id=?", i, clip(c.Title, 1000), cid)
			kept[cid] = true
		} else {
			cid = id()
			_, e = tx.ExecContext(r.Context(), "INSERT INTO chapters(id,book_id,ordinal,title,url) VALUES(?,?,?,?,?)", cid, b.ID, i, clip(c.Title, 1000), c.URL)
		}
		if e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(r.Context(), "DELETE FROM bookmarks WHERE book_id=? AND chapter_id IN (SELECT id FROM chapters WHERE book_id=? AND ordinal>=100000)", b.ID, b.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(r.Context(), "DELETE FROM progress WHERE book_id=? AND chapter_id IN (SELECT id FROM chapters WHERE book_id=? AND ordinal>=100000)", b.ID, b.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(r.Context(), "DELETE FROM chapters WHERE book_id=? AND ordinal>=100000", b.ID); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, map[string]int{"chapterCount": len(detail.Chapters), "previousCount": oldCount})
	return nil
}
func (s *Server) changeBookSource(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		SourceID            string `json:"sourceId"`
		URL                 string `json:"url"`
		CurrentChapterTitle string `json:"currentChapterTitle"`
	}
	if e := decode(w, r, &req); e != nil {
		return e
	}
	if req.SourceID == "" || req.URL == "" {
		return fail(400, "PARAM", "书源ID与书籍URL不能为空")
	}
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if b.Kind != "online" {
		return fail(400, "LOCAL_BOOK", "本地导入书籍不支持在线换源")
	}
	src, e := s.source(r.Context(), current(r).ID, req.SourceID, true)
	if e != nil {
		return e
	}
	detail, e := s.engine.Details(r.Context(), engine.Source{ID: src.ID, Rule: src.Rule}, req.URL)
	if e != nil {
		return fail(422, "DETAILS", "新书源目录解析失败："+clip(e.Error(), 250))
	}
	if len(detail.Chapters) == 0 || len(detail.Chapters) > 20000 {
		return fail(422, "CHAPTERS", "新书源目录为空或章节过多")
	}

	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()

	var lock string
	if e = tx.QueryRowContext(r.Context(), "SELECT id FROM books WHERE id=? AND user_id=? FOR UPDATE", b.ID, current(r).ID).Scan(&lock); e != nil {
		return e
	}
	identity := digest(src.ID + "\n" + req.URL)
	var dupID string
	if err := tx.QueryRowContext(r.Context(), "SELECT id FROM books WHERE user_id=? AND identity_hash=? AND id!=?", current(r).ID, identity, b.ID).Scan(&dupID); err == nil {
		return fail(409, "DUPLICATE", "该书源的同一本书已在书架中")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	currentTitle := strings.TrimSpace(req.CurrentChapterTitle)
	var curOrdinal int = -1
	if currentTitle == "" && b.Progress != nil && b.Progress.ChapterID != "" {
		_ = tx.QueryRowContext(r.Context(), "SELECT title, ordinal FROM chapters WHERE id=? AND book_id=?", b.Progress.ChapterID, b.ID).Scan(&currentTitle, &curOrdinal)
	}

	if _, e = tx.ExecContext(r.Context(), "DELETE FROM chapters WHERE book_id=?", b.ID); e != nil {
		return e
	}

	records := make([]chapterRecord, len(detail.Chapters))
	for i, c := range detail.Chapters {
		records[i] = chapterRecord{ID: id(), Ordinal: i, Title: c.Title, URL: c.URL}
	}
	if e = batchInsertChapterRecords(r.Context(), tx, b.ID, records); e != nil {
		return e
	}

	newCover := b.Cover
	if newCover == "" {
		newCover = detail.Cover
	}
	newIntro := b.Intro
	if newIntro == "" {
		newIntro = detail.Intro
	}
	if _, e = tx.ExecContext(r.Context(), "UPDATE books SET source_id=?,url=?,identity_hash=?,kind='online',cover=?,intro=? WHERE id=?", src.ID, req.URL, identity, newCover, newIntro, b.ID); e != nil {
		return e
	}

	var targetChapterID string
	cleanTitle := func(t string) string {
		t = strings.TrimSpace(t)
		t = strings.ReplaceAll(t, " ", "")
		t = strings.ReplaceAll(t, "　", "")
		return strings.ToLower(t)
	}
	targetClean := cleanTitle(currentTitle)

	if targetClean != "" {
		for _, rec := range records {
			if cleanTitle(rec.Title) == targetClean {
				targetChapterID = rec.ID
				break
			}
		}
	}
	if targetChapterID == "" && targetClean != "" {
		for _, rec := range records {
			rc := cleanTitle(rec.Title)
			if strings.Contains(rc, targetClean) || strings.Contains(targetClean, rc) {
				targetChapterID = rec.ID
				break
			}
		}
	}
	if targetChapterID == "" && curOrdinal >= 0 && curOrdinal < len(records) {
		targetChapterID = records[curOrdinal].ID
	}
	if targetChapterID == "" && len(records) > 0 {
		targetChapterID = records[0].ID
	}

	pos := 0.0
	if b.Progress != nil {
		pos = b.Progress.Position
	}
	var oldVersion int64
	_ = tx.QueryRowContext(r.Context(), "SELECT version FROM progress WHERE book_id=?", b.ID).Scan(&oldVersion)
	newVersion := oldVersion + 1
	if oldVersion == 0 {
		_, e = tx.ExecContext(r.Context(), "INSERT INTO progress(book_id,chapter_id,position,version) VALUES(?,?,?,?)", b.ID, targetChapterID, pos, newVersion)
	} else {
		_, e = tx.ExecContext(r.Context(), "UPDATE progress SET chapter_id=?,position=?,version=? WHERE book_id=?", targetChapterID, pos, newVersion, b.ID)
	}
	if e != nil {
		return e
	}

	if e = tx.Commit(); e != nil {
		return e
	}
	_ = os.RemoveAll(filepath.Join(s.cfg.DataDir, "cache", "books", b.ID))

	respond(w, map[string]any{
		"ok":               true,
		"currentChapterId": targetChapterID,
		"chapterCount":     len(records),
		"sourceName":       src.Name,
	})
	return nil
}
func (s *Server) getProgress(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if b.Progress == nil {
		respond(w, Progress{})
	} else {
		respond(w, b.Progress)
	}
	return nil
}
func (s *Server) putProgress(w http.ResponseWriter, r *http.Request) error {
	var v Progress
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if math.IsNaN(v.Position) || math.IsInf(v.Position, 0) || v.Position < 0 || v.Position > 1 || v.Version < 0 {
		return fail(400, "PROGRESS", "进度值无效")
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var bid string
	e = tx.QueryRowContext(r.Context(), "SELECT id FROM books WHERE id=? AND user_id=? FOR UPDATE", r.PathValue("id"), current(r).ID).Scan(&bid)
	if errors.Is(e, sql.ErrNoRows) {
		return fail(404, "NOT_FOUND", "书籍不存在")
	}
	if e != nil {
		return e
	}
	var cid string
	if e = tx.QueryRowContext(r.Context(), "SELECT id FROM chapters WHERE id=? AND book_id=?", v.ChapterID, bid).Scan(&cid); errors.Is(e, sql.ErrNoRows) {
		return fail(400, "CHAPTER", "章节不属于此书籍")
	}
	if e != nil {
		return e
	}
	var old int64
	e = tx.QueryRowContext(r.Context(), "SELECT version FROM progress WHERE book_id=?", bid).Scan(&old)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if old != v.Version {
		return fail(409, "PROGRESS_CONFLICT", "其他设备已更新进度，请刷新后重试")
	}
	v.Version = old + 1
	if old == 0 {
		_, e = tx.ExecContext(r.Context(), "INSERT INTO progress(book_id,chapter_id,position,version) VALUES(?,?,?,?)", bid, v.ChapterID, v.Position, v.Version)
	} else {
		_, e = tx.ExecContext(r.Context(), "UPDATE progress SET chapter_id=?,position=?,version=? WHERE book_id=?", v.ChapterID, v.Position, v.Version, bid)
	}
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	respond(w, v)
	return nil
}
func (s *Server) bookmarks(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if r.Method == "POST" {
		var v struct {
			ChapterID string `json:"chapterId"`
			Position  float64
			Note      string
		}
		if e = decode(w, r, &v); e != nil {
			return e
		}
		if v.Position < 0 || v.Position > 1 || len([]rune(v.Note)) > 1000 {
			return fail(400, "BOOKMARK", "书签参数无效")
		}
		var n int
		if e = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM chapters WHERE id=? AND book_id=?", v.ChapterID, b.ID).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			return fail(400, "CHAPTER", "章节不存在")
		}
		iid := id()
		if _, e = s.db.ExecContext(r.Context(), "INSERT INTO bookmarks(id,book_id,chapter_id,position,note) VALUES(?,?,?,?,?)", iid, b.ID, v.ChapterID, v.Position, v.Note); e != nil {
			return e
		}
		var ctitle string
		_ = s.db.QueryRowContext(r.Context(), "SELECT title FROM chapters WHERE id=? AND book_id=?", v.ChapterID, b.ID).Scan(&ctitle)
		respond(w, map[string]any{"id": iid, "chapterId": v.ChapterID, "chapterTitle": ctitle, "position": v.Position, "note": v.Note})
		return nil
	}
	rows, e := s.db.QueryContext(r.Context(), "SELECT b.id, b.chapter_id, COALESCE(c.title, ''), b.position, b.note FROM bookmarks b LEFT JOIN chapters c ON b.chapter_id=c.id WHERE b.book_id=? ORDER BY b.created_at DESC LIMIT 1000", b.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var iid, cid, ctitle, note string
		var pos float64
		if e = rows.Scan(&iid, &cid, &ctitle, &pos, &note); e != nil {
			return e
		}
		list = append(list, map[string]any{"id": iid, "chapterId": cid, "chapterTitle": ctitle, "position": pos, "note": note})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	respond(w, list)
	return nil
}
func (s *Server) deleteBookmark(w http.ResponseWriter, r *http.Request) error {
	res, e := s.db.ExecContext(r.Context(), "DELETE FROM bookmarks WHERE id=? AND book_id IN (SELECT id FROM books WHERE user_id=?)", r.PathValue("id"), current(r).ID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "NOT_FOUND", "书签不存在")
	}
	respond(w, map[string]bool{"ok": true})
	return nil
}

type BookSourceCandidate struct {
	SourceID      string `json:"sourceId"`
	SourceName    string `json:"sourceName"`
	BookURL       string `json:"bookUrl"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	LatestChapter string `json:"latestChapter"`
	Cover         string `json:"cover"`
	IsCurrent     bool   `json:"isCurrent"`
}

func (s *Server) bookSources(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if b.Kind != "online" {
		return fail(400, "LOCAL_BOOK", "本地导入书籍不支持在线换源")
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	uid := current(r).ID
	sources, err := s.resolveSearchSources(ctx, uid, nil, 1000)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		respond(w, []BookSourceCandidate{})
		return nil
	}

	keyword := strings.TrimSpace(b.Title)
	if keyword == "" {
		return fail(400, "PARAM", "书名为空无法换源搜索")
	}

	candidates := []BookSourceCandidate{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)

	for _, src := range sources {
		wg.Add(1)
		go func(v Source) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			srcCtx, srcCancel := context.WithTimeout(ctx, 12*time.Second)
			items, err := s.engine.Search(srcCtx, engine.Source{ID: v.ID, Rule: v.Rule}, keyword)
			srcCancel()
			if err != nil || len(items) == 0 {
				return
			}

			cleanBookTitle := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(b.Title, " ", ""), "　", ""))
			for _, item := range items {
				itemTitle := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(item.Title, " ", ""), "　", ""))
				if itemTitle != cleanBookTitle && !strings.Contains(itemTitle, cleanBookTitle) && !strings.Contains(cleanBookTitle, itemTitle) {
					continue
				}
				isCur := (v.ID == b.SourceID && item.URL == b.URL)
				cand := BookSourceCandidate{
					SourceID:      v.ID,
					SourceName:    v.Name,
					BookURL:       item.URL,
					Title:         item.Title,
					Author:        item.Author,
					LatestChapter: item.LatestChapter,
					Cover:         item.Cover,
					IsCurrent:     isCur,
				}
				mu.Lock()
				candidates = append(candidates, cand)
				mu.Unlock()
			}
		}(src)
	}
	wg.Wait()

	foundCurrent := false
	for _, c := range candidates {
		if c.IsCurrent {
			foundCurrent = true
			break
		}
	}
	if !foundCurrent && b.SourceID != "" {
		if curSrc, err := s.source(r.Context(), uid, b.SourceID, false); err == nil {
			candidates = append([]BookSourceCandidate{{
				SourceID:   curSrc.ID,
				SourceName: curSrc.Name,
				BookURL:    b.URL,
				Title:      b.Title,
				Author:     b.Author,
				Cover:      b.Cover,
				IsCurrent:  true,
			}}, candidates...)
		}
	}
	// 排序：当前书源最前，其余按是否有最新章节及名称匹配度排序
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].IsCurrent != candidates[j].IsCurrent {
			return candidates[i].IsCurrent
		}
		hasLatestI := candidates[i].LatestChapter != ""
		hasLatestJ := candidates[j].LatestChapter != ""
		if hasLatestI != hasLatestJ {
			return hasLatestI
		}
		return candidates[i].SourceName < candidates[j].SourceName
	})

	respond(w, candidates)
	return nil
}

type CacheChaptersReq struct {
	FromOrdinal int      `json:"fromOrdinal"`
	Count       int      `json:"count"`
	ChapterIDs  []string `json:"chapterIds"`
}

func (s *Server) cacheChapters(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	if b.Kind != "online" {
		respond(w, map[string]any{"ok": true, "cached": 0, "message": "本地书籍已全部在本地存储"})
		return nil
	}

	var req CacheChaptersReq
	_ = decode(w, r, &req)

	if req.Count <= 0 {
		req.Count = 50
	}
	if req.Count > 200 {
		req.Count = 200
	}

	var rows *sql.Rows
	if len(req.ChapterIDs) > 0 {
		placeholders := make([]string, len(req.ChapterIDs))
		args := make([]any, len(req.ChapterIDs)+1)
		args[0] = b.ID
		for i, cid := range req.ChapterIDs {
			placeholders[i] = "?"
			args[i+1] = cid
		}
		query := fmt.Sprintf("SELECT id,ordinal,title,url FROM chapters WHERE book_id=? AND id IN (%s) ORDER BY ordinal", strings.Join(placeholders, ","))
		rows, e = s.db.QueryContext(r.Context(), query, args...)
	} else {
		rows, e = s.db.QueryContext(r.Context(), "SELECT id,ordinal,title,url FROM chapters WHERE book_id=? AND ordinal >= ? ORDER BY ordinal LIMIT ?", b.ID, req.FromOrdinal, req.Count)
	}
	if e != nil {
		return e
	}
	defer rows.Close()

	type targetChap struct {
		ID, Title, URL string
		Ordinal        int
	}
	targets := []targetChap{}
	for rows.Next() {
		var tc targetChap
		if err := rows.Scan(&tc.ID, &tc.Ordinal, &tc.Title, &tc.URL); err == nil {
			targets = append(targets, tc)
		}
	}

	src, e := s.source(r.Context(), current(r).ID, b.SourceID, true)
	if e != nil {
		return e
	}

	cacheDir := filepath.Join(s.cfg.DataDir, "cache", "books", b.ID)
	_ = os.MkdirAll(cacheDir, 0700)

	cachedCount := 0
	skipCount := 0
	for i, ch := range targets {
		destFile := filepath.Join(cacheDir, ch.ID+".txt")
		if fi, err := os.Stat(destFile); err == nil && fi.Size() > 0 {
			skipCount++
			continue
		}
		var nextURL string
		if i+1 < len(targets) {
			nextURL = targets[i+1].URL
		} else {
			_ = s.db.QueryRowContext(r.Context(), "SELECT url FROM chapters WHERE book_id=? AND ordinal > ? ORDER BY ordinal ASC LIMIT 1", b.ID, ch.Ordinal).Scan(&nextURL)
		}
		content, err := s.engine.Content(r.Context(), engine.Source{ID: src.ID, Rule: src.Rule}, ch.URL, nextURL)
		if err == nil && len(content) > 0 {
			_ = os.WriteFile(destFile, []byte(content), 0600)
			cachedCount++
		}
		time.Sleep(30 * time.Millisecond)
	}

	respond(w, map[string]any{
		"ok":      true,
		"cached":  cachedCount,
		"skipped": skipCount,
		"total":   len(targets),
	})
	return nil
}

func (s *Server) bookCacheStatus(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	var total int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM chapters WHERE book_id=?", b.ID).Scan(&total)

	cachedCount := 0
	cacheDir := filepath.Join(s.cfg.DataDir, "cache", "books", b.ID)
	if b.Kind != "online" {
		cacheDir = filepath.Join(s.cfg.DataDir, "books", b.ID)
	}
	if entries, err := os.ReadDir(cacheDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
				cachedCount++
			}
		}
	}

	respond(w, map[string]any{
		"bookId":         b.ID,
		"totalChapters":  total,
		"cachedChapters": cachedCount,
	})
	return nil
}

func (s *Server) preloadUpcomingChapters(bookID, sourceID string, currentOrdinal, count int) {
	if count <= 0 || sourceID == "" || bookID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, "SELECT id,url FROM chapters WHERE book_id=? AND ordinal>? ORDER BY ordinal ASC LIMIT ?", bookID, currentOrdinal, count)
	if err != nil {
		return
	}
	defer rows.Close()

	type chapItem struct {
		id, url string
	}
	var queue []chapItem
	for rows.Next() {
		var ci chapItem
		if err := rows.Scan(&ci.id, &ci.url); err == nil {
			queue = append(queue, ci)
		}
	}
	if len(queue) == 0 {
		return
	}

	var rule string
	if err := s.db.QueryRowContext(ctx, "SELECT rule FROM sources WHERE id=?", sourceID).Scan(&rule); err != nil {
		return
	}

	cacheDir := filepath.Join(s.cfg.DataDir, "cache", "books", bookID)
	_ = os.MkdirAll(cacheDir, 0700)

	for i, ci := range queue {
		select {
		case <-ctx.Done():
			return
		default:
		}
		destFile := filepath.Join(cacheDir, ci.id+".txt")
		if fi, err := os.Stat(destFile); err == nil && fi.Size() > 0 {
			continue
		}
		var nextURL string
		if i+1 < len(queue) {
			nextURL = queue[i+1].url
		}
		text, err := s.engine.Content(ctx, engine.Source{ID: sourceID, Rule: json.RawMessage(rule)}, ci.url, nextURL)
		if err == nil && len(text) > 0 {
			_ = os.WriteFile(destFile, []byte(text), 0600)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Server) exportBook(w http.ResponseWriter, r *http.Request) error {
	b, e := s.book(r.Context(), current(r).ID, r.PathValue("id"))
	if e != nil {
		return e
	}
	rows, e := s.db.QueryContext(r.Context(), "SELECT id, ordinal, title FROM chapters WHERE book_id=? ORDER BY ordinal ASC", b.ID)
	if e != nil {
		return e
	}
	defer rows.Close()

	type chInfo struct {
		id      string
		ordinal int
		title   string
	}
	var chs []chInfo
	for rows.Next() {
		var ci chInfo
		if err := rows.Scan(&ci.id, &ci.ordinal, &ci.title); err == nil {
			chs = append(chs, ci)
		}
	}
	if len(chs) == 0 {
		return fail(400, "NO_CHAPTERS", "该书籍无章节可供导出")
	}

	filename := b.Title
	if filename == "" {
		filename = "novel"
	}
	encodedFilename := url.PathEscape(filename + ".txt")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.txt\"; filename*=UTF-8''%s", filename, encodedFilename))

	var buf bytes.Buffer
	buf.WriteString("《" + b.Title + "》\r\n")
	if b.Author != "" {
		buf.WriteString("作者：" + b.Author + "\r\n")
	}
	if b.Intro != "" {
		buf.WriteString("简介：" + strings.ReplaceAll(b.Intro, "\n", "\r\n") + "\r\n")
	}
	buf.WriteString("\r\n" + strings.Repeat("=", 40) + "\r\n\r\n")
	w.Write(buf.Bytes())

	cacheDir := filepath.Join(s.cfg.DataDir, "cache", "books", b.ID)
	for _, ci := range chs {
		var chBuf bytes.Buffer
		chBuf.WriteString(ci.title + "\r\n\r\n")

		cacheFile := filepath.Join(cacheDir, ci.id+".txt")
		if contentBytes, err := os.ReadFile(cacheFile); err == nil && len(contentBytes) > 0 {
			chBuf.Write(contentBytes)
		} else {
			chBuf.WriteString("（本章暂未缓存至本地）")
		}
		chBuf.WriteString("\r\n\r\n" + strings.Repeat("-", 30) + "\r\n\r\n")
		w.Write(chBuf.Bytes())
	}

	return nil
}

var incompletePageRegex = regexp.MustCompile(`(?:\([第\s]*1\s*[/／]\s*[2-9]\d*页?\)|（[第\s]*1\s*[/／]\s*[2-9]\d*页?）|本章未完|点击下一页)`)

func isIncompleteChapterText(s string) bool {
	checkHead := s
	if len(checkHead) > 500 {
		checkHead = checkHead[:500]
	}
	checkTail := s
	if len(checkTail) > 500 {
		checkTail = checkTail[len(checkTail)-500:]
	}
	return incompletePageRegex.MatchString(checkHead) || incompletePageRegex.MatchString(checkTail)
}
