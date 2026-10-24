package download

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/sagnikb/myspace/internal/models"
)

func GenerateEPUB(blog *models.Blog) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// mimetype must be first and uncompressed
	mimeHeader := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	f, err := w.CreateHeader(mimeHeader)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte("application/epub+zip")); err != nil {
		return nil, err
	}

	files := []struct{ name, content string }{
		{"META-INF/container.xml", containerXML()},
		{"OEBPS/content.opf", contentOPF(blog)},
		{"OEBPS/nav.xhtml", navXHTML(blog)},
		{"OEBPS/toc.ncx", tocNCX(blog)},
		{"OEBPS/chapter1.xhtml", chapterXHTML(blog)},
		{"OEBPS/style.css", epubCSS()},
	}
	for _, file := range files {
		if err := writeFile(w, file.name, file.content); err != nil {
			return nil, fmt.Errorf("write %s: %w", file.name, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeFile(w *zip.Writer, name, content string) error {
	f, err := w.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(content))
	return err
}

func containerXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
}

func contentOPF(blog *models.Blog) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">sagnikbhowmick.com/blog/%s</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:creator>Sagnik Bhowmick</dc:creator>
    <dc:language>en</dc:language>
    <dc:date>%s</dc:date>
    <dc:description>%s</dc:description>
    <meta property="dcterms:modified">%s</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="style" href="style.css" media-type="text/css"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="chapter1"/>
  </spine>
</package>`,
		escapeXML(blog.Slug),
		escapeXML(blog.Title),
		blog.Date.Format("2006-01-02"),
		escapeXML(blog.Description),
		blog.Date.Format("2006-01-02T15:04:05Z"),
	)
}

// navXHTML is the EPUB 3 navigation document every EPUB 3 must have: the
// article, with its sections (blog.TOC: h2, and h3 under the h2 before it)
// linking to their headings in the chapter. toc.ncx stays for EPUB 2
// readers.
func navXHTML(blog *models.Blog) string {
	var b strings.Builder
	link := func(id, title string) {
		fmt.Fprintf(&b, `<a href="chapter1.xhtml#%s">%s</a>`, escapeXML(id), escapeXML(title))
	}
	open := 0 // nested lists left open: 1 inside the article's, 2 inside a section's
	for _, it := range blog.TOC {
		switch {
		case open == 0:
			b.WriteString("\n        <ol>\n          <li>")
			open = 1
		case it.Level == 3 && open == 1:
			b.WriteString("\n            <ol>\n              <li>")
			open = 2
		case it.Level == 2 && open == 2:
			b.WriteString("</li>\n            </ol>\n          </li>\n          <li>")
			open = 1
		case open == 2:
			b.WriteString("</li>\n              <li>")
		default:
			b.WriteString("</li>\n          <li>")
		}
		link(it.ID, it.Title)
	}
	switch open {
	case 2:
		b.WriteString("</li>\n            </ol>\n          </li>\n        </ol>\n      ")
	case 1:
		b.WriteString("</li>\n        </ol>\n      ")
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en" xml:lang="en">
<head>
  <meta charset="UTF-8"/>
  <title>%s</title>
</head>
<body>
  <nav epub:type="toc" id="toc">
    <h1>Contents</h1>
    <ol>
      <li><a href="chapter1.xhtml">%s</a>%s</li>
    </ol>
  </nav>
</body>
</html>`,
		escapeXML(blog.Title),
		escapeXML(blog.Title),
		b.String(),
	)
}

func tocNCX(blog *models.Blog) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="sagnikbhowmick.com/blog/%s"/>
  </head>
  <docTitle><text>%s</text></docTitle>
  <navMap>
    <navPoint id="chapter1" playOrder="1">
      <navLabel><text>%s</text></navLabel>
      <content src="chapter1.xhtml"/>
    </navPoint>
  </navMap>
</ncx>`,
		escapeXML(blog.Slug),
		escapeXML(blog.Title),
		escapeXML(blog.Title),
	)
}

// chapterXHTML must be well-formed XML: only numeric character references
// (no HTML named entities) and HTMLContent rendered as XHTML.
func chapterXHTML(blog *models.Blog) string {
	tags := ""
	if len(blog.Tags) > 0 {
		tags = fmt.Sprintf(`<p class="tags">Tags: %s</p>`, escapeXML(strings.Join(blog.Tags, ", ")))
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" lang="en">
<head>
  <meta charset="UTF-8"/>
  <title>%s</title>
  <link rel="stylesheet" href="style.css"/>
</head>
<body>
  <header>
    <h1>%s</h1>
    <p class="meta">%s &#183; %s min read</p>
    %s
  </header>
  <main>
    %s
  </main>
  <footer>
    <p>Published on sagnikbhowmick.com</p>
  </footer>
</body>
</html>`,
		escapeXML(blog.Title),
		escapeXML(blog.Title),
		blog.Date.Format("January 2, 2006"),
		strconv.Itoa(blog.ReadingTime),
		tags,
		blog.HTMLContent,
	)
}

func epubCSS() string {
	return `body {
  font-family: Georgia, serif;
  line-height: 1.6;
  color: #333;
  max-width: 100%;
  padding: 1em;
}
h1 { font-size: 1.8em; margin-bottom: 0.3em; }
h2 { font-size: 1.4em; margin-top: 1.5em; }
h3 { font-size: 1.2em; margin-top: 1.2em; }
.meta { color: #666; font-size: 0.9em; }
.tags { color: #666; font-size: 0.85em; }
pre { background: #f4f4f4; padding: 1em; overflow-x: auto; font-size: 0.85em; }
code { font-family: monospace; }
.code-block__header { color: #666; font-family: monospace; font-size: 0.75em; }
.code-block__copy, [hidden] { display: none; }
footer { margin-top: 3em; color: #999; font-size: 0.8em; border-top: 1px solid #eee; padding-top: 1em; }`
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
