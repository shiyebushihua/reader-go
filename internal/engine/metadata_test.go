package engine

import (
	"encoding/json"
	"testing"
)

func TestDisplayMetadataDoesNotBlockReading(t *testing.T) {
	raw := json.RawMessage(`{"bookSourceUrl":"https://example.com","searchUrl":"/search?q={{key}}","ruleSearch":{"bookList":".book","name":"a@text","kind":".kind@text","lastChapter":"@js:ignoredMetadata()","wordCount":".words@text"},"ruleBookInfo":{"kind":".category@text"},"ruleContent":{"content":".content@text","imageStyle":"FULL"}}`)
	if _, e := parseSource(Source{Rule: raw}); e != nil {
		t.Fatal(e)
	}
	raw = json.RawMessage(`{"bookSourceUrl":"https://example.com","ruleContent":{"content":"@js:execute()"}}`)
	if _, e := parseSource(Source{Rule: raw}); e == nil {
		t.Fatal("executable content must remain unsupported")
	}
}
