package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"readergo/internal/engine"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Source struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	URL       string          `json:"url"`
	Group     string          `json:"group"`
	Enabled   bool            `json:"enabled"`
	Rule      json.RawMessage `json:"rule,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

func (s *Server) source(ctx context.Context, uid, id string, enabled bool) (Source, error) {
	var v Source
	q := "SELECT id,name,url,group_name,enabled,rule,updated_at FROM sources WHERE id=? AND user_id=?"
	if enabled {
		q += " AND enabled=TRUE"
	}
	e := s.db.QueryRowContext(ctx, q, id, uid).Scan(&v.ID, &v.Name, &v.URL, &v.Group, &v.Enabled, &v.Rule, &v.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return v, fail(404, "SOURCE_NOT_FOUND", "书源不存在或已禁用")
	}
	return v, e
}
func (s *Server) sources(w http.ResponseWriter, r *http.Request) error {
	uid := current(r).ID
	conds := []string{"user_id=?"}
	args := []any{uid}
	if enabledStr := r.URL.Query().Get("enabled"); enabledStr != "" && enabledStr != "all" {
		if enabledStr == "true" {
			conds = append(conds, "enabled=TRUE")
		} else if enabledStr == "false" {
			conds = append(conds, "enabled=FALSE")
		}
	}
	if group := strings.TrimSpace(r.URL.Query().Get("group")); group != "" {
		if group == "__none__" || group == "未分组" {
			conds = append(conds, "(group_name IS NULL OR group_name='')")
		} else {
			conds = append(conds, "group_name=?")
			args = append(args, group)
		}
	}
	if kw := strings.TrimSpace(r.URL.Query().Get("query")); kw != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(kw)
		conds = append(conds, `(name LIKE ? ESCAPE '\\' OR url LIKE ? ESCAPE '\\')`)
		args = append(args, "%"+escaped+"%", "%"+escaped+"%")
	}

	whereClause := " WHERE " + strings.Join(conds, " AND ")

	pageStr := r.URL.Query().Get("page")
	if pageStr != "" {
		page, _ := strconv.Atoi(pageStr)
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		if pageSize <= 0 {
			pageSize = 5
		}
		if pageSize > 100 {
			pageSize = 100
		}

		var total int
		countQ := "SELECT COUNT(*) FROM sources" + whereClause
		if err := s.db.QueryRowContext(r.Context(), countQ, args...).Scan(&total); err != nil {
			return err
		}

		totalPages := (total + pageSize - 1) / pageSize
		offset := (page - 1) * pageSize

		q := "SELECT id,name,url,group_name,enabled,rule,updated_at FROM sources" + whereClause + " ORDER BY name LIMIT ? OFFSET ?"
		qArgs := append(append([]any{}, args...), pageSize, offset)
		rows, err := s.db.QueryContext(r.Context(), q, qArgs...)
		if err != nil {
			return err
		}
		defer rows.Close()

		list := []Source{}
		for rows.Next() {
			var v Source
			if err = rows.Scan(&v.ID, &v.Name, &v.URL, &v.Group, &v.Enabled, &v.Rule, &v.UpdatedAt); err != nil {
				return err
			}
			list = append(list, v)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		meta := map[string]any{
			"total":      total,
			"page":       page,
			"pageSize":   pageSize,
			"totalPages": totalPages,
		}
		respondWithMeta(w, list, meta)
		return nil
	}

	q := "SELECT id,name,url,group_name,enabled,rule,updated_at FROM sources" + whereClause + " ORDER BY name LIMIT 2000"
	rows, e := s.db.QueryContext(r.Context(), q, args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	list := []Source{}
	for rows.Next() {
		var v Source
		if e = rows.Scan(&v.ID, &v.Name, &v.URL, &v.Group, &v.Enabled, &v.Rule, &v.UpdatedAt); e != nil {
			return e
		}
		list = append(list, v)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	respond(w, list)
	return nil
}
func (s *Server) sourceGroups(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.db.QueryContext(r.Context(), "SELECT DISTINCT group_name FROM sources WHERE user_id=? AND group_name IS NOT NULL AND group_name!='' ORDER BY group_name", current(r).ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	groups := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err == nil && g != "" {
			groups = append(groups, g)
		}
	}
	respond(w, groups)
	return nil
}
func (s *Server) batchSources(w http.ResponseWriter, r *http.Request) error {
	uid := current(r).ID
	var body struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if len(body.IDs) == 0 {
		return fail(400, "BAD_REQUEST", "未选择任何书源")
	}
	if len(body.IDs) > 500 {
		return fail(400, "LIMIT_EXCEEDED", "单次最多操作 500 个书源")
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	affected := 0
	switch body.Action {
	case "enable":
		stmt, err := tx.PrepareContext(r.Context(), "UPDATE sources SET enabled=TRUE WHERE id=? AND user_id=?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, sid := range body.IDs {
			if res, err := stmt.ExecContext(r.Context(), sid, uid); err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					affected++
				}
			}
		}
	case "disable":
		stmt, err := tx.PrepareContext(r.Context(), "UPDATE sources SET enabled=FALSE WHERE id=? AND user_id=?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, sid := range body.IDs {
			if res, err := stmt.ExecContext(r.Context(), sid, uid); err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					affected++
				}
			}
		}
	case "delete":
		stmt, err := tx.PrepareContext(r.Context(), "DELETE FROM sources WHERE id=? AND user_id=?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, sid := range body.IDs {
			if res, err := stmt.ExecContext(r.Context(), sid, uid); err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					affected++
				}
			}
		}
	default:
		return fail(400, "INVALID_ACTION", "不支持的操作类型")
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.audit(r, "sources.batch."+body.Action, fmt.Sprintf("%d sources", affected))
	respond(w, map[string]any{"affected": affected})
	return nil
}
func canonicalSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "http" && strings.HasSuffix(u.Host, ":80") {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	} else if u.Scheme == "https" && strings.HasSuffix(u.Host, ":443") {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
func parseSource(raw json.RawMessage) (Source, error) {
	var v struct {
		Name    string `json:"bookSourceName"`
		URL     string `json:"bookSourceUrl"`
		Group   string `json:"bookSourceGroup"`
		Enabled *bool  `json:"enabled"`
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		return Source{}, fail(400, "SOURCE_JSON", "书源必须是 JSON 对象")
	}
	v.Name = strings.TrimSpace(v.Name)
	v.URL = canonicalSourceURL(v.URL)
	v.Group = strings.TrimSpace(v.Group)
	u, e := url.Parse(v.URL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || v.Name == "" || len(v.Name) > 255 || len(v.Group) > 255 || len(v.URL) > 4096 {
		return Source{}, fail(400, "SOURCE_FIELDS", "书源名称或 HTTP(S) 地址无效")
	}
	enabled := true
	if v.Enabled != nil {
		enabled = *v.Enabled
	}
	return Source{ID: id(), Name: v.Name, URL: v.URL, Group: v.Group, Enabled: enabled, Rule: raw}, nil
}
func (s *Server) importSources(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Sources   json.RawMessage
		Overwrite bool
	}
	if e := decode(w, r, &body); e != nil {
		return e
	}
	raw := strings.TrimSpace(string(body.Sources))
	var entries []json.RawMessage
	if strings.HasPrefix(raw, "[") {
		if e := json.Unmarshal(body.Sources, &entries); e != nil {
			return fail(400, "SOURCE_JSON", "无效书源数组")
		}
	} else {
		entries = []json.RawMessage{body.Sources}
	}
	if len(entries) == 0 || len(entries) > 2000 {
		return fail(400, "SOURCE_LIMIT", "单次导入 1–2000 个书源")
	}

	type dedupItem struct {
		source Source
		order  int
	}
	dedupMap := make(map[string]dedupItem)
	skippedInBatch := 0

	for i, entry := range entries {
		v, e := parseSource(entry)
		if e != nil {
			return e
		}
		key := digest(v.URL)
		if _, exists := dedupMap[key]; exists {
			if !body.Overwrite {
				skippedInBatch++
				continue
			}
		}
		dedupMap[key] = dedupItem{source: v, order: i}
	}

	items := make([]dedupItem, 0, len(dedupMap))
	for _, item := range dedupMap {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].order < items[j].order })

	parsed := make([]Source, 0, len(items))
	for _, it := range items {
		parsed = append(parsed, it.source)
	}

	uid := current(r).ID
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	imported, updated, skipped := 0, 0, skippedInBatch
	for _, v := range parsed {
		var existing string
		sourceKey := digest(v.URL)
		e = tx.QueryRowContext(r.Context(), "SELECT id FROM sources WHERE user_id=? AND source_key=?", uid, sourceKey).Scan(&existing)
		if e == nil {
			if !body.Overwrite {
				skipped++
				continue
			}
			_, e = tx.ExecContext(r.Context(), "UPDATE sources SET name=?,url=?,group_name=?,enabled=?,rule=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND user_id=?", v.Name, v.URL, v.Group, v.Enabled, string(v.Rule), existing, uid)
			if e != nil {
				return e
			}
			updated++
		} else if errors.Is(e, sql.ErrNoRows) {
			_, e = tx.ExecContext(r.Context(), "INSERT INTO sources(id,user_id,source_key,name,url,group_name,enabled,rule) VALUES(?,?,?,?,?,?,?,?)", v.ID, uid, sourceKey, v.Name, v.URL, v.Group, v.Enabled, string(v.Rule))
			if e != nil {
				return e
			}
			imported++
		} else {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit(r, "sources.import", fmt.Sprintf("imported=%d, updated=%d, skipped=%d", imported, updated, skipped))
	respond(w, map[string]int{
		"imported": imported,
		"updated":  updated,
		"skipped":  skipped,
		"total":    len(entries),
	})
	return nil
}
func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) error {
	uid := current(r).ID
	old, e := s.source(r.Context(), uid, r.PathValue("id"), false)
	if e != nil {
		return e
	}
	var v struct {
		Name, Group *string
		Enabled     *bool
		Rule        json.RawMessage
	}
	if e = decode(w, r, &v); e != nil {
		return e
	}
	if len(v.Rule) > 0 {
		p, e := parseSource(v.Rule)
		if e != nil {
			return e
		}
		old.Name = p.Name
		old.URL = p.URL
		old.Group = p.Group
		old.Enabled = p.Enabled
		old.Rule = v.Rule
	}
	if v.Name != nil {
		old.Name = *v.Name
	}
	if v.Group != nil {
		old.Group = *v.Group
	}
	if v.Enabled != nil {
		old.Enabled = *v.Enabled
	}
	if len(old.Name) == 0 || len(old.Name) > 255 || len(old.Group) > 255 {
		return fail(400, "SOURCE_FIELDS", "名称或分组长度无效")
	}
	var obj map[string]any
	if e = json.Unmarshal(old.Rule, &obj); e != nil {
		return e
	}
	obj["bookSourceName"] = old.Name
	obj["bookSourceGroup"] = old.Group
	obj["enabled"] = old.Enabled
	old.Rule, _ = json.Marshal(obj)
	_, e = s.db.ExecContext(r.Context(), "UPDATE sources SET source_key=?,name=?,url=?,group_name=?,enabled=?,rule=? WHERE id=? AND user_id=?", digest(old.URL), old.Name, old.URL, old.Group, old.Enabled, string(old.Rule), old.ID, uid)
	if e != nil {
		return e
	}
	s.audit(r, "source.update", old.ID)
	respond(w, old)
	return nil
}
func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) error {
	res, e := s.db.ExecContext(r.Context(), "DELETE FROM sources WHERE id=? AND user_id=?", r.PathValue("id"), current(r).ID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "NOT_FOUND", "书源不存在")
	}
	s.audit(r, "source.delete", r.PathValue("id"))
	respond(w, map[string]bool{"ok": true})
	return nil
}
func (s *Server) remoteSource(w http.ResponseWriter, r *http.Request) error {
	var v struct{ URL string }
	if e := decode(w, r, &v); e != nil {
		return e
	}
	raw, e := s.engine.FetchSource(r.Context(), v.URL)
	if e != nil {
		return fail(422, "SOURCE_FETCH", "远程书源获取失败，请检查地址、网络或访问限制")
	}
	respond(w, raw)
	return nil
}
func (s *Server) checkSource(w http.ResponseWriter, r *http.Request) error {
	v, e := s.source(r.Context(), current(r).ID, r.PathValue("id"), false)
	if e != nil {
		return e
	}
	var body struct{ Keyword string }
	if e = decode(w, r, &body); e != nil {
		return e
	}
	if len(body.Keyword) == 0 || len(body.Keyword) > 200 {
		return fail(400, "KEYWORD", "请输入有效关键词")
	}
	results, e := s.engine.Search(r.Context(), engine.Source{ID: v.ID, Rule: v.Rule}, body.Keyword)
	if e != nil {
		return fail(422, "SOURCE_CHECK", "书源搜索失败或使用暂不支持的规则；请查看兼容说明")
	}
	respond(w, results)
	return nil
}
func normalizeMatchText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		"《", "", "》", "", "「", "", "」", "", "『", "", "』", "",
		"（", "", "）", "", "(", "", ")", "", "【", "", "】", "",
		"[", "", "]", "", "：", "", ":", "", "·", "", " ", "", "\t", "",
	)
	return replacer.Replace(s)
}

func scoreSearchResult(res engine.Result, keyword string) int {
	kw := normalizeMatchText(keyword)
	if kw == "" {
		return 0
	}
	title := normalizeMatchText(res.Title)
	author := normalizeMatchText(res.Author)
	if title == "" {
		return 0
	}
	if title == kw {
		return 100
	}
	if strings.HasPrefix(title, kw) {
		return 85
	}
	if strings.Contains(title, kw) {
		return 75
	}
	if author != "" && author == kw {
		return 65
	}
	if author != "" && strings.Contains(author, kw) {
		return 55
	}
	if len([]rune(title)) >= 2 && strings.Contains(kw, title) {
		return 40
	}
	return 0
}

func filterAndRankResults(items []engine.Result, keyword string) []engine.Result {
	type scored struct {
		item  engine.Result
		score int
		tLen  int
	}
	matched := make([]scored, 0, len(items))
	for _, it := range items {
		sc := scoreSearchResult(it, keyword)
		if sc > 0 {
			matched = append(matched, scored{item: it, score: sc, tLen: len([]rune(strings.TrimSpace(it.Title)))})
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		return matched[i].tLen < matched[j].tLen
	})
	out := make([]engine.Result, 0, len(matched))
	for _, m := range matched {
		out = append(out, m.item)
	}
	return out
}

func (s *Server) resolveSearchSources(ctx context.Context, uid string, sourceIDs []string, maxLimit int) ([]Source, error) {
	if maxLimit <= 0 || maxLimit > 1000 {
		maxLimit = 1000
	}
	if len(sourceIDs) == 0 {
		rows, err := s.db.QueryContext(ctx, "SELECT id,name,url,group_name,enabled,rule,updated_at FROM sources WHERE user_id=? AND enabled=TRUE ORDER BY name LIMIT ?", uid, maxLimit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		list := []Source{}
		for rows.Next() {
			var v Source
			if err := rows.Scan(&v.ID, &v.Name, &v.URL, &v.Group, &v.Enabled, &v.Rule, &v.UpdatedAt); err == nil {
				list = append(list, v)
			}
		}
		return list, nil
	}
	sources := []Source{}
	seen := map[string]bool{}
	for _, sid := range sourceIDs {
		if seen[sid] {
			continue
		}
		seen[sid] = true
		v, e := s.source(ctx, uid, sid, true)
		if e == nil {
			sources = append(sources, v)
			if len(sources) >= maxLimit {
				break
			}
		}
	}
	return sources, nil
}

func (s *Server) searchBooks(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Keyword        string
		SourceIDs      []string `json:"sourceIds"`
		TimeoutSeconds int      `json:"timeoutSeconds"`
	}
	if e := decode(w, r, &body); e != nil {
		return e
	}
	body.Keyword = strings.TrimSpace(body.Keyword)
	if len(body.Keyword) == 0 || len(body.Keyword) > 200 || len(body.SourceIDs) > 1000 {
		return fail(400, "SEARCH", "请输入有效搜索关键词")
	}
	select {
	case s.search <- struct{}{}:
		defer func() { <-s.search }()
	default:
		return fail(429, "BUSY", "搜索队列繁忙，请稍后再试")
	}
	perSourceTimeout := 120 * time.Second
	if body.TimeoutSeconds > 0 && body.TimeoutSeconds <= 120 {
		perSourceTimeout = time.Duration(body.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	uid := current(r).ID
	sources, err := s.resolveSearchSources(ctx, uid, body.SourceIDs, 1000)
	if err != nil {
		return err
	}
	results := []engine.Result{}
	warnings := []map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, src := range sources {
		wg.Add(1)
		go func(v Source) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				warnings = append(warnings, map[string]string{"sourceId": v.ID, "message": "搜索已超时"})
				mu.Unlock()
				return
			}
			srcCtx, srcCancel := context.WithTimeout(ctx, perSourceTimeout)
			items, e := s.engine.Search(srcCtx, engine.Source{ID: v.ID, Rule: v.Rule}, body.Keyword)
			srcCancel()
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				warnings = append(warnings, map[string]string{"sourceId": v.ID, "message": "书源请求或解析失败"})
				return
			}
			items = filterAndRankResults(items, body.Keyword)
			if len(items) > 100 {
				items = items[:100]
			}
			results = append(results, items...)
		}(src)
	}
	wg.Wait()
	results = filterAndRankResults(results, body.Keyword)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	return json.NewEncoder(w).Encode(map[string]any{"data": results, "meta": map[string]any{"warnings": warnings}})
}
func (s *Server) searchBooksStream(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return s.searchBooks(w, r)
	}
	var body struct {
		Keyword        string
		SourceIDs      []string `json:"sourceIds"`
		TimeoutSeconds int      `json:"timeoutSeconds"`
	}
	if e := decode(w, r, &body); e != nil {
		return e
	}
	body.Keyword = strings.TrimSpace(body.Keyword)
	if len(body.Keyword) == 0 || len(body.Keyword) > 200 || len(body.SourceIDs) > 1000 {
		return fail(400, "SEARCH", "请输入有效搜索关键词")
	}
	select {
	case s.search <- struct{}{}:
		defer func() { <-s.search }()
	default:
		return fail(429, "BUSY", "搜索队列繁忙，请稍后再试")
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	perSourceTimeout := 120 * time.Second
	if body.TimeoutSeconds > 0 && body.TimeoutSeconds <= 120 {
		perSourceTimeout = time.Duration(body.TimeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	uid := current(r).ID
	sources, err := s.resolveSearchSources(ctx, uid, body.SourceIDs, 1000)
	if err != nil {
		return err
	}
	total := len(sources)
	sourceNames := make([]string, 0, total)
	for _, src := range sources {
		sourceNames = append(sourceNames, src.Name)
	}
	sendSSE(w, flusher, "start", map[string]any{"total": total, "sources": sourceNames})

	var mu sync.Mutex
	var completedCount int
	var totalFound int
	var wg sync.WaitGroup
	sem := make(chan struct{}, 24)

	for _, src := range sources {
		wg.Add(1)
		go func(v Source) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				completedCount++
				sendSSE(w, flusher, "progress", map[string]any{
					"sourceId":   v.ID,
					"sourceName": v.Name,
					"status":     "timeout",
					"error":      "搜索已超时",
					"results":    []engine.Result{},
					"completed":  completedCount,
					"total":      total,
				})
				mu.Unlock()
				return
			}

			mu.Lock()
			sendSSE(w, flusher, "progress", map[string]any{
				"sourceId":   v.ID,
				"sourceName": v.Name,
				"status":     "searching",
				"completed":  completedCount,
				"total":      total,
				"timestamp":  time.Now().UnixMilli(),
			})
			mu.Unlock()

			srcCtx, srcCancel := context.WithTimeout(ctx, perSourceTimeout)
			items, err := s.engine.Search(srcCtx, engine.Source{ID: v.ID, Rule: v.Rule}, body.Keyword)
			srcCancel()

			mu.Lock()
			defer mu.Unlock()
			completedCount++

			if err != nil {
				errMsg := "书源未响应或规则不匹配"
				status := "error"
				if errors.Is(err, context.DeadlineExceeded) || (srcCtx.Err() == context.DeadlineExceeded) {
					errMsg = fmt.Sprintf("响应超时(>%ds)", int(perSourceTimeout.Seconds()))
					status = "timeout"
				}
				sendSSE(w, flusher, "progress", map[string]any{
					"sourceId":   v.ID,
					"sourceName": v.Name,
					"status":     status,
					"error":      errMsg,
					"results":    []engine.Result{},
					"completed":  completedCount,
					"total":      total,
					"timestamp":  time.Now().UnixMilli(),
				})
				return
			}
			items = filterAndRankResults(items, body.Keyword)
			if len(items) > 100 {
				items = items[:100]
			}
			totalFound += len(items)
			sendSSE(w, flusher, "progress", map[string]any{
				"sourceId":   v.ID,
				"sourceName": v.Name,
				"status":     "success",
				"count":      len(items),
				"results":    items,
				"completed":  completedCount,
				"total":      total,
				"timestamp":  time.Now().UnixMilli(),
			})
		}(src)
	}

	wg.Wait()
	sendSSE(w, flusher, "done", map[string]any{
		"total":      total,
		"completed":  completedCount,
		"totalFound": totalFound,
	})
	return nil
}
func sendSSE(w io.Writer, flusher http.Flusher, event string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
	flusher.Flush()
}
func (s *Server) overview(w http.ResponseWriter, r *http.Request) error {
	v := map[string]any{"version": Version}
	for _, table := range []string{"users", "books", "sources"} {
		var n int
		if e := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); e != nil {
			return e
		}
		v[table] = n
	}
	respond(w, v)
	return nil
}
func (s *Server) deduplicateSources(w http.ResponseWriter, r *http.Request) error {
	uid := current(r).ID
	var body struct {
		Mode string `json:"mode"`
	}
	_ = decode(w, r, &body)
	if body.Mode == "" {
		body.Mode = "all"
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	deletedIDs := make(map[string]bool)

	// 1. 同用户下同 source_key / URL 规范化重复清理：保留最新 updated_at
	rows, err := tx.QueryContext(r.Context(), `
		SELECT s1.id 
		FROM sources s1
		JOIN sources s2 ON s1.user_id = s2.user_id AND s1.source_key = s2.source_key AND s1.id != s2.id
		WHERE s1.user_id = ? AND (s1.updated_at < s2.updated_at OR (s1.updated_at = s2.updated_at AND s1.id < s2.id))
	`, uid)
	if err == nil {
		for rows.Next() {
			var dupID string
			if err := rows.Scan(&dupID); err == nil {
				deletedIDs[dupID] = true
			}
		}
		rows.Close()
	}

	// 2. 如果包含 name 模式，当前用户同名源保留最新更新记录
	if body.Mode == "name" || body.Mode == "all" {
		nameRows, err := tx.QueryContext(r.Context(), `
			SELECT s1.id
			FROM sources s1
			JOIN sources s2 ON s1.user_id = s2.user_id AND s1.name = s2.name AND s1.id != s2.id
			WHERE s1.user_id = ? AND (s1.updated_at < s2.updated_at OR (s1.updated_at = s2.updated_at AND s1.id < s2.id))
		`, uid)
		if err == nil {
			for nameRows.Next() {
				var dupID string
				if err := nameRows.Scan(&dupID); err == nil {
					deletedIDs[dupID] = true
				}
			}
			nameRows.Close()
		}
	}

	cleanedCount := 0
	if len(deletedIDs) > 0 {
		ids := make([]any, 0, len(deletedIDs)+1)
		ids = append(ids, uid)
		placeholders := make([]string, 0, len(deletedIDs))
		for id := range deletedIDs {
			ids = append(ids, id)
			placeholders = append(placeholders, "?")
		}
		delQ := "DELETE FROM sources WHERE user_id=? AND id IN (" + strings.Join(placeholders, ",") + ")"
		res, err := tx.ExecContext(r.Context(), delQ, ids...)
		if err != nil {
			return err
		}
		aff, _ := res.RowsAffected()
		cleanedCount = int(aff)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	var totalRemaining int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM sources WHERE user_id=?", uid).Scan(&totalRemaining)

	s.audit(r, "sources.deduplicate", fmt.Sprintf("mode=%s, cleaned=%d, remaining=%d", body.Mode, cleanedCount, totalRemaining))
	respond(w, map[string]any{
		"cleaned":   cleanedCount,
		"remaining": totalRemaining,
		"removed":   cleanedCount,
		"retained":  totalRemaining,
		"total":     cleanedCount + totalRemaining,
		"mode":      body.Mode,
	})
	return nil
}
func (s *Server) probeSources(w http.ResponseWriter, r *http.Request) error {
	uid := current(r).ID
	var body struct {
		AutoDelete *bool    `json:"autoDelete"`
		IDs        []string `json:"ids"`
	}
	_ = decode(w, r, &body)
	autoDelete := true
	if body.AutoDelete != nil {
		autoDelete = *body.AutoDelete
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	var rows *sql.Rows
	var err error
	if len(body.IDs) > 0 && len(body.IDs) <= 500 {
		args := make([]any, 0, len(body.IDs)+1)
		args = append(args, uid)
		placeholders := make([]string, 0, len(body.IDs))
		for _, sid := range body.IDs {
			args = append(args, sid)
			placeholders = append(placeholders, "?")
		}
		rows, err = s.db.QueryContext(ctx, "SELECT id,name,url,group_name,enabled,rule,updated_at FROM sources WHERE user_id=? AND id IN ("+strings.Join(placeholders, ",")+") ORDER BY name", args...)
	} else {
		rows, err = s.db.QueryContext(ctx, "SELECT id,name,url,group_name,enabled,rule,updated_at FROM sources WHERE user_id=? ORDER BY name LIMIT 1000", uid)
	}
	if err != nil {
		return err
	}
	defer rows.Close()

	list := []Source{}
	for rows.Next() {
		var v Source
		if err := rows.Scan(&v.ID, &v.Name, &v.URL, &v.Group, &v.Enabled, &v.Rule, &v.UpdatedAt); err == nil {
			list = append(list, v)
		}
	}

	type deadItem struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		URL    string `json:"url"`
		Reason string `json:"reason"`
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	deadList := []deadItem{}
	aliveIDs := []string{}

	for _, src := range list {
		wg.Add(1)
		go func(v Source) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			ok, reason := s.engine.Probe(ctx, engine.Source{ID: v.ID, Rule: v.Rule})
			mu.Lock()
			defer mu.Unlock()
			if ok {
				aliveIDs = append(aliveIDs, v.ID)
			} else {
				deadList = append(deadList, deadItem{
					ID:     v.ID,
					Name:   v.Name,
					URL:    v.URL,
					Reason: reason,
				})
			}
		}(src)
	}
	wg.Wait()

	removedCount := 0
	if autoDelete && len(deadList) > 0 {
		args := make([]any, 0, len(deadList)+1)
		args = append(args, uid)
		placeholders := make([]string, 0, len(deadList))
		for _, d := range deadList {
			args = append(args, d.ID)
			placeholders = append(placeholders, "?")
		}
		res, err := s.db.ExecContext(r.Context(), "DELETE FROM sources WHERE user_id=? AND id IN ("+strings.Join(placeholders, ",")+")", args...)
		if err == nil {
			aff, _ := res.RowsAffected()
			removedCount = int(aff)
		}
	}

	var totalRemaining int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM sources WHERE user_id=?", uid).Scan(&totalRemaining)

	s.audit(r, "sources.probe", fmt.Sprintf("probed=%d, alive=%d, dead=%d, removed=%d", len(list), len(aliveIDs), len(deadList), removedCount))
	respond(w, map[string]any{
		"probed":      len(list),
		"alive":       len(aliveIDs),
		"aliveIds":    aliveIDs,
		"dead":        len(deadList),
		"removed":     removedCount,
		"retained":    totalRemaining,
		"deadSources": deadList,
	})
	return nil
}
func (s *Server) auditList(w http.ResponseWriter, r *http.Request) error {
	pageStr := r.URL.Query().Get("page")
	if pageStr != "" {
		page, _ := strconv.Atoi(pageStr)
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		if pageSize <= 0 {
			pageSize = 5
		}
		if pageSize > 100 {
			pageSize = 100
		}

		var total int
		if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM audit_logs").Scan(&total); err != nil {
			return err
		}

		offset := (page - 1) * pageSize
		rows, err := s.db.QueryContext(r.Context(), "SELECT id,actor,action,target,created_at FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?", pageSize, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		list := []map[string]any{}
		for rows.Next() {
			var id int64
			var actor, action, target string
			var at time.Time
			if err = rows.Scan(&id, &actor, &action, &target, &at); err != nil {
				return err
			}
			list = append(list, map[string]any{"id": id, "actor": actor, "action": action, "target": target, "createdAt": at})
		}
		if err = rows.Err(); err != nil {
			return err
		}
		totalPages := (total + pageSize - 1) / pageSize
		respondWithMeta(w, list, map[string]any{
			"total":      total,
			"page":       page,
			"pageSize":   pageSize,
			"totalPages": totalPages,
		})
		return nil
	}

	rows, e := s.db.QueryContext(r.Context(), "SELECT id,actor,action,target,created_at FROM audit_logs ORDER BY id DESC LIMIT 200")
	if e != nil {
		return e
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var actor, action, target string
		var at time.Time
		if e = rows.Scan(&id, &actor, &action, &target, &at); e != nil {
			return e
		}
		list = append(list, map[string]any{"id": id, "actor": actor, "action": action, "target": target, "createdAt": at})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	respond(w, list)
	return nil
}
