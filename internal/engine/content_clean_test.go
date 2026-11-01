package engine

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "strings"
 "testing"
)

func TestPurifyContent(t *testing.T) {
 cases := []struct{name, input, want string}{
  {"paragraphs", `<div>第一段<br>第二行</div><p>第三段<b>加粗</b></p>`, "第一段\n\n第二行\n\n第三段加粗"},
  {"escaped", `&lt;p&gt;甲&amp;nbsp;乙&lt;/p&gt;&lt;p&gt;丙&lt;/p&gt;`, "甲 乙\n\n丙"},
  {"double escaped", `&amp;lt;div&amp;gt;正文&amp;lt;/div&amp;gt;`, "正文"},
  {"non content", `<div>正文<script>alert(1)</script><style>.ad{}</style><!--广告--><iframe>广告</iframe><template>模板</template>结尾</div>`, "正文结尾"},
  {"plain", "比较 1 < 2，3 > 2。\r\n第二段 &amp; 正文", "比较 1 < 2，3 > 2。\n\n第二段 & 正文"},
  {"inline", `前文<div>段落</div>后文`, "前文\n\n段落\n\n后文"},
  {"empty", "", ""},
 }
 for _, tc := range cases { t.Run(tc.name, func(t *testing.T) {
  got := PurifyContent(tc.input)
  if got != tc.want { t.Fatalf("got %q want %q",got,tc.want) }
  if again := PurifyContent(got); again != got { t.Fatalf("not idempotent: %q",again) }
  got, err := cleanContent(tc.input, "")
  if err != nil || got != tc.want { t.Fatalf("default cleanup: %q %v",got,err) }
 }) }
 got, err := cleanContent(`<p>正文广告</p>`, "广告##")
 if err != nil || got != "正文" { t.Fatalf("replace plus cleanup: %q %v",got,err) }
}

func TestPaginationFailureDoesNotReturnPartialSuccess(t *testing.T) {
 e := fixtureEngine(t, map[string]string{})
 e.client.Transport = roundTripFunc(func(r *http.Request)(*http.Response,error){
  if r.URL.Path != "/one" { return nil, errors.New("page unavailable") }
  return &http.Response{StatusCode:200,Header:http.Header{"Content-Type":[]string{"text/html; charset=utf-8"}},Body:io.NopCloser(strings.NewReader(`<p>第一页</p><a href="/two">下一页</a>`)),Request:r},nil
 })
 s:=Source{Rule:json.RawMessage(`{"bookSourceUrl":"https://books.example","ruleContent":{"content":"p@text","nextContentUrl":"a@href"}}`)}
 text,err:=e.Content(context.Background(),s,"https://books.example/one")
 if err==nil || text!="" {t.Fatalf("partial success: %q %v",text,err)}
}
