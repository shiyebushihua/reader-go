package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func upload(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "upload")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestTXTEncodings(t *testing.T) {
	text := "前言\r\n说明\r\n第一章 开始\r\n你好世界\r\n第二章 结束\r\n再见"
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...), gbk} {
		b, err := Parse(context.Background(), upload(t, data), "中文.txt")
		if err != nil || b.Title != "中文" || b.Kind != "txt" || len(b.Chapters) != 3 || !strings.Contains(b.Chapters[1].Content, "你好世界") {
			t.Fatalf("%+v %v", b, err)
		}
	}
	b, err := Parse(context.Background(), upload(t, []byte("Chapter 1 Beginning\nhello\nChapter II End\nbye")), "English.TXT")
	if err != nil || len(b.Chapters) != 2 {
		t.Fatal(b, err)
	}
}
func TestTXTBoundsAndCancellation(t *testing.T) {
	b, err := Parse(context.Background(), upload(t, []byte(strings.Repeat("中", maxChapter/3+100))), "large.txt")
	if err != nil || len(b.Chapters) != 2 {
		t.Fatal(len(b.Chapters), err)
	}
	for _, c := range b.Chapters {
		if len(c.Content) > maxChapter || !utf8.ValidString(c.Content) {
			t.Fatal("invalid bounded chapter")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Parse(ctx, "unused", "x.txt"); err == nil {
		t.Fatal("ignored cancellation")
	}
	for _, data := range [][]byte{[]byte("  \n"), {0, 1, 2}, {0xff}} {
		if _, err = Parse(context.Background(), upload(t, data), "x.txt"); err == nil {
			t.Fatal("invalid TXT accepted")
		}
	}
	p := filepath.Join(t.TempDir(), "large")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxUpload + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err = Parse(context.Background(), p, "x.epub"); err == nil {
		t.Fatal("oversized upload accepted")
	}
}
func zipUpload(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "upload.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, text := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
func epubFixture() map[string]string {
	return map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/book.opf"/></rootfiles></container>`,
		"OEBPS/book.opf":         `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Fixture Book</dc:title><dc:creator>Writer</dc:creator></metadata><manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/><item id="b" href="b.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="b"/><itemref idref="a"/></spine></package>`,
		"OEBPS/nav.xhtml":        `<html><body><nav><a href="a.xhtml#first">First label</a><a href="b.xhtml">Second label</a></nav></body></html>`,
		"OEBPS/a.xhtml":          `<html><head><title>First</title></head><body><h1>A</h1><p>Safe &amp; sound</p><script>alert(1)</script><style>bad</style><p hidden="hidden">secret</p><img src="https://invalid.example/tracker"/></body></html>`,
		"OEBPS/b.xhtml":          `<html><body><h1>B</h1><p>Paragraph<br/>next</p></body></html>`,
	}
}
func TestEPUBSpineNavAndSafeText(t *testing.T) {
	b, err := Parse(context.Background(), zipUpload(t, epubFixture()), "fallback.epub")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Fixture Book" || b.Author != "Writer" || b.Kind != "epub" || len(b.Chapters) != 2 {
		t.Fatalf("%+v", b)
	}
	if b.Chapters[0].Title != "Second label" || b.Chapters[1].Title != "First label" || b.Chapters[0].Content != "B\nParagraph\nnext" {
		t.Fatalf("%+v", b.Chapters)
	}
	if strings.Contains(b.Chapters[1].Content, "alert") || strings.Contains(b.Chapters[1].Content, "secret") || strings.Contains(b.Chapters[1].Content, "<") || !strings.Contains(b.Chapters[1].Content, "Safe & sound") {
		t.Fatal(b.Chapters[1])
	}
}
func TestEPUBNCX(t *testing.T) {
	files := epubFixture()
	files["OEBPS/book.opf"] = strings.ReplaceAll(files["OEBPS/book.opf"], `properties="nav"`, "")
	files["OEBPS/book.opf"] = strings.Replace(files["OEBPS/book.opf"], "</manifest>", `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>`, 1)
	files["OEBPS/book.opf"] = strings.Replace(files["OEBPS/book.opf"], "<spine>", `<spine toc="ncx">`, 1)
	files["OEBPS/toc.ncx"] = `<ncx><navMap><navPoint><navLabel><text>NCX label</text></navLabel><content src="b.xhtml"/></navPoint></navMap></ncx>`
	b, err := Parse(context.Background(), zipUpload(t, files), "x.epub")
	if err != nil || b.Chapters[0].Title != "NCX label" {
		t.Fatal(b, err)
	}
}
func TestEPUBRejectsUnsafeArchives(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "OEBPS/../../escape", "C:/file", "OEBPS\\evil"} {
		files := epubFixture()
		files[name] = "bad"
		if _, err := Parse(context.Background(), zipUpload(t, files), "x.epub"); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	files := epubFixture()
	files["bomb"] = strings.Repeat("x", 2<<20)
	if _, err := Parse(context.Background(), zipUpload(t, files), "x.epub"); err == nil {
		t.Fatal("compression bomb accepted")
	}
	files = epubFixture()
	files["OEBPS/book.opf"] = strings.Replace(files["OEBPS/book.opf"], `href="b.xhtml"`, `href="../../escape"`, 1)
	if _, err := Parse(context.Background(), zipUpload(t, files), "x.epub"); err == nil {
		t.Fatal("escaping reference accepted")
	}
	files = epubFixture()
	files["META-INF/encryption.xml"] = "<encryption/>"
	if _, err := Parse(context.Background(), zipUpload(t, files), "x.epub"); err == nil {
		t.Fatal("encrypted EPUB accepted")
	}
}

func TestArchiveReferences(t *testing.T) {
	for _, ref := range []string{"../../escape", "%2e%2e/%2e%2e/escape", "https://example.com/x", "//example.com/x", "/absolute", `a%5cb`, "a%00b", "a?query=1", "C:/x"} {
		if _, err := archiveRef("OEBPS/book.opf", ref); err == nil {
			t.Errorf("accepted %q", ref)
		}
	}
	for ref, want := range map[string]string{"../Text/a.xhtml#first": "Text/a.xhtml", "a%20b.xhtml": "OEBPS/a b.xhtml"} {
		got, err := archiveRef("OEBPS/book.opf", ref)
		if err != nil || got != want {
			t.Fatal(ref, got, err)
		}
	}
}

func TestZIPMetadataLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []zip.FileHeader
		want    string
	}{
		{"duplicate", []zip.FileHeader{{Name: "same"}, {Name: "same"}}, "duplicate"},
		{"encrypted", []zip.FileHeader{{Name: "secret", Flags: 1}}, "encrypted"},
		{"entry-size", []zip.FileHeader{{Name: "large", UncompressedSize64: maxArchiveTotal + 1}}, "size limit"},
		{"total-size", []zip.FileHeader{{Name: "one", UncompressedSize64: 70 << 20, CompressedSize64: 70 << 20}, {Name: "two", UncompressedSize64: 70 << 20, CompressedSize64: 70 << 20}}, "128 MiB"},
		{"ratio", []zip.FileHeader{{Name: "bomb", UncompressedSize64: 2 << 20, CompressedSize64: 1}}, "bomb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			z := zip.NewWriter(&buf)
			for _, h := range tc.headers {
				if _, err := z.CreateRaw(&h); err != nil {
					t.Fatal(err)
				}
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(context.Background(), upload(t, buf.Bytes()), "x.epub"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	h := zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0777)
	w, err := z.CreateHeader(&h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("../outside")); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Parse(context.Background(), upload(t, buf.Bytes()), "x.epub"); err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatal(err)
	}
}

func TestEPUBMissingAndUnsupportedContent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"missing-container", func(f map[string]string) { delete(f, "META-INF/container.xml") }, "missing"},
		{"missing-chapter", func(f map[string]string) { delete(f, "OEBPS/b.xhtml") }, "missing"},
		{"missing-id", func(f map[string]string) {
			f["OEBPS/book.opf"] = strings.Replace(f["OEBPS/book.opf"], `idref="b"`, `idref="unknown"`, 1)
		}, "missing manifest"},
		{"media-type", func(f map[string]string) {
			f["OEBPS/book.opf"] = strings.ReplaceAll(f["OEBPS/book.opf"], "application/xhtml+xml", "image/svg+xml")
		}, "unsupported spine"},
		{"duplicate-id", func(f map[string]string) {
			f["OEBPS/book.opf"] = strings.Replace(f["OEBPS/book.opf"], `id="b"`, `id="a"`, 1)
		}, "duplicate manifest"},
		{"external-chapter", func(f map[string]string) {
			f["OEBPS/book.opf"] = strings.Replace(f["OEBPS/book.opf"], `href="b.xhtml"`, `href="https://example.com/chapter"`, 1)
		}, "unsafe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := epubFixture()
			tc.mutate(files)
			if _, err := Parse(context.Background(), zipUpload(t, files), "x.epub"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundedArchiveReads(t *testing.T) {
	if _, err := readBounded(context.Background(), strings.NewReader("12345"), 4); err == nil {
		t.Fatal("read limit ignored")
	}
	data, err := readBounded(context.Background(), strings.NewReader("1234"), 4)
	if err != nil || string(data) != "1234" {
		t.Fatal(string(data), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readBounded(ctx, strings.NewReader("x"), 4); err == nil {
		t.Fatal("read cancellation ignored")
	}
	path := zipUpload(t, map[string]string{"entry": "1234"})
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	a := archive{ctx: context.Background(), files: map[string]*zip.File{"entry": z.File[0]}}
	if _, err := a.get("entry", 3); err == nil {
		t.Fatal("entry limit ignored")
	}
	a.read = maxArchiveTotal - 3
	if _, err := a.get("entry", 4); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal(err)
	}
}

func TestParseRejectsUnknownAndNonRegular(t *testing.T) {
	if _, err := Parse(context.Background(), upload(t, []byte("text")), "x.pdf"); err == nil {
		t.Fatal("unknown format accepted")
	}
	if _, err := Parse(context.Background(), t.TempDir(), "x.txt"); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := Parse(context.Background(), upload(t, []byte("not zip")), "x.epub"); err == nil {
		t.Fatal("invalid ZIP accepted")
	}
}
