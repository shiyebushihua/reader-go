// Package importer parses controlled local upload paths without extracting archives.
package importer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

type Chapter struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}
type Book struct {
	Title    string
	Author   string
	Kind     string
	Chapters []Chapter
}

const (
	maxUpload   = 100 << 20
	maxText     = 64 << 20
	maxChapter  = 4 << 20
	maxChapters = 20000
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func readBounded(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("content exceeds %d byte limit", limit)
	}
	return data, nil
}
func Parse(ctx context.Context, path string, originalName string) (Book, error) {
	if err := ctx.Err(); err != nil {
		return Book{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Book{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Book{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxUpload {
		return Book{}, fmt.Errorf("upload must be a regular file no larger than 100 MiB")
	}
	// Only the caller-supplied filename chooses the format; archive paths never touch disk.
	name := filepath.Base(strings.ReplaceAll(originalName, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	title := strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
	if title == "" {
		title = "Untitled"
	}
	switch ext {
	case ".txt":
		data, err := readBounded(ctx, f, maxText)
		if err != nil {
			return Book{}, err
		}
		return parseTXT(ctx, data, title)
	case ".epub":
		return parseEPUB(ctx, f, st.Size(), title)
	default:
		return Book{}, fmt.Errorf("unsupported file type: only TXT and EPUB are supported")
	}
}

var heading = regexp.MustCompile(`(?i)^(?:第[零〇一二三四五六七八九十百千万两0-9]+[章回节卷部篇].{0,100}|(?:chapter|book|part)\s+[0-9ivxlcdm]+(?:\b|[.:：]).{0,100}|序章|序言|前言|楔子|尾声|后记)$`)

func parseTXT(ctx context.Context, data []byte, title string) (Book, error) {
	book := Book{Title: title, Kind: "txt", Chapters: []Chapter{}}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(data) {
		var err error
		data, err = readBounded(ctx, transform.NewReader(bytes.NewReader(data), simplifiedchinese.GB18030.NewDecoder()), maxText)
		if err != nil {
			return Book{}, err
		}
		if bytes.Contains(data, []byte("\ufffd")) {
			return Book{}, fmt.Errorf("TXT is neither valid UTF-8 nor decodable GBK/GB18030")
		}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return Book{}, fmt.Errorf("binary or UTF-16 TXT is unsupported; use UTF-8 or GBK")
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return Book{}, fmt.Errorf("TXT contains no text")
	}
	chapterTitle := "正文"
	var content strings.Builder
	part := 1
	flush := func() error {
		s := strings.TrimSpace(content.String())
		if s == "" {
			content.Reset()
			return nil
		}
		t := chapterTitle
		if part > 1 {
			t = fmt.Sprintf("%s (%d)", t, part)
		}
		book.Chapters = append(book.Chapters, Chapter{t, s})
		content.Reset()
		part++
		if len(book.Chapters) > maxChapters {
			return fmt.Errorf("TXT exceeds %d chapters", maxChapters)
		}
		return nil
	}
	// Split even files without headings into bounded chapters, at UTF-8 boundaries.
	i := 0
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		i++
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return Book{}, err
			}
		}
		trimmed := strings.TrimSpace(line)
		if len(trimmed) <= 400 && heading.MatchString(trimmed) {
			if err := flush(); err != nil {
				return Book{}, err
			}
			chapterTitle = trimmed
			part = 1
		}
		for len(line) > 0 {
			room := maxChapter - content.Len() - 1
			if room < 4 {
				if err := flush(); err != nil {
					return Book{}, err
				}
				room = maxChapter - 1
			}
			n := min(room, len(line))
			for n < len(line) && n > 0 && !utf8.RuneStart(line[n]) {
				n--
			}
			content.WriteString(line[:n])
			line = line[n:]
			if line != "" {
				if err := flush(); err != nil {
					return Book{}, err
				}
			}
		}
		if content.Len() >= maxChapter-1 {
			if err := flush(); err != nil {
				return Book{}, err
			}
		}
		content.WriteByte('\n')
	}
	if err := flush(); err != nil {
		return Book{}, err
	}
	return book, nil
}
