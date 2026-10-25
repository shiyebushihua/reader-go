package server

import (
	"context"
	"encoding/json"
	"os"
	"readergo/internal/engine"
	"strings"
	"testing"
	"time"
)

// Opt-in smoke test: reads only the explicitly supplied source file, never copies it into repository.
func TestSourceLiveSmoke(t *testing.T) {
	path := os.Getenv("READER_SOURCE_SMOKE_FILE")
	if path == "" {
		t.Skip("explicit source file required")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var items []json.RawMessage
	if e = json.Unmarshal(data, &items); e != nil {
		t.Fatal(e)
	}
	eng := engine.New()
	tried, passed := 0, 0
	for i, raw := range items {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		skip := false
		for _, k := range []string{"header", "loginUrl", "loginUi", "loginCheckJs", "jsLib"} {
			v := obj[k]
			if v != nil && v != "" {
				skip = true
			}
		}
		rules := string(raw)
		for _, marker := range []string{"@js:", "<js>", "java.", "@XPath:", "token=", "password=", "Cookie"} {
			if strings.Contains(rules, marker) {
				skip = true
			}
		}
		if skip {
			continue
		}
		tried++
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		results, e := eng.Search(ctx, engine.Source{ID: "smoke", Rule: raw}, "三国")
		cancel()
		if e != nil {
			reason := e.Error()
			if len(reason) > 160 {
				reason = reason[:160]
			}
			if strings.Contains(reason, "http") {
				reason = "network/request failure (URL redacted)"
			}
			t.Logf("source index %d: FAIL %s", i, reason)
		} else {
			t.Logf("source index %d: search returned %d", i, len(results))
			if len(results) > 0 {
				passed++
			}
		}
		if tried >= 5 {
			break
		}
	}
	t.Logf("smoke summary: total=%d attempted=%d nonempty=%d (not a compatibility certification)", len(items), tried, passed)
}
