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

	"github.com/PuerkitoBio/goquery"
)


type Source struct {
	ID   string
	Rule json.RawMessage
}

type Result struct {
	SourceID      string `json:"sourceId"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	Cover         string `json:"cover"`
	Intro         string `json:"intro"`
	LatestChapter string `json:"latestChapter,omitempty"`
	SourceName    string `json:"sourceName,omitempty"`
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
	if len(s.Rule) > 5<<20 {
		return r, fmt.Errorf("source rules exceed 5 MiB")
	}
	if err := json.Unmarshal(s.Rule, &r); err != nil {
		return r, fmt.Errorf("invalid source rules: %w", err)
	}

	for _, group := range []struct {
		name    string
		rules   map[string]string
		allowed string
	}{
		{"ruleSearch", r.Search, " bookList name author bookUrl coverUrl intro kind lastChapter latestChapter newChapter updateChapter lastUpdate wordCount checkKeyWord updateTime "},
		{"ruleBookInfo", r.Info, " init name author coverUrl intro tocUrl kind lastChapter latestChapter newChapter updateChapter lastUpdate wordCount updateTime canReName downloadUrl "},
		{"ruleToc", r.Toc, " chapterList chapterName chapterUrl nextTocUrl updateTime formatJs isVolume "},
		{"ruleContent", r.Content, " content nextContentUrl replaceRegex imageStyle title sourceRegex webView "},
	} {
		for key, rule := range group.rules {
			if strings.TrimSpace(rule) == "" {
				continue
			}
			// Legado metadata fields that do not block parsing
			if (group.name == "ruleSearch" || group.name == "ruleBookInfo") && strings.Contains(" kind lastChapter latestChapter newChapter updateChapter lastUpdate wordCount checkKeyWord updateTime canReName downloadUrl ", " "+key+" ") {
				continue
			}
			if group.name == "ruleToc" && strings.Contains(" updateTime formatJs isVolume ", " "+key+" ") {
				continue
			}
			if group.name == "ruleContent" && strings.Contains(" imageStyle title sourceRegex webView ", " "+key+" ") {
				continue
			}
			if key == "replaceRegex" {
				continue
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
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			if out == nil {
				out = map[string]string{}
			}
			return out, nil
		}
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid header line")
			}
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func resolve(base, ref string) (string, error) {
	bu, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	ru, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	u := bu.ResolveReference(ru)
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
	res := unquotedKeyRegex.ReplaceAllString(raw, `$1"$2":`)
	res = trailingCommaRegex.ReplaceAllString(res, "}")
	return res
}

func template(text, key string, page int, escaped bool) (string, error) {
	escapedKey := key
	if escaped {
		escapedKey = url.QueryEscape(key)
	}
	prevPage := strconv.Itoa(page - 1)
	if page <= 1 {
		prevPage = "1"
	}
	nextPage := strconv.Itoa(page + 1)
	text = strings.NewReplacer(
		"{{key}}", escapedKey,
		"{{page}}", strconv.Itoa(page),
		"{key}", escapedKey,
		"{page}", strconv.Itoa(page),
		"{{page-1}}", prevPage,
		"{{page+1}}", nextPage,
	).Replace(text)

	// If URL template contains JS, evaluate it
	if strings.Contains(text, "<js>") || strings.Contains(text, "@js:") {
		mainText, jsCode := SplitJSBlock(text)
		if jsCode != "" {
			env := JSEnv{
				Result: mainText,
				Src:    mainText,
				Key:    key,
				Page:   page,
			}
			val, err := RunLegadoJS(jsCode, env)
			if err == nil && val != nil {
				text = fmt.Sprintf("%v", val)
			}
		}
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
					spec.Method = strings.ToUpper(strings.TrimSpace(spec.Method))
				case "charset":
					_ = json.Unmarshal(v, &spec.Charset)
				case "headers", "header":
					var h map[string]string
					if h, err = headers(v); err == nil {
						for hk, val := range h {
							spec.Headers[hk] = val
						}
					}
				case "cookie", "cookies":
					var cStr string
					if err := json.Unmarshal(v, &cStr); err == nil && cStr != "" {
						if existing, ok := spec.Headers["Cookie"]; ok && existing != "" {
							spec.Headers["Cookie"] = existing + "; " + cStr
						} else {
							spec.Headers["Cookie"] = cStr
						}
					}
				case "webview":
					if _, hasUA := spec.Headers["User-Agent"]; !hasUA {
						spec.Headers["User-Agent"] = "Mozilla/5.0 (Linux; Android 14; Mobile) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36"
					}
				case "body":
					if len(v) > 0 && v[0] == '"' {
						_ = json.Unmarshal(v, &spec.Body)
					} else {
						spec.Body = string(v)
					}
				default:
					// Gracefully ignore harmless options like webView, retry, cacheTime
				}
			}
		}
	}
	if spec.Body != "" {
		spec.Body, err = template(spec.Body, key, page, true)
		if err != nil {
			return spec, err
		}
	}
	return spec, nil
}

func (e *Engine) load(ctx context.Context, r sourceRules, base, raw string) (value, string, error) {
	spec, err := makeRequest(r, base, raw, "", 1)
	if err != nil {
		return value{}, "", err
	}
	res, err := e.fetch(ctx, spec)
	if err != nil {
		return value{}, "", err
	}
	doc, err := document(res.Body)
	if err != nil {
		return value{}, "", err
	}
	doc.env = &JSEnv{BaseURL: res.URL, Src: string(res.Body)}
	return doc, res.URL, nil
}

var cleanChapterPrefixRegex = regexp.MustCompile(`^(最新章节[：:\s]*|更新[：:\s]*|最新[：:\s]*|正文[：:\s]*)`)

func cleanChapterTitle(t string) string {
	t = strings.TrimSpace(t)
	t = cleanChapterPrefixRegex.ReplaceAllString(t, "")
	return strings.TrimSpace(t)
}

var sniffChapterRegex = regexp.MustCompile(`(?:最新章节|最新|更新到|最新更新)[：:\s]+(第?[0-9零一二两三四五六七八九十百千万]+[章回节集部卷][^\r\n]{0,30}|[^\r\n]{1,30})`)

func sniffLatestFromIntro(intro string) (string, string) {
	m := sniffChapterRegex.FindStringSubmatch(intro)
	if len(m) > 1 {
		latest := cleanChapterTitle(m[1])
		cleanIntro := sniffChapterRegex.ReplaceAllString(intro, "")
		return latest, strings.TrimSpace(cleanIntro)
	}
	return "", intro
}

func (e *Engine) Search(ctx context.Context, s Source, keyword string, pages ...int) ([]Result, error) {
	page := 1
	if len(pages) > 0 && pages[0] > 0 {
		page = pages[0]
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := parseSource(s)
	if err != nil {
		return nil, err
	}
	if r.SearchURL == "" {
		return nil, fmt.Errorf("source missing searchUrl")
	}
	spec, err := makeRequest(r, r.BookSourceURL, r.SearchURL, keyword, page)
	if err != nil {
		return nil, err
	}
	res, err := e.fetch(ctx, spec)
	if err != nil {
		return nil, err
	}
	body, finalURL := res.Body, res.URL
	doc, err := document(body)
	if err != nil {
		return nil, err
	}
	doc.env = &JSEnv{BaseURL: finalURL, Src: string(body), Key: keyword, Page: page}

	items, err := elements(doc, r.Search["bookList"])
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(items))
	seen := map[string]bool{}
	for _, v := range items {
		v.env = doc.env
		var f [6]string
		for i, k := range []string{"name", "author", "bookUrl", "coverUrl", "intro", "lastChapter"} {
			rule := r.Search[k]
			if rule != "" {
				s, err := extract(v, rule)
				if err != nil {
					return nil, err
				}
				f[i] = s
			}
		}
		bookURL, err := resolve(finalURL, f[2])
		if err != nil {
			return nil, err
		}
		if seen[bookURL] {
			continue
		}
		seen[bookURL] = true
		cover := ""
		if f[3] != "" {
			cover, err = resolve(finalURL, f[3])
			if err != nil {
				return nil, err
			}
		}
		latest := ""
		if len(f) > 5 && strings.TrimSpace(f[5]) != "" {
			latest = strings.TrimSpace(f[5])
		}
		if latest == "" {
			for _, lk := range []string{"latestChapter", "newChapter", "updateChapter", "lastUpdate"} {
				if rule, ok := r.Search[lk]; ok && strings.TrimSpace(rule) != "" {
					if lVal, lErr := extract(v, rule); lErr == nil && strings.TrimSpace(lVal) != "" {
						latest = strings.TrimSpace(lVal)
						break
					}
				}
			}
		}
		if latest != "" {
			latest = cleanChapterTitle(latest)
		}
		introText := f[4]
		if latest == "" && introText != "" {
			if snipLatest, newIntro := sniffLatestFromIntro(introText); snipLatest != "" {
				latest = snipLatest
				introText = newIntro
			}
		}
		out = append(out, Result{SourceID: s.ID, URL: bookURL, Title: f[0], Author: f[1], Cover: cover, Intro: introText, LatestChapter: latest})
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
	var f [4]string
	for i, k := range []string{"name", "author", "coverUrl", "intro"} {
		rule := r.Info[k]
		if rule != "" {
			s, er := extract(doc, rule)
			if er != nil {
				return d, er
			}
			f[i] = s
		}
	}
	d.Title = f[0]
	d.Author = f[1]
	if f[2] != "" {
		d.Cover, err = resolve(base, f[2])
		if err != nil {
			return d, err
		}
	}
	d.Intro = f[3]
	tocURL := base
	if rule := r.Info["tocUrl"]; rule != "" {
		u, er := extract(doc, rule)
		if er != nil {
			return d, er
		}
		if u != "" {
			tocURL, err = resolve(base, u)
			if err != nil {
				return d, err
			}
		}
	}
	d.Chapters, err = e.TOC(ctx, s, tocURL)
	return d, err
}

func (e *Engine) TOC(ctx context.Context, s Source, rawURL string) ([]Chapter, error) {
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	r, err := parseSource(s)
	if err != nil {
		return nil, err
	}
	currentURL := rawURL
	visited := map[string]bool{}
	var allChapters []Chapter
	seenChapterURLs := map[string]bool{}

	maxPages := 200
	for p := 0; p < maxPages; p++ {
		if currentURL == "" || visited[currentURL] {
			break
		}
		visited[currentURL] = true

		doc, base, err := e.load(ctx, r, r.BookSourceURL, currentURL)
		if err != nil {
			if len(allChapters) > 0 {
				break
			}
			return nil, err
		}
		items, err := elements(doc, r.Toc["chapterList"])
		if err != nil {
			if len(allChapters) > 0 {
				break
			}
			return nil, err
		}
		for _, v := range items {
			name, err := extract(v, r.Toc["chapterName"])
			if err != nil {
				return nil, err
			}
			u, err := extract(v, r.Toc["chapterUrl"])
			if err != nil {
				return nil, err
			}
			absURL, err := resolve(base, u)
			if err != nil {
				return nil, err
			}
			if !seenChapterURLs[absURL] {
				seenChapterURLs[absURL] = true
				allChapters = append(allChapters, Chapter{Title: name, URL: absURL})
			}
		}

		nextRule := r.Toc["nextTocUrl"]
		if nextRule == "" {
			break
		}
		next, err := extract(doc, nextRule)
		if err != nil || next == "" {
			break
		}
		absNext, err := resolve(base, next)
		if err != nil || absNext == currentURL || visited[absNext] {
			break
		}
		currentURL = absNext
	}
	return allChapters, nil
}

func (e *Engine) Content(ctx context.Context, s Source, rawURL string, nextChapterURLs ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	r, err := parseSource(s)
	if err != nil {
		return "", err
	}

	var normNextChapterURL string
	if len(nextChapterURLs) > 0 && nextChapterURLs[0] != "" {
		if abs, err := resolve(r.BookSourceURL, nextChapterURLs[0]); err == nil {
			normNextChapterURL = strings.TrimRight(abs, "/")
		} else {
			normNextChapterURL = strings.TrimRight(nextChapterURLs[0], "/")
		}
	}

	currentURL := rawURL
	visited := map[string]bool{}
	var fullText strings.Builder
	var lastPageText string
	pages := 0
	const maxPages = 50

	for {
		if currentURL == "" || visited[currentURL] {
			break
		}
		pages++
		if pages > maxPages {
			return "", fmt.Errorf("content pagination exceeded %d pages", maxPages)
		}
		visited[currentURL] = true

		doc, base, err := e.load(ctx, r, r.BookSourceURL, currentURL)
		if err != nil {
			return "", err
		}
		text, err := extract(doc, r.Content["content"])
		if err != nil {
			return "", err
		}
		text, err = cleanContent(text, r.Content["replaceRegex"])
		if err != nil {
			return "", err
		}

		trimmedText := strings.TrimSpace(text)
		if trimmedText != "" && trimmedText == lastPageText {
			break
		}
		lastPageText = trimmedText

		if trimmedText != "" {
			if fullText.Len() > 0 {
				fullText.WriteString("\n\n")
			}
			fullText.WriteString(trimmedText)
		}

		nextURL := e.findNextContentURL(doc, base, r.Content["nextContentUrl"], currentURL, normNextChapterURL)
		if nextURL == "" || visited[nextURL] {
			break
		}
		if normNextChapterURL != "" && strings.TrimRight(nextURL, "/") == normNextChapterURL {
			break
		}
		currentURL = nextURL
	}
	return fullText.String(), nil
}

func (e *Engine) findNextContentURL(doc value, base, nextRule, currentURL, normNextChapterURL string) string {
	normCurr := strings.TrimRight(currentURL, "/")

	// 1. 书源自带规则优先
	cleanNextRule := strings.TrimSpace(nextRule)
	if cleanNextRule != "" && !strings.Contains(cleanNextRule, "???") {
		if next, err := extract(doc, cleanNextRule); err == nil && strings.TrimSpace(next) != "" {
			if absNext, err := resolve(base, strings.TrimSpace(next)); err == nil && absNext != "" {
				normNext := strings.TrimRight(absNext, "/")
				if normNext != normCurr {
					if normNextChapterURL != "" && normNext == normNextChapterURL {
						return ""
					}
					return absNext
				}
			}
		}
	}

	// 2. 启发式 Fallback：当规则未配置、失效或未提取到下一页时，智能探测 DOM 中的分页链接
	if doc.node != nil {
		var candidate string
		doc.node.Find("a").EachWithBreak(func(_ int, s *goquery.Selection) bool {
			href, exists := s.Attr("href")
			if !exists {
				return true
			}
			href = strings.TrimSpace(href)
			if href == "" || href == "#" || strings.HasPrefix(strings.ToLower(href), "javascript:") {
				return true
			}

			txt := strings.TrimSpace(s.Text())
			// 严格排除章节、上页、目录等非子分页链接
			if strings.Contains(txt, "下一章") || strings.Contains(txt, "下章") ||
				strings.Contains(txt, "上一页") || strings.Contains(txt, "上页") ||
				strings.Contains(txt, "上一章") || strings.Contains(txt, "上章") ||
				strings.Contains(txt, "下一卷") || strings.Contains(txt, "下卷") ||
				strings.Contains(txt, "下一篇") || strings.Contains(txt, "下篇") ||
				strings.Contains(txt, "目录") || strings.Contains(txt, "书架") ||
				strings.Contains(txt, "首页") || strings.Contains(txt, "返回") {
				return true
			}

			isNextPage := txt == "下一页" || txt == "下页" || txt == "下一頁" || txt == "次頁" || txt == "翻下页" ||
				strings.Contains(txt, "下一页") || strings.Contains(txt, "下一頁")

			id, _ := s.Attr("id")
			class, _ := s.Attr("class")
			if !isNextPage {
				if (id == "pb_next" || strings.Contains(class, "page_down") || strings.Contains(class, "nextpage")) &&
					!strings.Contains(txt, "章") && !strings.Contains(txt, "卷") {
					isNextPage = true
				}
			}

			if isNextPage {
				abs, err := resolve(base, href)
				if err == nil && abs != "" {
					normAbs := strings.TrimRight(abs, "/")
					if normAbs != normCurr {
						if normNextChapterURL != "" && normAbs == normNextChapterURL {
							candidate = ""
							return false
						}
						candidate = abs
						return false
					}
				}
			}
			return true
		})

		if candidate != "" {
			return candidate
		}
	}

	return ""
}
