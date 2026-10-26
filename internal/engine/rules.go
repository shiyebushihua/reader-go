package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

type value struct {
	node *goquery.Selection
	data any
}

func document(body []byte) (value, error) {
	text := strings.TrimPrefix(strings.TrimSpace(string(body)), "\ufeff")
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		if !json.Valid([]byte(text)) {
			return value{}, fmt.Errorf("invalid JSON document")
		}
		var v any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return value{}, fmt.Errorf("invalid JSON document: %w", err)
		}
		return value{data: v}, nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(text))
	if err != nil {
		return value{}, err
	}
	return value{node: doc.Selection}, nil
}
func supported(rule string) error {
	low := strings.ToLower(rule)
	for _, token := range []string{"@js:", "<js>", "javascript:", "@xpath:", "xpath:", "@get:", "@put:", "{{", "}}", "%%"} {
		if strings.Contains(low, token) {
			return fmt.Errorf("unsupported rule construct %q", token)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(rule), "//") {
		return fmt.Errorf("unsupported XPath rule")
	}
	return nil
}
func extract(v value, rule string) (string, error) {
	if strings.TrimSpace(rule) == "" {
		return "", nil
	}
	if err := supported(rule); err != nil {
		return "", err
	}
	// Cleaning applies after the complete fallback/concatenation expression.
	parts := strings.Split(rule, "##")
	if len(parts) > 3 {
		return "", fmt.Errorf("unsupported regex cleaning flags")
	}
	base := parts[0]
	var text string
	if strings.Contains(base, "||") {
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
	} else if strings.Contains(base, "&&") {
		var out []string
		for _, r := range strings.Split(base, "&&") {
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
		if v.node != nil {
			return "", fmt.Errorf("JSONPath rule applied to HTML")
		}
		vals, err := jsonPath(v.data, strings.TrimSpace(base))
		if err != nil {
			return "", err
		}
		var out []string
		for _, x := range vals {
			switch n := x.(type) {
			case string:
				out = append(out, n)
			case json.Number:
				out = append(out, n.String())
			case bool:
				out = append(out, strconv.FormatBool(n))
			case nil:
			default:
				return "", fmt.Errorf("JSONPath scalar rule selected an object or array")
			}
		}
		text = strings.Join(out, "\n")
	} else {
		if v.node == nil {
			return "", fmt.Errorf("HTML rule applied to JSON")
		}
		sel, attr, err := htmlSelect(v.node, strings.TrimSpace(base), true)
		if err != nil {
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
	}
	if len(parts) >= 2 {
		re, err := regexp.Compile(sanitizeRegex(parts[1]))
		if err != nil {
			return "", fmt.Errorf("unsupported/invalid cleaning regex: %w", err)
		}
		replacement := ""
		if len(parts) == 3 {
			replacement = parts[2]
		}
		text = re.ReplaceAllString(text, replacement)
	}
	return strings.TrimSpace(text), nil
}
func elements(v value, rule string) ([]value, error) {
	if rule == "" {
		return nil, fmt.Errorf("missing list rule")
	}
	if err := supported(rule); err != nil {
		return nil, err
	}
	for _, op := range []string{"||", "&&"} {
		if strings.Contains(rule, op) {
			var out []value
			for _, r := range strings.Split(rule, op) {
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
	if strings.HasPrefix(rule, "$") {
		if v.node != nil {
			return nil, fmt.Errorf("JSONPath list applied to HTML")
		}
		vals, err := jsonPath(v.data, rule)
		if err != nil {
			return nil, err
		}
		for _, x := range vals {
			if arr, ok := x.([]any); ok {
				for _, a := range arr {
					out = append(out, value{data: a})
				}
			} else {
				out = append(out, value{data: x})
			}
		}
	} else {
		if v.node == nil {
			return nil, fmt.Errorf("HTML list applied to JSON")
		}
		sel, _, err := htmlSelect(v.node, rule, false)
		if err != nil {
			return nil, err
		}
		sel.Each(func(_ int, s *goquery.Selection) { out = append(out, value{node: s}) })
	}
	if len(out) > 20000 {
		return nil, fmt.Errorf("list exceeds 20000 items")
	}
	return out, nil
}

var legacyIndex = regexp.MustCompile(`^(class|tag|id)\.([\w-]+)(?:\.(-?\d+))?$`)

func htmlSelect(root *goquery.Selection, rule string, scalar bool) (*goquery.Selection, string, error) {
	rule = strings.TrimPrefix(rule, "@css:")
	rule = strings.TrimPrefix(rule, "css:")
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
		if m := legacyIndex.FindStringSubmatch(p); m != nil {
			selector := m[2]
			if m[1] == "class" {
				selector = "." + selector
			}
			if m[1] == "id" {
				selector = "#" + selector
			}
			cur = cur.Find(selector)
			if m[3] != "" {
				i, _ := strconv.Atoi(m[3])
				cur = cur.Eq(i)
			}
		} else {
			if strings.HasPrefix(p, "class.") || strings.HasPrefix(p, "tag.") || strings.HasPrefix(p, "id.") {
				return nil, "", fmt.Errorf("unsupported legacy selector %q", p)
			}
			matcher, err := cascadia.Compile(p)
			if err != nil {
				return nil, "", fmt.Errorf("invalid/unsupported CSS selector: %w", err)
			}
			cur = cur.FindMatcher(matcher)
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
			case "script", "style", "noscript", "iframe", "object", "svg":
				return
			case "br":
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
			case "p", "div", "li", "section", "article", "h1", "h2", "h3", "tr", "blockquote":
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
func jsonPath(root any, path string) ([]any, error) {
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("JSONPath must start with $")
	}
	values := []any{root}
	rest := path[1:]
	for rest != "" {
		var key string
		index := -1
		wild := false
		if rest[0] == '.' {
			rest = rest[1:]
			i := 0
			for i < len(rest) && (rest[i] == '_' || rest[i] == '-' || rest[i] >= 'a' && rest[i] <= 'z' || rest[i] >= 'A' && rest[i] <= 'Z' || rest[i] >= '0' && rest[i] <= '9') {
				i++
			}
			if i == 0 {
				return nil, fmt.Errorf("unsupported JSONPath %q", path)
			}
			key = rest[:i]
			rest = rest[i:]
		} else if rest[0] == '[' {
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid JSONPath")
			}
			token := strings.TrimSpace(rest[1:end])
			rest = rest[end+1:]
			if token == "*" {
				wild = true
			} else if len(token) >= 2 && (token[0] == '\'' && token[len(token)-1] == '\'' || token[0] == '"' && token[len(token)-1] == '"') {
				key = token[1 : len(token)-1]
				if key == "" || strings.ContainsAny(key, "\\\"'") {
					return nil, fmt.Errorf("unsupported JSONPath quoted key %q", token)
				}
			} else {
				var err error
				index, err = strconv.Atoi(token)
				if err != nil || index < 0 {
					return nil, fmt.Errorf("unsupported JSONPath subscript %q", token)
				}
			}
		} else {
			return nil, fmt.Errorf("unsupported JSONPath %q", path)
		}
		var next []any
		for _, v := range values {
			if key != "" {
				if obj, ok := v.(map[string]any); ok {
					if x, exists := obj[key]; exists {
						next = append(next, x)
					}
				}
			} else if arr, ok := v.([]any); ok {
				if wild {
					next = append(next, arr...)
				} else if index >= 0 && index < len(arr) {
					next = append(next, arr[index])
				}
			}
		}
		values = next
		if len(values) > 20000 {
			return nil, fmt.Errorf("JSONPath exceeds item limit")
		}
	}
	return values, nil
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
		return text, nil
	}
	lines := strings.Split(replaceRegex, "\n")
	if len(lines) > 100 {
		return "", fmt.Errorf("replaceRegex exceeds 100 rules")
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
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
		if len(parts) > 2 {
			continue
		}
		pattern := sanitizeRegex(parts[0])
		replacement := ""
		if len(parts) == 2 {
			replacement = parts[1]
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		text = re.ReplaceAllString(text, replacement)
	}
	return strings.TrimSpace(text), nil
}
