package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestPostSearchRuleCompatibility(t *testing.T) {
	ruleJSON := `{
		"bookSourceUrl": "https://m.41nr.com",
		"searchUrl": "/s.php,{method:\"post\",body:\"type=articlename&s={{key}}&submit=\"}",
		"ruleSearch": {
			"bookList": ".line",
			"name": "a@text",
			"author": "p@textNodes##\\[.*\\]",
			"bookUrl": "a@href"
		}
	}`
	s := Source{
		ID:   "bd61c083685152dd838ea70f43530267",
		Rule: json.RawMessage(ruleJSON),
	}
	r, err := parseSource(s)
	if err != nil {
		t.Fatalf("parseSource failed: %v", err)
	}
	spec, err := makeRequest(r, r.BookSourceURL, r.SearchURL, "都市偷心龙爪手", 1)
	if err != nil {
		t.Fatalf("makeRequest failed: %v", err)
	}
	if spec.Method != "POST" {
		t.Errorf("expected Method POST, got %s", spec.Method)
	}
	if spec.Body != "type=articlename&s=%E9%83%BD%E5%B8%82%E5%81%B7%E5%BF%83%E9%BE%99%E7%88%AA%E6%89%8B&submit=" {
		t.Errorf("unexpected Body: %s", spec.Body)
	}
	fmt.Printf("POST rule test passed! Spec: %+v\n", spec)
}

func TestLiveSearch41nr(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}
	ruleJSON := `{
		"bookSourceUrl": "https://m.41nr.com",
		"header": "{\"User-Agent\": \"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36\", \"Referer\": \"https://m.41nr.com/\"}",
		"searchUrl": "/s.php,{method:\"post\",body:\"type=articlename&s={{key}}&submit=\"}",
		"ruleSearch": {
			"bookList": ".line",
			"name": "a@text",
			"author": "p@textNodes##\\[.*\\]",
			"bookUrl": "a@href"
		}
	}`
	s := Source{
		ID:   "bd61c083685152dd838ea70f43530267",
		Rule: json.RawMessage(ruleJSON),
	}
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := e.Search(ctx, s, "都市偷心龙爪手")
	if err != nil {
		t.Fatalf("Live search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("Expected results, got 0")
	}
	found := false
	for _, item := range results {
		fmt.Printf("Found book: title=%q author=%q url=%q\n", item.Title, item.Author, item.URL)
		if item.Title == "都市偷心龙爪手" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Target book 都市偷心龙爪手 not found in results: %+v", results)
	}
}
