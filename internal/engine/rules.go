package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/antchfx/htmlquery"
	"github.com/tidwall/gjson"
	"golang.org/x/net/html"
)

type value struct {
	node *goquery.Selection
	data any
	raw  string
	env  *JSEnv
}

func (v value) rawString() string {
	if v.raw != "" {
		return v.raw
	}
	if v.node != nil {
		h, err := v.node.Html()
		if err == nil && h != "" {
			return h
		}
		if len(v.node.Nodes) > 0 {
			return plainText(v.node.Nodes[0])
		}
	}
	if v.data != nil {
		if s, ok := v.data.(string); ok {
			return s
		}
		b, _ := json.Marshal(v.data)
		return string(b)
	}
	return ""
}

func document(body []byte) (value, error) {
	text := strings.TrimPrefix(strings.TrimSpace(string(body)), "\ufeff")
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		var v any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&v); err == nil {
			// Ensure no trailing tokens after valid JSON
			if dec.More() {
				return value{}, fmt.Errorf("trailing garbage after JSON")
			}
			var dummy any
			if dec.Decode(&dummy) == nil {
				return value{}, fmt.Errorf("extra JSON data found")
			}
			return value{data: v, raw: text}, nil
		}
		return value{}, fmt.Errorf("malformed JSON document")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(text))
	if err != nil {
		return value{}, err
	}
	return value{node: doc.Selection, raw: text}, nil
}

func cleanRule(rule string) string {
	rule = putPattern.ReplaceAllString(rule, "")
	rule = strings.TrimPrefix(rule, "@@")
	rule = strings.TrimPrefix(rule, "@css:")
	rule = strings.TrimPrefix(rule, "css:")
	return rule
}

func supported(rule string) error {
	// Legado 3.0 supports CSS, XPath, JSONPath, Regular Expressions, and embedded JS.
	// We no longer reject these rules.
	return nil
}

func extract(v value, rule string) (string, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "", nil
	}

	// 1. Check for embedded JS (<js>...</js> or @js:...)
	// 0. If rule is purely @get:{key}, return the stored variable directly
	if v.env != nil && v.env.Storage != nil {
		trimmed := strings.TrimSpace(rule)
		if strings.HasPrefix(trimmed, "@get:{") && strings.HasSuffix(trimmed, "}") && !strings.ContainsAny(trimmed, "&|%#") {
			key := strings.TrimSuffix(strings.TrimPrefix(trimmed, "@get:{"), "}")
			return v.env.Storage.Get(strings.TrimSpace(key)), nil
		}
	}
	// 1. Check for embedded JS (<js>...</js> or @js:...)
	mainRule, jsCode := SplitJSBlock(rule)
	mainRule = cleanRule(mainRule)
	var text string
	var err error

	// 2. If mainRule is empty but we have JS, evaluate directly on raw
	if mainRule == "" && jsCode != "" {
		env := JSEnv{
			Result:  v.rawString(),
			Src:     v.rawString(),
			BaseURL: "",
		}
		if v.env != nil {
			env = *v.env
			env.Result = v.rawString()
		}
		val, jsErr := RunLegadoJS(jsCode, env)
		if jsErr != nil {
			return "", jsErr
		}
		return fmt.Sprintf("%v", val), nil
	}

	// 3. Extraction pipeline: split cleaning regex '##'
	parts := strings.Split(mainRule, "##")
	base := strings.TrimSpace(parts[0])

	if strings.Contains(base, "||") {
		// Fallback candidate evaluation
		for _, r := range strings.Split(base, "||") {
			s, e := extract(v, strings.TrimSpace(r))
			if e != nil {
				return "", e
			}
			if s != "" {
				text = s
				break
			}
		}
	} else if strings.Contains(base, "&&") || strings.Contains(base, "%%") {
		// Concat or interleaving evaluation
		var out []string
		sep := "&&"
		if strings.Contains(base, "%%") {
			sep = "%%"
		}
		for _, r := range strings.Split(base, sep) {
			s, e := extract(v, strings.TrimSpace(r))
			if e != nil {
				return "", e
			}
			if s != "" {
				out = append(out, s)
			}
		}
		text = strings.Join(out, "\n")
	} else if strings.HasPrefix(strings.TrimSpace(base), "$") {
		// JSONPath extraction via GJSON
		text, err = extractJSONPath(v, strings.TrimSpace(base))
		if err != nil {
			return "", err
		}
	} else if isXPath(base) {
		// XPath extraction
		text, err = extractXPath(v, base)
		if err != nil {
			return "", err
		}
	} else if base == "" && len(parts) >= 2 {
		text = v.rawString()
	} else if base != "" {
		if v.node != nil {
			sel, attr, err := htmlSelect(v.node, strings.TrimSpace(base), true)
			if err != nil {
				if !strings.ContainsAny(base, "@[]></") {
					return base, nil
				}
				return "", err
			}
			var out []string
			sel.Each(func(_ int, s *goquery.Selection) {
				var t string
				switch attr {
				case "text", "textNodes", "html", "all":
					for _, n := range s.Nodes {
						t += plainText(n)
					}
				case "ownText":
					for _, n := range s.Nodes {
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							if c.Type == html.TextNode {
								t += c.Data
							}
						}
					}
				default:
					t, _ = s.Attr(attr)
				}
				if t = strings.TrimSpace(t); t != "" {
					out = append(out, t)
				}
			})
			text = strings.Join(out, "\n")
		} else if v.raw != "" && gjson.Valid(v.raw) {
			// Fallback: If JSON response but given dot-path without $ prefix (e.g. "data.content")
			res := gjson.Get(v.raw, base)
			if res.Exists() {
				text = res.String()
			}
		}
	}

	// 4. Regex replacement pipeline
	// 4. Regex replacement pipeline
	if len(parts) >= 2 {
		pattern := sanitizeRegex(parts[1])
		re, err := regexp.Compile(pattern)
		if err == nil {
			replacement := ""
			if len(parts) >= 3 {
				replacement = parts[2]
			}
			replaceFirst := len(parts) > 3 || base == "" || strings.HasSuffix(mainRule, "###")
			if strings.HasSuffix(replacement, "###") {
				replacement = strings.TrimSuffix(replacement, "###")
				replaceFirst = true
			}
			if replaceFirst {
				loc := re.FindStringIndex(text)
				if loc != nil {
					matched := text[loc[0]:loc[1]]
					text = re.ReplaceAllString(matched, replacement)
				} else {
					text = ""
				}
			} else {
				text = re.ReplaceAllString(text, replacement)
			}
		}
	}

	text = strings.TrimSpace(text)

	// 5. If rule had a post-processing JS block, pass text as 'result'
	if jsCode != "" {
		env := JSEnv{
			Result:  text,
			Src:     v.rawString(),
			BaseURL: "",
		}
		if v.env != nil {
			env = *v.env
			env.Result = text
		}
		val, jsErr := RunLegadoJS(jsCode, env)
		if jsErr == nil && val != nil {
			text = strings.TrimSpace(fmt.Sprintf("%v", val))
		}
	}

	return text, nil
}

func elements(v value, rule string) ([]value, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil, fmt.Errorf("missing list rule")
	}

	// Check for embedded JS in element rule
	mainRule, jsCode := SplitJSBlock(rule)
	mainRule = cleanRule(mainRule)

	for _, op := range []string{"||", "&&", "%%"} {
		if strings.Contains(mainRule, op) {
			var out []value
			for _, r := range strings.Split(mainRule, op) {
				vs, err := elements(v, strings.TrimSpace(r))
				if err != nil {
					return nil, err
				}
				out = append(out, vs...)
				if len(out) > 20000 {
					return nil, fmt.Errorf("list exceeds 20000 items")
				}
				if op == "||" && len(out) > 0 {
					break
				}
			}
			return out, nil
		}
	}

	var out []value
	if strings.HasPrefix(mainRule, "$") {
		// JSONPath list extraction via GJSON
		out = elementsJSONPath(v, mainRule)
	} else if isXPath(mainRule) {
		// XPath list extraction
		items, err := elementsXPath(v, mainRule)
		if err != nil {
			return nil, err
		}
		out = items
	} else {
		if v.node != nil {
			sel, _, err := htmlSelect(v.node, mainRule, false)
			if err != nil {
				return nil, err
			}
			sel.Each(func(_ int, s *goquery.Selection) {
				out = append(out, value{node: s, env: v.env})
			})
		} else if v.raw != "" && gjson.Valid(v.raw) {
			// Auto route to GJSON if input is json
			out = elementsJSONPath(v, mainRule)
		}
	}

	// If JS post-processing exists on elements level
	if jsCode != "" {
		// Execute JS on the raw result
		env := JSEnv{Result: out, Src: v.rawString()}
		if v.env != nil {
			env = *v.env
			env.Result = out
		}
		_, _ = RunLegadoJS(jsCode, env)
	}

	if len(out) > 20000 {
		return nil, fmt.Errorf("list exceeds 20000 items")
	}
	return out, nil
}

// XPath Helper Functions
func isXPath(rule string) bool {
	rule = strings.TrimSpace(rule)
	return strings.HasPrefix(rule, "//") || strings.HasPrefix(rule, "xpath:") || strings.HasPrefix(rule, "@xpath:")
}

func cleanXPath(rule string) string {
	rule = strings.TrimSpace(rule)
	rule = strings.TrimPrefix(rule, "@xpath:")
	rule = strings.TrimPrefix(rule, "xpath:")
	return strings.TrimSpace(rule)
}

func extractXPath(v value, rule string) (string, error) {
	rule = cleanXPath(rule)
	raw := v.rawString()
	if raw == "" {
		return "", nil
	}
	doc, err := htmlquery.Parse(strings.NewReader(raw))
	if err != nil {
		return "", err
	}

	nodes, err := htmlquery.QueryAll(doc, rule)
	if err != nil {
		return "", fmt.Errorf("xpath query error: %w", err)
	}

	var texts []string
	for _, n := range nodes {
		t := htmlquery.InnerText(n)
		t = strings.TrimSpace(t)
		if t != "" {
			texts = append(texts, t)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func elementsXPath(v value, rule string) ([]value, error) {
	rule = cleanXPath(rule)
	raw := v.rawString()
	if raw == "" {
		return nil, nil
	}
	doc, err := htmlquery.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	nodes, err := htmlquery.QueryAll(doc, rule)
	if err != nil {
		return nil, fmt.Errorf("xpath query error: %w", err)
	}
	var out []value
	for _, n := range nodes {
		htmlStr := htmlquery.OutputHTML(n, true)
		subDoc, _ := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
		if subDoc != nil {
			out = append(out, value{node: subDoc.Selection, raw: htmlStr, env: v.env})
		}
	}
	return out, nil
}

// GJSON (JSONPath) Helper Functions
func validateJSONPath(path string) error {
	if strings.Contains(path, "['']") || strings.Contains(path, `[""]`) {
		return fmt.Errorf("invalid jsonpath empty key: %s", path)
	}
	if strings.Contains(path, "','") || strings.Contains(path, `","`) {
		return fmt.Errorf("unsupported jsonpath union: %s", path)
	}
	if strings.Contains(path, "[-") {
		return fmt.Errorf("unsupported negative index: %s", path)
	}
	if strings.Contains(path, ":") && strings.Contains(path, "[") {
		return fmt.Errorf("unsupported jsonpath slice: %s", path)
	}
	return nil
}

func normalizeGJSONPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	// Strip trailing [*] because in gjson 'data.list' already accesses the array
	path = strings.TrimSuffix(path, "[*]")
	path = strings.ReplaceAll(path, "[*]", "")
	path = strings.ReplaceAll(path, "['", ".")
	path = strings.ReplaceAll(path, "']", "")
	path = strings.ReplaceAll(path, "[\"", ".")
	path = strings.ReplaceAll(path, "\"]", "")
	path = strings.ReplaceAll(path, "[", ".")
	path = strings.ReplaceAll(path, "]", "")
	path = strings.Trim(path, ".")
	return path
}
func extractJSONPath(v value, path string) (string, error) {
	if err := validateJSONPath(path); err != nil {
		return "", err
	}
	raw := v.rawString()
	if raw == "" {
		return "", nil
	}
	gpath := normalizeGJSONPath(path)
	res := gjson.Get(raw, gpath)
	if !res.Exists() {
		return "", nil
	}
	if res.IsArray() {
		var out []string
		res.ForEach(func(_, value gjson.Result) bool {
			s := strings.TrimSpace(value.String())
			if s != "" {
				out = append(out, s)
			}
			return true
		})
		return strings.Join(out, "\n"), nil
	}
	return strings.TrimSpace(res.String()), nil
}

func elementsJSONPath(v value, path string) []value {
	raw := v.rawString()
	if raw == "" {
		return nil
	}
	gpath := normalizeGJSONPath(path)
	res := gjson.Get(raw, gpath)
	var out []value
	if res.IsArray() {
		res.ForEach(func(_, item gjson.Result) bool {
			itemRaw := item.Raw
			var obj any
			_ = json.Unmarshal([]byte(itemRaw), &obj)
			out = append(out, value{data: obj, raw: itemRaw, env: v.env})
			return true
		})
	} else if res.Exists() {
		itemRaw := res.Raw
		var obj any
		_ = json.Unmarshal([]byte(itemRaw), &obj)
		out = append(out, value{data: obj, raw: itemRaw, env: v.env})
	}
	return out
}

var (
	legacyIndex         = regexp.MustCompile(`^(class|tag|id)\.([\w-]+)(?:[\.:](-?\d+))?$`)
	legacyExclude       = regexp.MustCompile(`^(class|tag|id)\.([\w-]+)!(-?\d+)$`)
	tagIndexPattern     = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9_-]*)\.(-?\d+)$`)
	bracketIndexPattern = regexp.MustCompile(`^(.+)\[(-?\d+)\]$`)
	putPattern          = regexp.MustCompile(`(?i)@put:\{[^}]*\}`)
	getPattern          = regexp.MustCompile(`(?i)@get:\{([^}]+)\}`)
)

func excludeIndex(s *goquery.Selection, idx int) *goquery.Selection {
	n := s.Length()
	if n == 0 {
		return s
	}
	if idx < 0 {
		idx = n + idx
	}
	if idx >= 0 && idx < n {
		return s.FilterFunction(func(i int, _ *goquery.Selection) bool {
			return i != idx
		})
	}
	return s
}

func pickIndex(s *goquery.Selection, idx int) *goquery.Selection {
	n := s.Length()
	if n == 0 {
		return s
	}
	if idx < 0 {
		idx = n + idx
	}
	if idx >= 0 && idx < n {
		return s.Eq(idx)
	}
	return s.Slice(0, 0)
}

func htmlSelect(root *goquery.Selection, rule string, scalar bool) (*goquery.Selection, string, error) {
	rule = cleanRule(rule)
	attr := "text"
	parts := strings.Split(rule, "@")
	if scalar && len(parts) > 1 {
		attr = strings.TrimSpace(parts[len(parts)-1])
		parts = parts[:len(parts)-1]
	} else if scalar && len(parts) == 1 {
		switch rule {
		case "text", "textNodes", "ownText", "html", "all", "href", "src", "content", "title":
			attr = rule
			parts = nil
		}
	}
	cur := root
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "text.") {
			keyword := strings.TrimPrefix(p, "text.")
			candidates := cur.Find("*")
			matched := candidates.FilterFunction(func(_ int, s *goquery.Selection) bool {
				var own string
				for _, n := range s.Nodes {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.TextNode {
							own += c.Data
						}
					}
				}
				return strings.Contains(own, keyword)
			})
			if matched.Length() == 0 {
				matched = candidates.FilterFunction(func(_ int, s *goquery.Selection) bool {
					return strings.Contains(s.Text(), keyword)
				})
			}
			if matched.Length() == 0 {
				matched = cur.FilterFunction(func(_ int, s *goquery.Selection) bool {
					return strings.Contains(s.Text(), keyword)
				})
			}
			cur = matched
		} else if m := legacyExclude.FindStringSubmatch(p); m != nil {
			selector := m[2]
			if m[1] == "class" {
				selector = "." + selector
			} else if m[1] == "id" {
				selector = "#" + selector
			}
			sub := cur.Find(selector)
			if sub.Length() == 0 && cur.Filter(selector).Length() > 0 {
				sub = cur.Filter(selector)
			}
			idx, _ := strconv.Atoi(m[3])
			cur = excludeIndex(sub, idx)
		} else if m := legacyIndex.FindStringSubmatch(p); m != nil {
			selector := m[2]
			if m[1] == "class" {
				selector = "." + selector
			} else if m[1] == "id" {
				selector = "#" + selector
			}
			sub := cur.Find(selector)
			if sub.Length() == 0 && cur.Filter(selector).Length() > 0 {
				sub = cur.Filter(selector)
			}
			if m[3] != "" {
				i, _ := strconv.Atoi(m[3])
				cur = pickIndex(sub, i)
			} else {
				cur = sub
			}
		} else if m := tagIndexPattern.FindStringSubmatch(p); m != nil {
			tagName := m[1]
			idx, _ := strconv.Atoi(m[2])
			sub := cur.Find(tagName)
			if sub.Length() == 0 && cur.Filter(tagName).Length() > 0 {
				sub = cur.Filter(tagName)
			}
			cur = pickIndex(sub, idx)
		} else if m := bracketIndexPattern.FindStringSubmatch(p); m != nil {
			baseSel := strings.TrimSpace(m[1])
			idx, _ := strconv.Atoi(m[2])
			matcher, err := cascadia.Compile(baseSel)
			if err != nil {
				return nil, "", fmt.Errorf("invalid bracket selector %q: %w", baseSel, err)
			}
			sub := cur.FindMatcher(matcher)
			if sub.Length() == 0 && cur.FilterMatcher(matcher).Length() > 0 {
				sub = cur.FilterMatcher(matcher)
			}
			cur = pickIndex(sub, idx)
		} else if p == "children" {
			cur = cur.Children()
		} else {
			if strings.HasPrefix(p, "class.") || strings.HasPrefix(p, "tag.") || strings.HasPrefix(p, "id.") {
				return nil, "", fmt.Errorf("unsupported legacy selector %q", p)
			}
			matcher, err := cascadia.Compile(p)
			if err != nil {
				return nil, "", fmt.Errorf("invalid/unsupported CSS selector: %w", err)
			}
			sub := cur.FindMatcher(matcher)
			if sub.Length() == 0 && cur.FilterMatcher(matcher).Length() > 0 {
				sub = cur.FilterMatcher(matcher)
			} else {
				cur = sub
			}
		}
	}
	if attr == "" {
		return nil, "", fmt.Errorf("empty extraction attribute")
	}
	return cur, attr, nil
}

func plainText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "iframe", "object", "svg", "template", "head":
				return
			case "p", "div", "li", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "blockquote", "dd", "dl":
				b.WriteByte('\n')
			case "br", "hr":
				b.WriteByte('\n')
				return
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "li", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "blockquote", "dd", "dl":
				b.WriteByte('\n')
			}
		}
	}
	walk(n)
	var lines []string
	for _, line := range strings.Split(b.String(), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func sanitizeRegex(pattern string) string {
	runes := []rune(pattern)
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			next := runes[i+1]
			if next > 127 {
				b.WriteRune(next)
				i++
				continue
			}
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

func cleanContent(text, replaceRegex string) (string, error) {
	if strings.TrimSpace(replaceRegex) == "" {
		return PurifyContent(text), nil
	}
	lines := strings.Split(replaceRegex, "\n")
	if len(lines) > 100 {
		return "", fmt.Errorf("replaceRegex exceeds 100 rules")
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if len(line) > 2048 {
			return "", fmt.Errorf("replaceRegex rule exceeds 2048 bytes")
		}
		if err := supported(line); err != nil {
			return "", err
		}
		if strings.HasPrefix(line, "##") {
			line = strings.TrimPrefix(line, "##")
		}
		parts := strings.Split(line, "##")
		if len(parts) == 0 {
			continue
		}
		pattern := sanitizeRegex(parts[0])
		if pattern == "" {
			continue
		}
		replacement := ""
		if len(parts) >= 2 {
			replacement = parts[1]
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if strings.HasSuffix(replacement, "###") {
			replacement = strings.TrimSuffix(replacement, "###")
		}
		if len(parts) > 2 || strings.HasSuffix(line, "###") {
			loc := re.FindStringIndex(text)
			if loc != nil {
				text = text[:loc[0]] + re.ReplaceAllString(text[loc[0]:loc[1]], replacement) + text[loc[1]:]
			}
		} else {
			text = re.ReplaceAllString(text, replacement)
		}
	}
	text = PurifyContent(text)
	return strings.TrimSpace(text), nil
}

func normalizeContentParagraphs(text string) string {
	if text == "" {
		return ""
	}
	rawLines := strings.Split(text, "\n")
	var cleanedLines []string
	for _, l := range rawLines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			cleanedLines = append(cleanedLines, trimmed)
		}
	}
	if len(cleanedLines) == 0 {
		return ""
	}
	var merged []string
	isIsolatedPunctuation := func(s string) bool {
		if s == "" {
			return false
		}
		runes := []rune(s)
		if len(runes) > 3 {
			return false
		}
		for _, r := range runes {
			switch r {
			case '”', '’', '」', '』', '"', '\'', '！', '!', '？', '?', '。', '；', ';', '：', ':', '…', '—':
			default:
				return false
			}
		}
		return true
	}
	for _, line := range cleanedLines {
		if len(merged) > 0 && isIsolatedPunctuation(line) {
			merged[len(merged)-1] = merged[len(merged)-1] + line
			continue
		}
		merged = append(merged, line)
	}
	return strings.Join(merged, "\n\n")
}
