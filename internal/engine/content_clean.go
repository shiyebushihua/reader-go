package engine
import (
 stdhtml "html"
 "regexp"
 "strings"
 "golang.org/x/net/html"
)
var contentHTMLTag = regexp.MustCompile(`(?i)</?[a-z][a-z0-9]*(?:\s[^<>]*|\s*/?)>|<!--`)
// PurifyContent preserves paragraphs while removing markup from text-book content.
func PurifyContent(text string) string {
 text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
 for i := 0; i < 3; i++ {
  decoded := stdhtml.UnescapeString(text)
  if decoded == text { break }
  text = decoded
 }
 if contentHTMLTag.MatchString(text) {
  if doc, err := html.Parse(strings.NewReader(text)); err == nil { text = plainText(doc) }
 }
 text = strings.NewReplacer("\u00a0", " ", "\u200b", "", "\ufeff", "").Replace(text)
 return normalizeContentParagraphs(text)
}
