package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

const maxArchiveTotal = 128 << 20
const maxEntry = 8 << 20

type archive struct {
	ctx   context.Context
	files map[string]*zip.File
	read  int64
}

func safeName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return path.Clean(name) == strings.TrimSuffix(name, "/")
}
func archiveRef(base, ref string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\\x00:") {
		return "", fmt.Errorf("unsafe EPUB reference %q", ref)
	}
	name := path.Clean(path.Join(path.Dir(base), u.Path))
	if !safeName(name) {
		return "", fmt.Errorf("EPUB reference escapes archive: %q", ref)
	}
	return name, nil
}
func (a *archive) get(name string, limit int64) ([]byte, error) {
	if err := a.ctx.Err(); err != nil {
		return nil, err
	}
	f := a.files[name]
	if f == nil {
		return nil, fmt.Errorf("EPUB missing %s", name)
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("EPUB entry %s exceeds size limit", name)
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := readBounded(a.ctx, r, limit)
	if err != nil {
		return nil, err
	}
	a.read += int64(len(data))
	if a.read > maxArchiveTotal {
		return nil, fmt.Errorf("EPUB decompression budget exceeded")
	}
	return data, nil
}

type containerXML struct {
	Files []struct {
		Path string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}
type manifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	Type       string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}
type packageXML struct {
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators []string `xml:"creator"`
	} `xml:"metadata"`
	Manifest []manifestItem `xml:"manifest>item"`
	Spine    struct {
		TOC   string `xml:"toc,attr"`
		Items []struct {
			ID     string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func parseEPUB(ctx context.Context, r io.ReaderAt, size int64, fallback string) (Book, error) {
	book := Book{Title: fallback, Kind: "epub", Chapters: []Chapter{}}
	z, err := zip.NewReader(r, size)
	if err != nil {
		return Book{}, fmt.Errorf("invalid EPUB ZIP: %w", err)
	}
	if len(z.File) > 10000 {
		return Book{}, fmt.Errorf("EPUB exceeds 10000 archive entries")
	}
	a := archive{ctx: ctx, files: map[string]*zip.File{}}
	var total uint64
	for _, f := range z.File {
		if err = ctx.Err(); err != nil {
			return Book{}, err
		}
		if !safeName(f.Name) || f.Mode()&0170000 != 0 && !f.FileInfo().IsDir() && !f.Mode().IsRegular() {
			return Book{}, fmt.Errorf("unsafe ZIP entry %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return Book{}, fmt.Errorf("non-regular ZIP entry")
		}
		if _, ok := a.files[f.Name]; ok {
			return Book{}, fmt.Errorf("duplicate ZIP entry")
		}
		if f.Flags&1 != 0 {
			return Book{}, fmt.Errorf("encrypted ZIP entries unsupported")
		}
		// Bound even unreferenced assets; reject extreme ratios without integer rounding.
		if f.UncompressedSize64 > maxArchiveTotal || f.UncompressedSize64 > 1<<20 && (f.CompressedSize64 == 0 || f.CompressedSize64 < (f.UncompressedSize64+199)/200) {
			return Book{}, fmt.Errorf("ZIP bomb/entry size limit")
		}
		total += f.UncompressedSize64
		if total > maxArchiveTotal {
			return Book{}, fmt.Errorf("EPUB exceeds 128 MiB uncompressed")
		}
		a.files[f.Name] = f
	}
	if _, ok := a.files["META-INF/encryption.xml"]; ok {
		return Book{}, fmt.Errorf("encrypted/obfuscated EPUB is unsupported")
	}
	data, err := a.get("META-INF/container.xml", 1<<20)
	if err != nil {
		return Book{}, err
	}
	var container containerXML
	if err = xml.Unmarshal(data, &container); err != nil {
		return Book{}, err
	}
	if len(container.Files) == 0 {
		return Book{}, fmt.Errorf("EPUB container has no rootfile")
	}
	opf := container.Files[0].Path
	if !safeName(opf) {
		return Book{}, fmt.Errorf("unsafe OPF path")
	}
	data, err = a.get(opf, 2<<20)
	if err != nil {
		return Book{}, err
	}
	var pkg packageXML
	if err = xml.Unmarshal(data, &pkg); err != nil {
		return Book{}, fmt.Errorf("invalid OPF: %w", err)
	}
	if len(pkg.Metadata.Titles) > 0 && strings.TrimSpace(pkg.Metadata.Titles[0]) != "" {
		book.Title = strings.TrimSpace(pkg.Metadata.Titles[0])
	}
	book.Author = strings.Join(pkg.Metadata.Creators, ", ")
	items := map[string]manifestItem{}
	for _, item := range pkg.Manifest {
		if item.ID == "" {
			return Book{}, fmt.Errorf("manifest item has no id")
		}
		if _, ok := items[item.ID]; ok {
			return Book{}, fmt.Errorf("duplicate manifest id")
		}
		items[item.ID] = item
	}
	labels := map[string]string{}
	for _, item := range pkg.Manifest {
		if !strings.Contains(" "+item.Properties+" ", " nav ") {
			continue
		}
		name, er := archiveRef(opf, item.Href)
		if er != nil {
			return Book{}, er
		}
		data, er = a.get(name, maxEntry)
		if er != nil {
			return Book{}, er
		}
		doc, er := goquery.NewDocumentFromReader(bytes.NewReader(data))
		if er != nil {
			return Book{}, er
		}
		doc.Find("nav a[href]").Each(func(_ int, s *goquery.Selection) {
			href, _ := s.Attr("href")
			ref, e := archiveRef(name, href)
			if e == nil && labels[ref] == "" {
				labels[ref] = strings.TrimSpace(s.Text())
			}
		})
	}
	if pkg.Spine.TOC != "" {
		if item, ok := items[pkg.Spine.TOC]; ok {
			name, er := archiveRef(opf, item.Href)
			if er != nil {
				return Book{}, er
			}
			data, er = a.get(name, maxEntry)
			if er != nil {
				return Book{}, er
			}
			var ncx struct {
				Points []navPoint `xml:"navMap>navPoint"`
			}
			if er = xml.Unmarshal(data, &ncx); er != nil {
				return Book{}, fmt.Errorf("invalid NCX: %w", er)
			}
			var visit func([]navPoint)
			visit = func(points []navPoint) {
				for _, p := range points {
					ref, e := archiveRef(name, p.Source)
					if e == nil && labels[ref] == "" {
						labels[ref] = strings.TrimSpace(p.Label)
					}
					visit(p.Children)
				}
			}
			visit(ncx.Points)
		}
	}
	if len(pkg.Spine.Items) > maxChapters {
		return Book{}, fmt.Errorf("EPUB spine exceeds chapter limit")
	}
	seen := map[string]bool{}
	textTotal := 0
	for _, entry := range pkg.Spine.Items {
		if err = ctx.Err(); err != nil {
			return Book{}, err
		}
		if entry.Linear == "no" {
			continue
		}
		item, ok := items[entry.ID]
		if !ok {
			return Book{}, fmt.Errorf("spine references missing manifest id %q", entry.ID)
		}
		if item.Type != "application/xhtml+xml" && item.Type != "text/html" {
			return Book{}, fmt.Errorf("unsupported spine media type %q", item.Type)
		}
		name, er := archiveRef(opf, item.Href)
		if er != nil {
			return Book{}, er
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		data, er = a.get(name, maxEntry)
		if er != nil {
			return Book{}, er
		}
		doc, er := goquery.NewDocumentFromReader(contextReader{ctx, bytes.NewReader(data)})
		if er != nil {
			return Book{}, er
		}
		title := labels[name]
		if title == "" {
			title = strings.TrimSpace(doc.Find("h1,h2,h3").First().Text())
		}
		if title == "" {
			title = strings.TrimSpace(doc.Find("title").First().Text())
		}
		if title == "" {
			title = fmt.Sprintf("Chapter %d", len(book.Chapters)+1)
		}
		var text strings.Builder
		body := doc.Find("body").First()
		for _, n := range body.Nodes {
			writeText(&text, n)
		}
		content := normalizeText(text.String())
		if content == "" {
			continue
		}
		if len(content) > maxChapter {
			return Book{}, fmt.Errorf("EPUB chapter exceeds 4 MiB")
		}
		textTotal += len(content)
		if textTotal > maxText {
			return Book{}, fmt.Errorf("EPUB text exceeds 64 MiB")
		}
		book.Chapters = append(book.Chapters, Chapter{title, content})
	}
	if len(book.Chapters) == 0 {
		return Book{}, fmt.Errorf("EPUB has no readable spine chapters")
	}
	return book, nil
}

type navPoint struct {
	Label    string
	Source   string
	Children []navPoint
}

func (n *navPoint) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	// encoding/xml cannot map an attribute through a nested path.
	var v struct {
		Label   string `xml:"navLabel>text"`
		Content struct {
			Src string `xml:"src,attr"`
		} `xml:"content"`
		Children []navPoint `xml:"navPoint"`
	}
	if err := d.DecodeElement(&v, &start); err != nil {
		return err
	}
	n.Label = v.Label
	n.Source = v.Content.Src
	n.Children = v.Children
	return nil
}
func writeText(b *strings.Builder, n *html.Node) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "noscript", "iframe", "object", "svg", "head":
			return
		case "br":
			b.WriteByte('\n')
			return
		}
		for _, a := range n.Attr {
			if a.Key == "hidden" || a.Key == "aria-hidden" && a.Val == "true" {
				return
			}
		}
	}
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeText(b, c)
	}
	if n.Type == html.ElementNode {
		switch n.Data {
		case "p", "div", "li", "section", "article", "h1", "h2", "h3", "blockquote", "tr":
			b.WriteByte('\n')
		}
	}
}
func normalizeText(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
