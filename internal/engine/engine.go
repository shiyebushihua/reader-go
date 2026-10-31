package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Source struct {
	ID   string
	Rule json.RawMessage
}
type Result struct {
	SourceID string `json:"sourceId"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Cover    string `json:"cover"`
	Intro         string `json:"intro"`
	LatestChapter string `json:"latestChapter,omitempty"`
}
type Chapter struct {
	Title string
	URL   string
}
type Detail struct {
	Title, Author, Cover, Intro, URL string
	Chapters                         []Chapter
}
type sourceRules struct {
	BookSourceURL string            `json:"bookSourceUrl"`
	SearchURL     string            `json:"searchUrl"`
	Header        json.RawMessage   `json:"header"`
	Search        map[string]string `json:"ruleSearch"`
	Info          map[string]string `json:"ruleBookInfo"`
	Toc           map[string]string `json:"ruleToc"`
	Content       map[string]string `json:"ruleContent"`
}

func parseSource(s Source) (sourceRules, error) {
	var r sourceRules
	if len(s.Rule) > 1<<20 {
		return r, fmt.Errorf("source rules exceed 1 MiB")
	}
	if err := json.Unmarshal(s.Rule, &r); err != nil {
		return r, fmt.Errorf("invalid source rules (rule values must be strings): %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(s.Rule, &raw); err != nil {
		return r, err
	}
	for _, key := range []string{"jsLib", "loginUrl", "loginUi", "loginCheckJs", "coverDecodeJs", "bookUrlPattern"} {
		if v := raw[key]; len(v) > 0 && string(v) != "null" && string(v) != `""` {
			return r, fmt.Errorf("unsupported source option %q", key)
		}
	}
	for _, group := range []struct {
		name    string
		rules   map[string]string
		allowed string
	}{
		{"ruleSearch", r.Search, " bookList name author bookUrl coverUrl intro "},
		{"ruleBookInfo", r.Info, " init name author coverUrl intro tocUrl "},
		{"ruleToc", r.Toc, " chapterList chapterName chapterUrl nextTocUrl "},
		{"ruleContent", r.Content, " content nextContentUrl replaceRegex "},
	} {
		for key, rule := range group.rules {
			// Display-only metadata is not evaluated in this version. Ignore it rather
			// than blocking otherwise usable search/reading rules.
			if (group.name == "ruleSearch" || group.name == "ruleBookInfo") && strings.Contains(" kind lastChapter wordCount checkKeyWord ", " "+key+" ") {
				continue
			}
			if group.name == "ruleContent" && key == "imageStyle" {
				continue
			}
			if strings.TrimSpace(rule) == "" {
				continue
			}
			if !strings.Contains(group.allowed, " "+key+" ") {
				return r, fmt.Errorf("unsupported %s field %q", group.name, key)
			}
			if err := supported(rule); err != nil {
				return r, fmt.Errorf("%s.%s: %w", group.name, key, err)
			}
		}
	}
	if r.BookSourceURL == "" {
		return r, fmt.Errorf("missing bookSourceUrl")
	}
	u, err := url.Parse(r.BookSourceURL)
	if err != nil {
		return r, err
	}
	if err = validURL(u); err != nil {
		return r, err
	}
	return r, nil
}
func headers(raw json.RawMessage) (map[string]string, error) {
	out := map[string]string{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return out, nil
		}
		if err := supported(s); err != nil {
			return nil, err
		}
		raw = json.RawMessage(s)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("headers must be a JSON string map: %w", err)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}
func resolve(base, ref string) (string, error) {
	if strings.ContainsAny(ref, "\r\n") {
		return "", fmt.Errorf("URL rule returned multiple values")
	}
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u = b.ResolveReference(u)
	u.Fragment = ""
	if err = validURL(u); err != nil {
		return "", err
	}
	return u.String(), nil
}
var unquotedKeyRegex = regexp.MustCompile(`(?m)([{,]\s*)([a-zA-Z_][a-zA-Z0-9_-]*)\s*:`)
var trailingCommaRegex = regexp.MustCompile(`,\s*}`)

func normalizeJSONOptions(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return raw
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	// Legado allows unquoted keys like {method:"post",body:"..."}
	res := unquotedKeyRegex.ReplaceAllString(raw, `$1"$2":`)
	res = trailingCommaRegex.ReplaceAllString(res, "}")
	return res
}

func template(text, key string, page int, escaped bool) (string, error) {
	if escaped {
		key = url.QueryEscape(key)
	}
	prevPage := strconv.Itoa(page - 1)
	if page <= 1 {
		prevPage = "1"
	}
	nextPage := strconv.Itoa(page + 1)
	text = strings.NewReplacer(
		"{{key}}", key,
		"{{page}}", strconv.Itoa(page),
		"{key}", key,
		"{page}", strconv.Itoa(page),
		"{{page-1}}", prevPage,
		"{{page+1}}", nextPage,
	).Replace(text)
	if err := supported(text); err != nil {
		return "", err
	}
	return text, nil
}
func makeRequest(r sourceRules, base, raw, key string, page int) (requestSpec, error) {
	spec := requestSpec{Method: "GET"}
	var err error
	spec.Headers, err = headers(r.Header)
	if err != nil {
		return spec, err
	}
	raw = strings.TrimSpace(raw)
	// Legado URL,JSON-options syntax; the delimiter must precede a JSON object.
	options := ""
	if i := strings.Index(raw, ",{"); i >= 0 {
		options = raw[i+1:]
		raw = raw[:i]
	} else if i := strings.Index(raw, ", {"); i >= 0 {
		options = raw[i+2:]
		raw = raw[:i]
	}
	raw, err = template(raw, key, page, true)
	if err != nil {
		return spec, err
	}
	spec.URL, err = resolve(base, raw)
	if err != nil {
		return spec, err
	}
	if options != "" {
		normalized := normalizeJSONOptions(options)
		var opts map[string]json.RawMessage
		if err = json.Unmarshal([]byte(normalized), &opts); err != nil {
			// Fallback: extract method, charset, body via regex if relaxed unmarshal still fails
			if m := regexp.MustCompile(`(?i)["']?method["']?\s*:\s*["']?([a-zA-Z]+)`).FindStringSubmatch(options); len(m) > 1 {
				spec.Method = strings.ToUpper(m[1])
			}
			if m := regexp.MustCompile(`(?i)["']?charset["']?\s*:\s*["']?([a-zA-Z0-9_-]+)`).FindStringSubmatch(options); len(m) > 1 {
				spec.Charset = m[1]
			}
			if m := regexp.MustCompile(`(?i)["']?body["']?\s*:\s*("([^"\\]*(?:\\.[^"\\]*)*)"|'([^'\\]*(?:\\.[^'\\]*)*)')`).FindStringSubmatch(options); len(m) > 2 {
				if m[2] != "" {
					spec.Body = m[2]
				} else if len(m) > 4 && m[4] != "" {
					spec.Body = m[4]
				}
			}
		} else {
			for k, v := range opts {
				switch strings.ToLower(k) {
				case "method":
					_ = json.Unmarshal(v, &spec.Method)
				case "charset":
					_ = json.Unmarshal(v, &spec.Charset)
				case "headers", "header":
					var h map[string]string
					if h, err = headers(v); err == nil {
						for hk, val := range h {
							spec.Headers[hk] = val
						}
					}
				case "body":
					if len(v) > 0 && v[0] == '"' {
						_ = json.Unmarshal(v, &spec.Body)
					} else {
						spec.Body = string(v)
					}
				default:
					return spec, fmt.Errorf("unsupported HTTP option %q", k)
			}
		}
	}
		// Body strings use form escaping, matching conventional Legado search POST forms.
		spec.Body, err = template(spec.Body, key, page, true)
		if err != nil {
			return spec, err
		}
	}
	for k, v := range spec.Headers {
		v, err = template(v, key, page, false)
		if err != nil {
			return spec, err
		}
		spec.Headers[k] = v
	}
	if spec.Method == "" {
		spec.Method = "GET"
	} else {
		spec.Method = strings.ToUpper(spec.Method)
	}
	return spec, nil
}
func (e *Engine) load(ctx context.Context, r sourceRules, base, raw string) (value, string, error) {
	spec, err := makeRequest(r, base, raw, "", 1)
	if err != nil {
		return value{}, "", err
	}
	p, err := e.fetch(ctx, spec)
	if err != nil {
		return value{}, "", err
	}
	v, err := document(p.Body)
	return v, p.URL, err
}
func fields(v value, rules map[string]string, names ...string) ([]string, error) {
	out := make([]string, len(names))
	for i, k := range names {
		s, err := extract(v, rules[k])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[i] = s
	}
	return out, nil
}
func (e *Engine) Search(ctx context.Context, s Source, keyword string) ([]Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	r, err := parseSource(s)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(keyword) == "" || len(keyword) > 1024 {
		return nil, fmt.Errorf("search keyword must be 1–1024 bytes")
	}
	if r.SearchURL == "" {
		return nil, fmt.Errorf("source has no searchUrl")
	}
	spec, err := makeRequest(r, r.BookSourceURL, r.SearchURL, keyword, 1)
	if err != nil {
		return nil, err
	}
	p, err := e.fetch(ctx, spec)
	if err != nil {
		return nil, err
	}
	doc, err := document(p.Body)
	if err != nil {
		return nil, err
	}
	list, err := elements(doc, r.Search["bookList"])
	if err != nil {
		return nil, fmt.Errorf("bookList: %w", err)
	}
	if len(list) > 500 {
		return nil, fmt.Errorf("search exceeds 500 results")
	}
	out := make([]Result, 0, len(list))
	seen := map[string]bool{}
	for _, v := range list {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		f, er := fields(v, r.Search, "name", "author", "bookUrl", "coverUrl", "intro", "lastChapter")
		if er != nil {
			return nil, er
		}
		if f[0] == "" || f[2] == "" {
			continue
		}
		bookURL, er := resolve(p.URL, f[2])
		if er != nil {
			return nil, er
		}
		if seen[bookURL] {
			continue
		}
		seen[bookURL] = true
		cover := ""
		if f[3] != "" {
			cover, er = resolve(p.URL, f[3])
			if er != nil {
				return nil, er
			}
		}
		latest := ""
		if len(f) > 5 {
			latest = strings.TrimSpace(f[5])
		}
		out = append(out, Result{SourceID: s.ID, URL: bookURL, Title: f[0], Author: f[1], Cover: cover, Intro: f[4], LatestChapter: latest})
	}
	return out, nil
}
func (e *Engine) Details(ctx context.Context, s Source, rawURL string) (Detail, error) {
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	var d Detail
	r, err := parseSource(s)
	if err != nil {
		return d, err
	}
	doc, base, err := e.load(ctx, r, r.BookSourceURL, rawURL)
	if err != nil {
		return d, err
	}
	d.URL = base
	if init := r.Info["init"]; init != "" {
		vs, er := elements(doc, init)
		if er != nil {
			return d, er
		}
		if len(vs) == 0 {
			return d, fmt.Errorf("book info init matched nothing")
		}
		doc = vs[0]
	}
	f, err := fields(doc, r.Info, "name", "author", "coverUrl", "intro", "tocUrl")
	if err != nil {
		return d, err
	}
	d.Title = f[0]
	d.Author = f[1]
	d.Intro = f[3]
	if f[2] != "" {
		d.Cover, err = resolve(base, f[2])
		if err != nil {
			return d, err
		}
	}
	if f[4] != "" {
		doc, base, err = e.load(ctx, r, base, f[4])
		if err != nil {
			return d, err
		}
	} else if r.Info["init"] != "" {
		doc, base, err = e.load(ctx, r, r.BookSourceURL, rawURL)
		if err != nil {
			return d, err
		}
	}
	seenPages := map[string]bool{}
	seenChapters := map[string]bool{}
	d.Chapters = []Chapter{}
	for page := 0; ; page++ {
		if err = ctx.Err(); err != nil {
			return d, err
		}
		if seenPages[base] {
			break
		}
		const maxTocPages = 200
		if page >= maxTocPages {
			return d, fmt.Errorf("TOC pagination exceeds %d pages", maxTocPages)
		}
		seenPages[base] = true
		list, er := elements(doc, r.Toc["chapterList"])
		if er != nil {
			return d, fmt.Errorf("chapterList: %w", er)
		}
		for _, v := range list {
			f, er := fields(v, r.Toc, "chapterName", "chapterUrl")
			if er != nil {
				return d, er
			}
			if f[0] == "" || f[1] == "" {
				continue
			}
			u, er := resolve(base, f[1])
			if er != nil {
				return d, er
			}
			if !seenChapters[u] {
				seenChapters[u] = true
				d.Chapters = append(d.Chapters, Chapter{f[0], u})
				if len(d.Chapters) > 20000 {
					return d, fmt.Errorf("TOC exceeds 20000 chapters")
				}
			}
		}
		next, er := extract(doc, r.Toc["nextTocUrl"])
		if er != nil {
			return d, er
		}
		if next == "" {
			break
		}
		u, er := resolve(base, next)
		if er != nil {
			return d, er
		}
		if seenPages[u] {
			break
		}
		if page >= maxTocPages-1 {
			return d, fmt.Errorf("TOC pagination exceeds %d pages", maxTocPages)
		}
		doc, base, err = e.load(ctx, r, base, next)
		if err != nil {
			return d, err
		}
	}
	if len(d.Chapters) == 0 {
		return d, fmt.Errorf("no chapters matched source rules")
	}
	return d, nil
}
func (e *Engine) Content(ctx context.Context, s Source, rawURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	r, err := parseSource(s)
	if err != nil {
		return "", err
	}
	if r.Content["content"] == "" {
		return "", fmt.Errorf("source has no content rule")
	}
	if r.Content["webJs"] != "" || r.Content["sourceRegex"] != "" {
		return "", fmt.Errorf("unsupported content webJs/sourceRegex")
	}
	seen := map[string]bool{}
	chunks := map[string]bool{}
	var out []string
	total := 0
	base := r.BookSourceURL
	next := rawURL
	for page := 0; next != ""; page++ {
		if page >= 10 {
			return "", fmt.Errorf("content pagination exceeds 10 pages")
		}
		doc, final, er := e.load(ctx, r, base, next)
		if er != nil {
			return "", er
		}
		if seen[final] {
			break
		}
		seen[final] = true
		base = final
		text, er := extract(doc, r.Content["content"])
		if er != nil {
			return "", er
		}
		if text == "" {
			return "", fmt.Errorf("content rule matched no text")
		}
		if replace := r.Content["replaceRegex"]; replace != "" {
			text, er = cleanContent(text, replace)
			if er != nil {
				return "", er
			}
		}
		if !chunks[text] {
			total += len(text)
			if total > 16<<20 {
				return "", fmt.Errorf("chapter content exceeds 16 MiB")
			}
			chunks[text] = true
			out = append(out, text)
		}
		next, er = extract(doc, r.Content["nextContentUrl"])
		if er != nil {
			return "", er
		}
		if next != "" {
			next, er = resolve(base, next)
			if er != nil {
				return "", er
			}
			if seen[next] {
				break
			}
		}
	}
	return strings.Join(out, "\n\n"), nil
}
