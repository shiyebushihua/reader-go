package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureEngine(t *testing.T, pages map[string]string) *Engine {
	t.Helper()
	e := New()
	e.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		s, ok := pages[r.URL.RequestURI()]
		if !ok {
			return nil, fmt.Errorf("missing fixture %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(s)), Request: r}, nil
	})
	return e
}
func TestHTMLFlow(t *testing.T) {
	e := fixtureEngine(t, map[string]string{
		"/search?q=hello+world&page=1": `<ul><li class="book"><a href="/book">Novel</a><b>Author</b><img src="/cover.jpg"></li></ul>`,
		"/book":                        `<h1>Novel</h1><b>Author</b><a class="toc" href="/toc">TOC</a>`,
		"/toc":                         `<a class="chapter" href="/c1">First</a><a class="next" href="/toc2">next</a>`,
		"/toc2":                        `<a class="chapter" href="/c1">First</a><a class="chapter" href="/c2">Second</a><a class="next" href="/toc">loop</a>`,
		"/c1":                          `<div id="content"><p>Hello</p><script>bad()</script>world<br>again</div><a class="next" href="/c1b">next</a>`,
		"/c1b":                         `<div id="content">The end</div><a class="next" href="/c1">loop</a>`,
	})
	s := Source{ID: "s", Rule: json.RawMessage(`{"bookSourceUrl":"https://books.example","searchUrl":"/search?q={{key}}&page={{page}}","ruleSearch":{"bookList":"class.book","name":"tag.a@text","author":"b@text","bookUrl":"a@href","coverUrl":"img@src"},"ruleBookInfo":{"name":"h1@text","author":"b@text","tocUrl":"a.toc@href"},"ruleToc":{"chapterList":"a.chapter","chapterName":"text","chapterUrl":"href","nextTocUrl":"a.next@href"},"ruleContent":{"content":"id.content@html","nextContentUrl":"a.next@href"}}`)}
	results, err := e.Search(context.Background(), s, "hello world")
	if err != nil || len(results) != 1 {
		t.Fatalf("search: %+v %v", results, err)
	}
	if results[0].URL != "https://books.example/book" || results[0].Cover != "https://books.example/cover.jpg" {
		t.Fatal(results)
	}
	d, err := e.Details(context.Background(), s, results[0].URL)
	if err != nil || len(d.Chapters) != 2 || d.Title != "Novel" {
		t.Fatalf("details: %+v %v", d, err)
	}
	text, err := e.Content(context.Background(), s, d.Chapters[0].URL)
	if err != nil || text != "Hello\n\nworld\n\nagain\n\nThe end" {
		t.Fatalf("content %q %v", text, err)
	}
}
func TestJSONRulesAndOptions(t *testing.T) {
	v, err := document([]byte(`{"data":{"books":[{"title":"Book","url":"/b"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	list, err := elements(v, "$.data.books[*]")
	if err != nil || len(list) != 1 {
		t.Fatal(err, list)
	}
	s, err := extract(list[0], "$.missing||$['title']##Book##Novel")
	if err != nil || s != "Novel" {
		t.Fatal(s, err)
	}
	r := sourceRules{BookSourceURL: "https://books.example", Header: json.RawMessage(`{"X-Test":"yes"}`)}
	spec, err := makeRequest(r, r.BookSourceURL, `/search,{"method":"POST","body":"q={{key}}&p={{page}}","headers":{"Content-Type":"application/x-www-form-urlencoded"},"charset":"gbk"}`, "中文", 2)
	if err != nil || spec.Method != "POST" || spec.Body != "q=%E4%B8%AD%E6%96%87&p=2" || spec.Charset != "gbk" || spec.Headers["X-Test"] != "yes" {
		t.Fatalf("%+v %v", spec, err)
	}
	// Legado 3.0 supports embedded JS, XPath, and JSONPath processing on documents
	jsVal, err := extract(v, "@js:JSON.parse(result).data.books[0].title")
	if err != nil || jsVal != "Book" {
		t.Fatalf("Legado JS extraction failed: %v, got: %s", err, jsVal)
	}
	h, _ := document([]byte(`<div class="x"><a>A</a><a>B</a></div>`))
	s, err = extract(h, "class.x@tag.a.1@text&&.missing@text")
	if err != nil || s != "B" {
		t.Fatal(s, err)
	}
	h2, _ := document([]byte(`<div><ul><li>A</li></ul><ul><li>B</li></ul><ul><li><a href="/c1">First</a></li><li><a href="/c2">Second</a></li></ul><a href="/next">下一页</a><p>P1</p><p>P2</p></div>`))
	chaps, err := elements(h2, "ul.2@li a")
	if err != nil || len(chaps) != 2 {
		t.Fatalf("ul.2@li a failed: %v len=%d", err, len(chaps))
	}
	cname, _ := extract(chaps[0], "text")
	curl, _ := extract(chaps[0], "href")
	if cname != "First" || curl != "/c1" {
		t.Fatalf("chapter mismatch: %s %s", cname, curl)
	}
	next, err := extract(h2, "text.下一页@href")
	if err != nil || next != "/next" {
		t.Fatalf("text.下一页@href failed: %s %v", next, err)
	}
	lastP, err := extract(h2, "p.-1@text")
	if err != nil || lastP != "P2" {
		t.Fatalf("p.-1@text failed: %s %v", lastP, err)
	}
	if _, err = extract(h, "[broken@text"); err == nil {
		t.Fatal("invalid CSS accepted")
	}
}
func TestFetchSecurity(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::1", "::ffff:127.0.0.1", "fc00::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicIP(netip.MustParseAddr(s)) {
			t.Errorf("accepted %s", s)
		}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
	e := New()
	if e.client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	for _, s := range []string{"http://127.0.0.1/", "file:///etc/passwd", "http://user:pass@books.example/", "http://[::1]/"} {
		if _, err := e.FetchSource(context.Background(), s); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.FetchSource(ctx, "https://books.example"); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := publicDial(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("private dial accepted")
	}
}
func TestRedirectRechecked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://127.0.0.1/private", 302) }))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	e := New()
	tr := e.client.Transport.(*http.Transport)
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Host)
	}
	if _, err := e.FetchSource(context.Background(), "http://books.example/"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("redirect accepted: %v", err)
	}
}
func TestBoundsGzipCharsetAndSource(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	_, _ = gz.Write([]byte(strings.Repeat("x", maxResponse+1)))
	_ = gz.Close()
	e := New()
	e.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": []string{"gzip"}}, Body: io.NopCloser(bytes.NewReader(b.Bytes())), Request: r}, nil
	})
	if _, err := e.FetchSource(context.Background(), "https://books.example"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatal(err)
	}
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中文"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCharset(encoded, "gbk", "")
	if err != nil || string(decoded) != "中文" {
		t.Fatal(string(decoded), err)
	}
	e = fixtureEngine(t, map[string]string{"/sources": `[{"bookSourceName":"fixture"}]`, "/bad": `<html>not json</html>`})
	if _, err = e.FetchSource(context.Background(), "https://books.example/sources"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.FetchSource(context.Background(), "https://books.example/bad"); err == nil {
		t.Fatal("invalid source accepted")
	}
}
func TestPaginationBudget(t *testing.T) {
	pages := map[string]string{}
	for i := 0; i < 51; i++ {
		pages[fmt.Sprintf("/p%d", i)] = fmt.Sprintf(`<p>Page %d</p><a href="/p%d">next</a>`, i, i+1)
	}
	e := fixtureEngine(t, pages)
	s := Source{Rule: json.RawMessage(`{"bookSourceUrl":"https://books.example","ruleContent":{"content":"p@text","nextContentUrl":"a@href"}}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := e.Content(ctx, s, "/p0"); err == nil || !strings.Contains(err.Error(), "50 pages") {
		t.Fatal(err)
	}
}

func TestDialValidatesAllAnswersAndPinsIP(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ips      []netip.Addr
		wantDial bool
	}{
		{"mixed", []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, false},
		{"private", []netip.Addr{netip.MustParseAddr("10.1.2.3")}, false},
		{"empty", nil, false},
		{"public", []netip.Addr{netip.MustParseAddr("8.8.8.8")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups, dials := 0, 0
			_, err := dialPublic(context.Background(), "tcp", "books.example:443",
				func(_ context.Context, network, host string) ([]netip.Addr, error) {
					lookups++
					if network != "ip" || host != "books.example" {
						t.Fatal(network, host)
					}
					return tc.ips, nil
				},
				func(_ context.Context, network, address string) (net.Conn, error) {
					dials++
					if address != "8.8.8.8:443" || network != "tcp" {
						t.Fatal("unvalidated dial", address)
					}
					return nil, fmt.Errorf("fixture dial failure")
				})
			if err == nil || lookups != 1 || (dials == 1) != tc.wantDial {
				t.Fatal(err, lookups, dials)
			}
		})
	}
}

func TestRedirectCredentialsAndLimit(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		target string
		keep   bool
	}{
		{"https://books.example/next", true},
		{"https://other.example/next", false},
		{"http://books.example/next", false},
	} {
		first, _ := http.NewRequest("GET", "https://books.example/start", nil)
		next, _ := http.NewRequest("GET", tc.target, nil)
		for _, key := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
			next.Header.Set(key, "secret")
		}
		if err := e.client.CheckRedirect(next, []*http.Request{first}); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
			if (next.Header.Get(key) != "") != tc.keep {
				t.Fatalf("%s leaked or removed at %s", key, tc.target)
			}
		}
		if err := e.client.CheckRedirect(next, make([]*http.Request, 5)); err == nil {
			t.Fatal("redirect budget ignored")
		}
	}
}

func TestUnsupportedSourceRulesFailBeforeFetch(t *testing.T) {
	for _, raw := range []string{
		`{"bookSourceUrl":""}`,
		`{"bookSourceUrl":"ftp://books.example"}`,
		`{"bookSourceUrl":"http://127.0.0.1:8080"}`,
	} {
		e := New()
		s := Source{Rule: json.RawMessage(raw)}
		if _, err := e.Search(context.Background(), s, "x"); err == nil {
			t.Fatalf("expected error for %s, got nil", raw)
		}
	}
}

func TestRuleBoundsAndMalformedJSON(t *testing.T) {
	for _, text := range []string{`{} garbage`, `{} {}`, `[1,]`} {
		if _, err := document([]byte(text)); err == nil {
			t.Fatal("invalid JSON accepted", text)
		}
	}
	v := value{data: map[string]any{"items": make([]any, 10001)}}
	if _, err := elements(v, "$.items&&$.items"); err == nil || !strings.Contains(err.Error(), "20000") {
		t.Fatal(err)
	}
	for _, rule := range []string{`$['a','b']`, `$['']`, `$.items[-1]`, `$.items[0:2]`} {
		if _, err := extract(v, rule); err == nil {
			t.Fatal("unsupported path accepted", rule)
		}
	}
	spec, err := makeRequest(sourceRules{Header: json.RawMessage(`"null"`)}, "https://books.example", `/s,{"headers":{"X-Test":"yes"}}`, "", 1)
	if err != nil || spec.Headers["X-Test"] != "yes" {
		t.Fatal(spec, err)
	}
}

func TestJSONFlow(t *testing.T) {
	e := fixtureEngine(t, map[string]string{
		"/search?q=novel": `{"items":[{"name":"Novel","url":"/b"},{"name":"Duplicate","url":"/b#x"}]}`,
		"/b":              `{"name":"Novel","toc":"/toc"}`,
		"/toc":            `{"chapters":[{"title":"One","url":"/c"}]}`,
		"/c":              `{"text":"Hello AD world"}`,
	})
	s := Source{ID: "json", Rule: json.RawMessage(`{"bookSourceUrl":"https://books.example","searchUrl":"/search?q={{key}}","ruleSearch":{"bookList":"$.items","name":"$.name","bookUrl":"$.url"},"ruleBookInfo":{"name":"$.name","tocUrl":"$.toc"},"ruleToc":{"chapterList":"$.chapters","chapterName":"$.title","chapterUrl":"$.url"},"ruleContent":{"content":"$.text","replaceRegex":"AD ##"}}`)}
	results, err := e.Search(context.Background(), s, "novel")
	if err != nil || len(results) != 1 || results[0].SourceID != "json" {
		t.Fatal(results, err)
	}
	d, err := e.Details(context.Background(), s, results[0].URL)
	if err != nil || len(d.Chapters) != 1 {
		t.Fatal(d, err)
	}
	text, err := e.Content(context.Background(), s, d.Chapters[0].URL)
	if err != nil || text != "Hello world" {
		t.Fatal(text, err)
	}
}

func TestFetchFailureBounds(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		status               int
		encoding, body, want string
	}{
		{"status", 403, "", "denied", "status 403"},
		{"encoding", 200, "br", "x", "unsupported response"},
		{"gzip", 200, "gzip", "not a gzip stream", "gzip"},
		{"raw-size", 200, "", strings.Repeat("x", maxResponse+1), "limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			e.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Encoding": []string{tc.encoding}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			if _, err := e.fetch(context.Background(), requestSpec{URL: "https://books.example"}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
	e := fixtureEngine(t, nil)
	for _, spec := range []requestSpec{{URL: "https://books.example", Method: "DELETE"}, {URL: "https://books.example", Body: strings.Repeat("x", 1<<20+1)}} {
		if _, err := e.fetch(context.Background(), spec); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestHaitangRegexAndTocFlow(t *testing.T) {
	e := fixtureEngine(t, map[string]string{
		"/book/56817/": `<meta property="og:novel:book_name" content="都市偷心龙爪手"/>
<meta property="og:novel:author" content="wtw1974"/>
<meta property="og:novel:category" content="都市言情"/>
<meta property="og:novel:description" content="简介测试"/>
<meta property="og:image" content="https://img.example/cover.jpg"/>
<meta property="og:novel:latest_chapter_name" content="第100章 结局"/>
<ul><li>Header1</li></ul>
<ul><li>Header2</li></ul>
<ul>
  <li><a href="/chapter/1.html">第一章 初遇</a></li>
  <li><a href="/chapter/2.html">第二章 龙爪手</a></li>
</ul>
<a href="/book/56817_2/">下一页</a>`,
		"/book/56817_2/": `<ul><li>H1</li></ul><ul><li>H2</li></ul>
<ul>
  <li><a href="/chapter/3.html">第三章 展威</a></li>
</ul>`,
		"/chapter/1.html": `<div id="content"><p>第一章正文内容</p></div>`,
	})

	s := Source{
		ID: "haitang",
		Rule: json.RawMessage(`{
			"bookSourceUrl": "https://m.haitang4.com#🎃",
			"bookSourceName": "海棠书屋🎃#3",
			"ruleBookInfo": {
				"name": "##:book_name\"[^\"]+\"([^\"]*)##$1###",
				"author": "##:author\"[^\"]+\"([^\"]*)##$1###",
				"kind": "##:category\"[^\"]+\"([^\"]*)##$1###",
				"intro": "##:description\"[^\"]+\"([^\"]*)##$1###",
				"coverUrl": "##og:image\"[^\"]+\"([^\"]*)##$1###",
				"lastChapter": "##_chapter_name\"[^\"]+\"([^\"]*)##$1###"
			},
			"ruleToc": {
				"chapterList": "ul.2@li a",
				"chapterName": "text",
				"chapterUrl": "href",
				"nextTocUrl": "text.下一页@href"
			},
			"ruleContent": {
				"content": "div#content@html",
				"nextContentUrl": "text.下一页@href"
			}
		}`),
	}

	d, err := e.Details(context.Background(), s, "https://m.haitang4.com/book/56817/")
	if err != nil {
		t.Fatalf("Details failed: %v", err)
	}
	if d.Title != "都市偷心龙爪手" {
		t.Errorf("Title = %q, want '都市偷心龙爪手'", d.Title)
	}
	if d.Author != "wtw1974" {
		t.Errorf("Author = %q, want 'wtw1974'", d.Author)
	}
	if d.Cover != "https://img.example/cover.jpg" {
		t.Errorf("Cover = %q, want 'https://img.example/cover.jpg'", d.Cover)
	}
	if d.Intro != "简介测试" {
		t.Errorf("Intro = %q, want '简介测试'", d.Intro)
	}
	if len(d.Chapters) != 3 {
		t.Fatalf("Chapters count = %d, want 3", len(d.Chapters))
	}
	if d.Chapters[0].Title != "第一章 初遇" || d.Chapters[0].URL != "https://m.haitang4.com/chapter/1.html" {
		t.Errorf("Chapter 0 mismatch: %+v", d.Chapters[0])
	}
	if d.Chapters[2].Title != "第三章 展威" || d.Chapters[2].URL != "https://m.haitang4.com/chapter/3.html" {
		t.Errorf("Chapter 2 mismatch: %+v", d.Chapters[2])
	}

	content, err := e.Content(context.Background(), s, d.Chapters[0].URL)
	if err != nil {
		t.Fatalf("Content failed: %v", err)
	}
	if !strings.Contains(content, "第一章正文内容") {
		t.Errorf("Content mismatch: %q", content)
	}
}

func TestLegado3RulesCompatibility(t *testing.T) {
	htmlDoc := `
	<div id="main">
		<div class="header"><h1>书名测试</h1><span class="author">作者测试</span></div>
		<ul class="catalog">
			<li class="header-item"><span>目录表头</span></li>
			<li class="item"><a href="/chap/1">第一回 楔子</a><span>字数: 3000</span></li>
			<li class="item"><a href="/chap/2">第二回 启程</a><span>字数: 3200</span></li>
			<li class="item"><a href="/chap/3">第三回 探秘</a><span>字数: 3500</span></li>
		</ul>
		<div class="content-box">
			<script>var ad = 1;</script>
			<p>段落一：这是正文内容。</p>
			<p>段落二：包含广告水印（请访问 www.bad.com 购买）。</p>
			<p>段落三：结尾。</p>
		</div>
	</div>
	`
	docVal, err := document([]byte(htmlDoc))
	if err != nil {
		t.Fatalf("document parse failed: %v", err)
	}

	// 1. 测试 @@ 前缀与 @put:{...} 剥离
	t1, err := extract(docVal, "@@.header h1@put:{id: \"123\"}@text")
	if err != nil || t1 != "书名测试" {
		t.Errorf("@@ & @put extraction failed: %q, err=%v", t1, err)
	}

	// 2. 测试方括号索引 bracketIndexPattern
	firstChap, err := extract(docVal, "ul.catalog li.item a[0]@text")
	if err != nil || firstChap != "第一回 楔子" {
		t.Errorf("bracket index [0] failed: %q, err=%v", firstChap, err)
	}
	lastChap, err := extract(docVal, "ul.catalog li.item a[-1]@text")
	if err != nil || lastChap != "第三回 探秘" {
		t.Errorf("bracket index [-1] failed: %q, err=%v", lastChap, err)
	}

	// 3. 测试排除索引 legacyExclude (!0 排除表头)
	filteredChaps, err := elements(docVal, "ul.catalog@tag.li!0")
	if err != nil || len(filteredChaps) != 3 {
		t.Fatalf("exclude index !0 failed: len=%d, err=%v", len(filteredChaps), err)
	}

	// 4. 测试 textNodes 与 ownText
	own, err := extract(docVal, ".catalog li.item[0]@ownText")
	if err != nil {
		t.Errorf("ownText failed: %v", err)
	}
	_ = own

	// 5. 测试 html / all 提取
	innerH, err := extract(docVal, ".content-box@html")
	if err != nil || strings.Contains(innerH, "var ad = 1") || !strings.Contains(innerH, "段落一") {
		t.Errorf("html attr should extract content and remove script: %q, err=%v", innerH, err)
	}

	// 6. 测试 cleanContent 多行正则清洗与注释忽略
	dirtyContent := "段落一：这是正文内容。\n段落二：包含广告水印（请访问 www.bad.com 购买）。\n段落三：结尾。"
	cleanRules := `
	// 这是注释行，应该被忽略
	（请访问.*?购买）##
	www\.bad\.com##
	`
	cleaned, err := cleanContent(dirtyContent, cleanRules)
	if err != nil {
		t.Fatalf("cleanContent failed: %v", err)
	}
	if strings.Contains(cleaned, "www.bad.com") || strings.Contains(cleaned, "购买") {
		t.Errorf("cleaned content still contains ad: %q", cleaned)
	}

	// 7. 测试书源包含丰富 Legado 扩展字段时的 parseSource 兼容性
	richSource := Source{
		ID: "rich_source",
		Rule: json.RawMessage(`{
			"bookSourceUrl": "https://rich.example",
			"searchUrl": "/search?key={{key}}",
			"ruleSearch": {
				"bookList": ".item",
				"name": "h2@text",
				"author": "span.author@text",
				"bookUrl": "a@href",
				"wordCount": "span.words@text",
				"lastChapter": "span.last@text",
				"updateTime": "span.time@text"
			},
			"ruleBookInfo": {
				"name": "h1@text",
				"author": "span.author@text",
				"kind": "span.kind@text",
				"wordCount": "span.words@text",
				"updateTime": "span.time@text",
				"canReName": "true"
			},
			"ruleToc": {
				"chapterList": "ul.list li a",
				"chapterName": "text",
				"chapterUrl": "href",
				"updateTime": "span.time@text"
			},
			"ruleContent": {
				"content": "#content@html",
				"imageStyle": "0",
				"title": "h1@text",
				"replaceRegex": "// comment\n广告##"
			}
		}`),
	}
	parsed, err := parseSource(richSource)
	if err != nil {
		t.Fatalf("parseSource with rich Legado fields failed: %v", err)
	}
	if parsed.BookSourceURL != "https://rich.example" {
		t.Errorf("parsed URL mismatch: %s", parsed.BookSourceURL)
	}
}
func TestLatestChapterExtractionAndSniff(t *testing.T) {
	cleaned := cleanChapterTitle("最新章节：第105章 大结局（终）")
	if cleaned != "第105章 大结局（终）" {
		t.Errorf("cleanChapterTitle failed, got: %q", cleaned)
	}

	sniffed, newIntro := sniffLatestFromIntro("最新：第99章 决战天巅\n这是简介内容第一行\n这是第二行")
	if sniffed != "第99章 决战天巅" {
		t.Errorf("sniffLatestFromIntro failed, got: %q", sniffed)
	}
	if strings.Contains(newIntro, "最新：") {
		t.Errorf("newIntro still contains prefix, got: %q", newIntro)
	}
}

func TestLegadoGJSONAndXPath(t *testing.T) {
	// 1. JSONPath / GJSON test
	jsonDoc := []byte(`{
		"code": 0,
		"data": {
			"list": [
				{"id": "1", "name": "逆天邪神", "author": "火星引力"},
				{"id": "2", "name": "诡秘之主", "author": "爱潜水的乌贼"}
			]
		}
	}`)
	doc, err := document(jsonDoc)
	if err != nil {
		t.Fatalf("document json failed: %v", err)
	}
	items, err := elements(doc, "$.data.list[*]")
	if err != nil || len(items) != 2 {
		t.Fatalf("elements JSONPath failed: len=%d, err=%v", len(items), err)
	}
	name1, err := extract(items[0], "$.name")
	if err != nil || name1 != "逆天邪神" {
		t.Errorf("extract JSONPath name1 failed: %q, err=%v", name1, err)
	}

	// 2. XPath test
	htmlDoc := []byte(`
		<html>
		<body>
			<div class="main">
				<ul class="chapters">
					<li><a href="/c1.html">第一章 降临</a></li>
					<li><a href="/c2.html">第二章 觉醒</a></li>
				</ul>
				<div id="content">
					<p>正文段落一</p>
					<p>正文段落二</p>
				</div>
			</div>
		</body>
		</html>
	`)
	hDoc, err := document(htmlDoc)
	if err != nil {
		t.Fatalf("document html failed: %v", err)
	}
	chaps, err := elements(hDoc, "//ul[@class='chapters']/li/a")
	if err != nil || len(chaps) != 2 {
		t.Fatalf("elements XPath failed: len=%d, err=%v", len(chaps), err)
	}
	c1Name, err := extract(chaps[0], "text")
	if err != nil || !strings.Contains(c1Name, "第一章") {
		t.Errorf("extract c1Name failed: %q, err=%v", c1Name, err)
	}
}

func TestLegadoJSSandboxAndPipeline(t *testing.T) {
	// 1. JS with Base64 & MD5 & Replace
	env := JSEnv{
		Result:  "aGVsbG8gd29ybGQ=", // base64 of "hello world"
		BaseURL: "https://example.com",
	}
	script := `
		let decoded = java.base64Decode(result);
		let md5Val = java.md5("test");
		decoded.toUpperCase() + "_" + md5Val.substring(0, 4)
	`
	res, err := RunLegadoJS(script, env)
	if err != nil {
		t.Fatalf("RunLegadoJS failed: %v", err)
	}
	resStr := fmt.Sprintf("%v", res)
	if !strings.HasPrefix(resStr, "HELLO WORLD_098f") {
		t.Errorf("unexpected JS result: %s", resStr)
	}

	// 2. Rule with embedded <js> block and regex clean
	htmlDoc := []byte(`
		<div class="content">
			<p>本章广告：购买会员更优惠</p>
			<p>林轩眼神微冷，抬手便是一剑！</p>
		</div>
	`)
	doc, err := document(htmlDoc)
	if err != nil {
		t.Fatalf("document failed: %v", err)
	}
	rule := `.content p@text##本章广告[^\n]*<js>result.replace("！", "。")</js>`
	cleaned, err := extract(doc, rule)
	if err != nil {
		t.Fatalf("extract with JS pipeline failed: %v", err)
	}
	if strings.Contains(cleaned, "本章广告") {
		t.Errorf("cleaning regex failed, still contains ad: %q", cleaned)
	}
	if !strings.Contains(cleaned, "抬手便是一剑。") {
		t.Errorf("JS replace failed, expected 。 but got: %q", cleaned)
	}
}

func TestLegadoFullParityFeatures(t *testing.T) {
	// 1. AES decode and encode
	key := "1234567890123456"
	iv := "1234567890123456"
	plain := "Hello Legado 3.0"
	env := JSEnv{
		Storage: NewLegadoStorage(),
	}
	script := fmt.Sprintf(`
		let enc = java.aesEncode("%s", "%s", "%s");
		let dec = java.aesDecode("%s", "%s", enc);
		dec;
	`, key, iv, plain, key, iv)
	res, err := RunLegadoJS(script, env)
	if err != nil {
		t.Fatalf("RunLegadoJS AES failed: %v", err)
	}
	if fmt.Sprintf("%v", res) != plain {
		t.Errorf("AES decode got %q, want %q", res, plain)
	}

	// 2. createSymmetricCrypto
	scriptCrypto := fmt.Sprintf(`
		let crypto = java.createSymmetricCrypto("AES/CBC/PKCS7Padding", "%s", "%s");
		let enc = crypto.encryptStr("%s");
		crypto.decryptStr(enc);
	`, key, iv, plain)
	resCrypto, err := RunLegadoJS(scriptCrypto, env)
	if err != nil {
		t.Fatalf("createSymmetricCrypto failed: %v", err)
	}
	if fmt.Sprintf("%v", resCrypto) != plain {
		t.Errorf("createSymmetricCrypto got %q, want %q", resCrypto, plain)
	}

	// 3. GBK encodeURI
	scriptGBK := `java.encodeURI("小说", "gbk")`
	resGBK, err := RunLegadoJS(scriptGBK, env)
	if err != nil {
		t.Fatalf("GBK encodeURI failed: %v", err)
	}
	if !strings.Contains(fmt.Sprintf("%v", resGBK), "%") {
		t.Errorf("GBK encodeURI expected percent-encoding, got %q", resGBK)
	}

	// 4. Time format
	scriptTime := `java.timeFormat(1704067200000, "yyyy-MM-dd")`
	resTime, err := RunLegadoJS(scriptTime, env)
	if err != nil {
		t.Fatalf("timeFormat failed: %v", err)
	}
	if !strings.HasPrefix(fmt.Sprintf("%v", resTime), "2024-01-01") {
		t.Errorf("timeFormat got %q", resTime)
	}

	// 5. Storage and @get:{var} in rule
	env.Storage.Put("bookPrefix", "仙侠")
	doc, _ := document([]byte(`<div><span class="title">凡人修仙传</span></div>`))
	doc.env = &env
	resVar, err := extract(doc, `@get:{bookPrefix} && span.title@text`)
	if err != nil {
		t.Fatalf("extract with @get failed: %v", err)
	}
	if !strings.Contains(resVar, "仙侠") || !strings.Contains(resVar, "凡人修仙传") {
		t.Errorf("extract with @get got %q", resVar)
	}
}

func TestNormalizeContentParagraphs(t *testing.T) {
	raw := "第一段话开头，他说：“今天天气真好\n”\n第二段话很激动！\n！\n第三段正常文字。"
	cleaned := normalizeContentParagraphs(raw)
	if strings.Contains(cleaned, "\n”") || strings.Contains(cleaned, "\n\n”") {
		t.Errorf("isolated quotation was not merged: %q", cleaned)
	}
	if strings.Contains(cleaned, "\n！") || strings.Contains(cleaned, "\n\n！") {
		t.Errorf("isolated exclamation was not merged: %q", cleaned)
	}
	if !strings.Contains(cleaned, "今天天气真好”") {
		t.Errorf("expected quote merged with text: %q", cleaned)
	}
}

func TestAutoChapterPagination(t *testing.T) {
	pages := map[string]string{
		"/chapter/1.html":   `<div id="content">第一章第一页内容。(第1/3页)</div><a href="/chapter/1_2.html" id="pb_next">下一页</a>`,
		"/chapter/1_2.html": `<div id="content">第一章第二页内容。(第2/3页)</div><a href="/chapter/1_3.html" id="pb_next">下一页</a>`,
		"/chapter/1_3.html": `<div id="content">第一章第三页内容。(第3/3页)</div><a href="/chapter/2.html" id="pb_next">下一章</a>`,
		"/chapter/2.html":   `<div id="content">这是第二章内容，绝不能被当作第一章分页读入！</div>`,
	}
	e := fixtureEngine(t, pages)

	// 场景 1: 规则正常提取 nextContentUrl，遇到下一章 stop
	s1 := Source{Rule: json.RawMessage(`{
		"bookSourceUrl": "https://books.example",
		"ruleContent": {
			"content": "div#content@text",
			"nextContentUrl": "a#pb_next@href"
		}
	}`)}
	content1, err := e.Content(context.Background(), s1, "https://books.example/chapter/1.html", "https://books.example/chapter/2.html")
	if err != nil {
		t.Fatalf("Content failed: %v", err)
	}
	if !strings.Contains(content1, "第一章第一页内容") ||
		!strings.Contains(content1, "第一章第二页内容") ||
		!strings.Contains(content1, "第一章第三页内容") {
		t.Errorf("content1 missing subpages: %s", content1)
	}
	if strings.Contains(content1, "这是第二章内容") {
		t.Errorf("content1 leaked into next chapter: %s", content1)
	}

	// 场景 2: 书源没有配置 nextContentUrl（甚至为损坏字符串），依靠启发式 DOM 分析下一页
	s2 := Source{Rule: json.RawMessage(`{
		"bookSourceUrl": "https://books.example",
		"ruleContent": {
			"content": "div#content@text",
			"nextContentUrl": "text.???@href"
		}
	}`)}
	content2, err := e.Content(context.Background(), s2, "https://books.example/chapter/1.html", "https://books.example/chapter/2.html")
	if err != nil {
		t.Fatalf("Content with heuristic fallback failed: %v", err)
	}
	if !strings.Contains(content2, "第一章第一页内容") ||
		!strings.Contains(content2, "第一章第二页内容") ||
		!strings.Contains(content2, "第一章第三页内容") {
		t.Errorf("content2 heuristic missing subpages: %s", content2)
	}
	if strings.Contains(content2, "这是第二章内容") {
		t.Errorf("content2 heuristic leaked into next chapter: %s", content2)
	}
}
