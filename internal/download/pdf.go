package download

import (
	"bufio"
	"bytes"
	"fmt"
	stdhtml "html"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/go-pdf/fpdf"
	"github.com/sagnikb/myspace/internal/blog"
	"github.com/sagnikb/myspace/internal/models"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer/html"
)

// GeneratePDF typesets a post as an A4 PDF after the site's article page:
// Times for the prose (the site's serif), Courier for code, labels and page
// furniture (its mono), in the site's ink and vermilion. It walks the same
// markdown tree the page is rendered from (blog.Document), so the PDF has the
// page's typography, its section numbers (§ 01) and listing numbers, inline
// styles and links, and code keeps every character and its indentation.
//
// The PDF core fonts draw cp1252 (WinAnsi), so every string goes through
// fpdf's cp1252 translator, after asCP1252 has replaced what cp1252 lacks.
// Metadata and the outline are UTF-16 and take the text as it is.
func GeneratePDF(post *models.Blog, site models.SiteConfig) ([]byte, error) {
	return generatePDF(post, site, true)
}

// generatePDF is GeneratePDF with the page contents compressed or not; tests
// read the text operators of an uncompressed file.
func generatePDF(post *models.Blog, site models.SiteConfig, compress bool) ([]byte, error) {
	// The footer's "2 / 7" needs the page count: lay the post out once to
	// count its pages, then again with the count.
	draft, err := typesetPDF(post, site, 0)
	if err != nil {
		return nil, err
	}
	pdf, err := typesetPDF(post, site, draft.PageCount())
	if err != nil {
		return nil, err
	}
	pdf.SetCompression(compress)
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Page geometry in millimetres (A4), type sizes in points.
const (
	pageWidth    = 210.0
	pageHeight   = 297.0
	marginX      = 22.0
	marginTop    = 24.0
	marginBottom = 22.0
	ptToMM       = 25.4 / 72

	bodyPt   = 11.0
	bodyLead = 5.6 // 1.44 × 11 pt
	codePt   = 8.0
	codeLead = 4.0
	codePadX = 3.5 // inside a listing's box
	codePadY = 2.5
	// codeScale sets inline code smaller than the text around it, as the
	// site does, since Courier is wide.
	codeScale  = 0.88
	listIndent = 6.5
	quoteInset = 7.0
)

type rgb struct{ r, g, b int }

// The site's light palette (static/css/style.css, :root).
var (
	inkRGB      = rgb{0x1B, 0x19, 0x16} // --ink
	ink2RGB     = rgb{0x46, 0x42, 0x3B} // --ink-2
	ink3RGB     = rgb{0x66, 0x5F, 0x55} // --ink-3
	ruleRGB     = rgb{0xDC, 0xD4, 0xC5} // --rule
	accentRGB   = rgb{0xA8, 0x36, 0x1A} // --accent
	codeBgRGB   = rgb{0xEF, 0xE9, 0xDD} // --code-bg
	codeHeadRGB = rgb{0xE7, 0xE0, 0xD2} // --code-head
	codeInkRGB  = rgb{0x1F, 0x1C, 0x18} // --code-ink
	whiteRGB    = rgb{0xFF, 0xFF, 0xFF}
)

// style is how a run of text is set.
type style struct {
	family       string // "Times" or "Courier"
	bold, italic bool
	size         float64 // points
	color        rgb
	link         string // the URL the text links to, if any
	anchor       int    // or the fpdf link to a heading of the post
}

var (
	bodyStyle  = style{family: "Times", size: bodyPt, color: inkRGB}
	labelStyle = style{family: "Courier", size: 7.5, color: ink3RGB}
)

func (s style) fontStyle() string {
	switch {
	case s.bold && s.italic:
		return "BI"
	case s.bold:
		return "B"
	case s.italic:
		return "I"
	}
	return ""
}

func (s style) linked() bool { return s.link != "" || s.anchor != 0 }

// pdfWriter lays a post out on an fpdf document.
type pdfWriter struct {
	pdf     *fpdf.Fpdf
	tr      func(string) string // fpdf's cp1252 translator
	source  []byte
	postURL *url.URL         // the post's address, for relative links
	anchors map[string]int   // heading ID → fpdf link
	widths  map[string][]int // family+style → glyph widths per cp1252 byte
	st      style            // the style inline text is set in
	h2, h3  int              // section numbers, as the site counts them
	listing int              // listing number
	outline int              // the last bookmark's level (-1: none yet)
}

// typesetPDF lays the post out; pages is the page count for the footer, 0
// while it is unknown.
func typesetPDF(post *models.Blog, site models.SiteConfig, pages int) (*fpdf.Fpdf, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	doc, source := blog.Document(post.Content)
	postURL, err := url.Parse(strings.TrimSuffix(site.BaseURL, "/") + "/blog/" + url.PathEscape(post.Slug))
	if err != nil {
		return nil, fmt.Errorf("post address: %w", err)
	}
	w := &pdfWriter{
		pdf:     pdf,
		tr:      pdf.UnicodeTranslatorFromDescriptor(""), // cp1252
		source:  source,
		postURL: postURL,
		anchors: map[string]int{},
		widths:  map[string][]int{},
		st:      bodyStyle,
		outline: -1,
	}

	pdf.SetTitle(post.Title, true)
	pdf.SetAuthor(site.Author, true)
	pdf.SetSubject(post.Description, true)
	pdf.SetKeywords(strings.Join(post.Tags, ", "), true)
	pdf.SetCreator(site.Domain, true)
	pdf.SetLang("en")
	pdf.SetMargins(marginX, marginTop, marginX)
	pdf.SetAutoPageBreak(true, marginBottom)
	pdf.SetCellMargin(0)
	pdf.SetLineWidth(0.2)

	// Glyph widths, read once per face before the first page so measuring
	// text never switches fonts in a page's content.
	for _, family := range []string{"Times", "Courier"} {
		for _, fs := range []string{"", "B", "I", "BI"} {
			pdf.SetFont(family, fs, 10)
			widths := make([]int, 256)
			for c := 1; c < 256; c++ {
				widths[c] = pdf.GetStringSymbolWidth(string([]byte{byte(c)}))
			}
			w.widths[family+fs] = widths
		}
	}

	// every heading can be a link target (a "#id" link in the post)
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering {
			if id := headingID(h); id != "" {
				w.anchors[id] = pdf.AddLink()
			}
		}
		return ast.WalkContinue, nil
	})

	address := site.Domain + "/blog/" + post.Slug
	if site.Domain == "" {
		address = postURL.String()
	}
	pdf.SetHeaderFunc(func() { w.runningHead(post.Title, site.Author) })
	pdf.SetFooterFunc(func() { w.foot(address, postURL.String(), pages) })

	pdf.AddPage()
	w.opener(post)
	w.blocks(doc, false)
	w.colophon(address, postURL.String())
	return pdf, pdf.Error()
}

// enc converts UTF-8 text to the cp1252 bytes the core fonts draw.
func (w *pdfWriter) enc(s string) string { return w.tr(asCP1252(s)) }

// width is the width in millimetres of cp1252 text s set in st.
func (w *pdfWriter) width(s string, st style) float64 {
	widths := w.widths[st.family+st.fontStyle()]
	units := 0
	for i := 0; i < len(s); i++ {
		units += widths[s[i]]
	}
	return float64(units) * st.size * ptToMM / 1000
}

// measure is the width of the column text currently wraps in.
func (w *pdfWriter) measure() float64 {
	left, _, right, _ := w.pdf.GetMargins()
	return pageWidth - left - right
}

func (w *pdfWriter) setFont(st style) {
	w.pdf.SetFont(st.family, st.fontStyle(), st.size)
	w.pdf.SetTextColor(st.color.r, st.color.g, st.color.b)
}

func (w *pdfWriter) setDraw(c rgb, width float64) {
	w.pdf.SetDrawColor(c.r, c.g, c.b)
	w.pdf.SetLineWidth(width)
}

func (w *pdfWriter) setFill(c rgb) { w.pdf.SetFillColor(c.r, c.g, c.b) }

// need starts a new page unless h millimetres still fit above the bottom
// margin.
func (w *pdfWriter) need(h float64) {
	if w.pdf.GetY()+h > pageHeight-marginBottom {
		w.pdf.AddPage()
	}
}

// space moves down h millimetres, except at the top of a page.
func (w *pdfWriter) space(h float64) {
	if w.pdf.GetY() > marginTop+0.5 {
		w.pdf.Ln(h)
	}
}

// text sets s in st as a paragraph of the current column.
func (w *pdfWriter) text(s string, st style, lead float64) {
	p := &para{w: w}
	p.add(s, st)
	p.emit(p.lines(w.measure()), lead, w.measure(), east.AlignLeft)
}

// runningHead heads every page after the first with the title and author.
func (w *pdfWriter) runningHead(title, author string) {
	if w.pdf.PageNo() == 1 {
		return
	}
	w.setFont(labelStyle)
	right := w.enc(author)
	room := pageWidth - 2*marginX - w.width(right, labelStyle) - 10
	w.pdf.Text(marginX, 13, w.fit(w.enc(title), labelStyle, room))
	w.pdf.Text(pageWidth-marginX-w.width(right, labelStyle), 13, right)
	w.setDraw(ruleRGB, 0.2)
	w.pdf.Line(marginX, 15.5, pageWidth-marginX, 15.5)
}

// foot puts the post's address and the page number at the foot of a page.
func (w *pdfWriter) foot(address, link string, pages int) {
	w.setFont(labelStyle)
	text := w.enc(address)
	y := pageHeight - 11
	w.pdf.Text(marginX, y, text)
	w.pdf.LinkString(marginX, y-3, w.width(text, labelStyle), 4, link)
	if pages > 0 {
		n := strconv.Itoa(w.pdf.PageNo()) + " / " + strconv.Itoa(pages)
		w.pdf.Text(pageWidth-marginX-w.width(n, labelStyle), y, n)
	}
}

// fit shortens cp1252 text s with an ellipsis until it fits in room.
func (w *pdfWriter) fit(s string, st style, room float64) string {
	if w.width(s, st) <= room {
		return s
	}
	const ellipsis = "\x85" // … in cp1252
	for len(s) > 0 && w.width(s+ellipsis, st) > room {
		s = s[:len(s)-1]
	}
	return strings.TrimRight(s, " ") + ellipsis
}

// opener sets the article's head: topics, title, dek, facts and a rule.
func (w *pdfWriter) opener(post *models.Blog) {
	if len(post.Tags) > 0 {
		tags := post.Tags[:min(3, len(post.Tags))]
		w.text(strings.ToUpper(strings.Join(tags, " · ")), style{family: "Courier", size: 8, color: accentRGB}, 4.5)
		w.pdf.Ln(2.5)
	}
	w.text(post.Title, style{family: "Times", bold: true, size: 25, color: inkRGB}, 10.4)
	if post.Description != "" {
		w.pdf.Ln(2)
		w.text(post.Description, style{family: "Times", italic: true, size: 13.5, color: ink2RGB}, 6.6)
	}
	w.pdf.Ln(4)
	facts := []string{post.Date.Format("2 January 2006"), strconv.Itoa(post.ReadingTime) + " min read"}
	sections := 0
	for _, it := range post.TOC {
		if it.Level == 2 {
			sections++
		}
	}
	switch {
	case sections == 1:
		facts = append(facts, "1 section")
	case sections > 1:
		facts = append(facts, strconv.Itoa(sections)+" sections")
	}
	w.text(strings.Join(facts, "  ·  "), style{family: "Courier", size: 8, color: ink3RGB}, 4.5)
	w.pdf.Ln(3.5)
	y := w.pdf.GetY()
	w.setDraw(inkRGB, 0.35)
	w.pdf.Line(marginX, y, pageWidth-marginX, y)
	w.setDraw(ruleRGB, 0.2)
	w.pdf.Ln(9)
}

// colophon ends the article with where to read it online.
func (w *pdfWriter) colophon(address, link string) {
	w.space(9)
	w.need(12)
	y := w.pdf.GetY()
	w.setDraw(ruleRGB, 0.2)
	w.pdf.Line(marginX, y, pageWidth-marginX, y)
	w.pdf.Ln(4)
	meta := style{family: "Courier", size: 8, color: ink3RGB}
	linked := meta
	linked.color, linked.link = accentRGB, link
	p := &para{w: w}
	p.add("Read it online at ", meta)
	p.add(address, linked)
	p.emit(p.lines(w.measure()), 4.5, w.measure(), east.AlignLeft)
}

// blocks lays out the block children of parent; tight is a tight list
// item's (its blocks sit closer).
func (w *pdfWriter) blocks(parent ast.Node, tight bool) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		if prev := n.PreviousSibling(); prev != nil {
			w.space(gap(prev, n, tight))
		}
		w.block(n, tight)
	}
}

// gap is the space between the blocks prev and n.
func gap(prev, n ast.Node, tight bool) float64 {
	if h, ok := n.(*ast.Heading); ok {
		if h.Level <= 2 {
			return 8
		}
		return 5.5
	}
	if _, ok := prev.(*ast.Heading); ok {
		return 1.5
	}
	boxed := func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock, *east.Table, *ast.Blockquote, *ast.ThematicBreak:
			return true
		}
		return false
	}
	switch {
	case boxed(prev) || boxed(n):
		return 4.5
	case tight:
		return 1.4
	}
	return 3.2
}

func (w *pdfWriter) block(n ast.Node, tight bool) {
	switch n := n.(type) {
	case *ast.Heading:
		w.heading(n)
	case *ast.Paragraph, *ast.TextBlock:
		p := &para{w: w}
		w.inlines(p, n)
		p.emit(p.lines(w.measure()), bodyLead, w.measure(), east.AlignLeft)
	case *ast.List:
		w.list(n)
	case *ast.FencedCodeBlock:
		w.code(n, string(n.Language(w.source)))
	case *ast.CodeBlock:
		w.code(n, "")
	case *ast.Blockquote:
		w.quote(n)
	case *ast.ThematicBreak:
		w.rule()
	case *east.Table:
		w.table(n)
	case *ast.HTMLBlock:
		// raw HTML has nothing the PDF can draw
	default:
		w.blocks(n, tight)
	}
}

// heading sets a heading, numbered as the site numbers the article's own h2
// and h3 (§ 01, § 01.2), kept with the start of what follows it, and adds it
// to the PDF's outline.
func (w *pdfWriter) heading(h *ast.Heading) {
	top := h.Parent() != nil && h.Parent().Kind() == ast.KindDocument
	st := style{family: "Times", bold: true, size: 15.5, color: inkRGB}
	lead, label := 7.0, ""
	switch h.Level {
	case 1:
		st.size, lead = 18, 8
	case 2:
		if top {
			w.h2++
			w.h3 = 0
			label = fmt.Sprintf("§ %02d", w.h2)
		}
	case 3:
		st.size, lead = 12.5, 6
		if top {
			w.h3++
			label = fmt.Sprintf("§ %02d.%d", w.h2, w.h3)
		}
	default:
		st.size, lead = bodyPt, bodyLead
	}

	saved := w.st
	w.st = st
	p := &para{w: w}
	w.inlines(p, h)
	w.st = saved
	lines := p.lines(w.measure())
	labelLead := 0.0
	if label != "" {
		labelLead = 5
	}
	w.need(labelLead + float64(len(lines))*lead + 2*bodyLead)

	if a, ok := w.anchors[headingID(h)]; ok {
		w.pdf.SetLink(a, -1, -1)
	}
	w.bookmark(inlineString(h, w.source), h.Level)
	if label != "" {
		w.text(label, style{family: "Courier", bold: true, size: 7.5, color: accentRGB}, labelLead)
	}
	p.emit(lines, lead, w.measure(), east.AlignLeft)
}

// bookmark adds a heading to the outline: h1 and h2 at the top level, deeper
// headings one level down, never more than one below the previous entry.
func (w *pdfWriter) bookmark(title string, level int) {
	if title == "" {
		return
	}
	lvl := 0
	if level >= 3 {
		lvl = 1
	}
	lvl = min(lvl, w.outline+1)
	w.outline = lvl
	w.pdf.Bookmark(utf16BE(title), lvl, -1)
}

// utf16BE is s as a PDF text string beyond PDFDocEncoding: UTF-16BE behind a
// byte order mark.
func utf16BE(s string) string {
	units := utf16.Encode([]rune(s))
	b := make([]byte, 2, 2+2*len(units))
	b[0], b[1] = 0xFE, 0xFF
	for _, u := range units {
		b = append(b, byte(u>>8), byte(u))
	}
	return string(b)
}

// list sets a list: an accent bullet or number in the margin, each item's
// blocks indented past it.
func (w *pdfWriter) list(l *ast.List) {
	left, _, _, _ := w.pdf.GetMargins()
	number := l.Start
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		if item.PreviousSibling() != nil {
			if l.IsTight {
				w.space(1.4)
			} else {
				w.space(3.2)
			}
		}
		marker, ms := "•", style{family: "Times", bold: true, size: bodyPt, color: accentRGB}
		if l.IsOrdered() {
			marker = strconv.Itoa(number) + string(l.Marker)
			ms = style{family: "Courier", bold: true, size: 8.5, color: accentRGB}
			number++
		}
		w.need(bodyLead)
		w.pdf.SetLeftMargin(left + listIndent)
		w.pdf.SetX(left)
		w.setFont(ms)
		w.pdf.CellFormat(listIndent, bodyLead, w.enc(marker), "", 0, "L", false, 0, "")
		w.blocks(item, l.IsTight)
		w.pdf.SetLeftMargin(left)
		w.pdf.SetX(left)
	}
}

// code sets a code block as the site does: a box headed "Listing 01 · go ·
// 12 lines", the code in Courier with its spacing kept. A line too long for
// the box wraps, its continuation indented four columns past the line.
func (w *pdfWriter) code(n ast.Node, lang string) {
	w.listing++
	lang = strings.ToLower(lang)
	if lang == "" {
		lang = "text"
	}
	left, _, _, _ := w.pdf.GetMargins()
	boxW := w.measure()
	cols := int((boxW - 2*codePadX) / (0.6 * codePt * ptToMM)) // Courier: 600 units a glyph
	var rows []string
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		line := strings.TrimRight(string(seg.Value(w.source)), "\r\n")
		rows = append(rows, wrapCode(asCP1252(expandTabs(line)), cols)...)
	}
	count := lines.Len()

	const headH = 6.5
	full := headH + 2*codePadY + float64(len(rows))*codeLead
	if len(rows) <= 15 {
		w.need(full) // a short listing is never split
	} else {
		w.need(headH + codePadY + 6*codeLead)
	}

	// the head: Listing 01 · go · 12 lines
	w.setDraw(ruleRGB, 0.2)
	w.setFill(codeHeadRGB)
	x, y := left, w.pdf.GetY()
	w.pdf.CellFormat(boxW, headH, "", "1", 0, "", true, 0, "")
	w.pdf.SetXY(x+codePadX, y)
	plural := "s"
	if count == 1 {
		plural = ""
	}
	for _, part := range []struct {
		text string
		st   style
	}{
		{fmt.Sprintf("Listing %02d", w.listing), style{family: "Courier", bold: true, size: 7.5, color: ink2RGB}},
		{"  ·  ", labelStyle},
		{lang, style{family: "Courier", bold: true, size: 7.5, color: inkRGB}},
		{fmt.Sprintf("  ·  %d line%s", count, plural), labelStyle},
	} {
		text := w.enc(part.text)
		w.setFont(part.st)
		w.pdf.CellFormat(w.width(text, part.st), headH, text, "", 0, "L", false, 0, "")
	}
	w.pdf.SetXY(x, y+headH)

	// the code
	w.setFill(codeBgRGB)
	w.pdf.SetCellMargin(codePadX)
	w.pdf.CellFormat(boxW, codePadY, "", "LR", 1, "", true, 0, "")
	w.setFont(style{family: "Courier", size: codePt, color: codeInkRGB})
	for _, row := range rows {
		w.pdf.CellFormat(boxW, codeLead, w.tr(row), "LR", 1, "L", true, 0, "")
	}
	w.pdf.CellFormat(boxW, codePadY, "", "LRB", 1, "", true, 0, "")
	w.pdf.SetCellMargin(0)
	w.pdf.SetX(left)
}

// expandTabs replaces each tab with spaces to the next multiple of four
// columns, as the site shows code (tab-size: 4).
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// wrapCode cuts a code line longer than cols characters into rows, after the
// last space that leaves a row at least half full, else after the last comma,
// semicolon or opening bracket, else at cols; each continuation is indented
// four columns past the line's own indentation.
func wrapCode(line string, cols int) []string {
	rs := []rune(line)
	if len(rs) <= cols || cols < 8 {
		return []string{line}
	}
	indent := 0
	for indent < len(rs) && rs[indent] == ' ' {
		indent++
	}
	hang := strings.Repeat(" ", min(indent+4, cols/2))
	var rows []string
	prefix := ""
	for len(rs) > 0 {
		room := cols - len(prefix)
		if len(rs) <= room {
			rows = append(rows, prefix+string(rs))
			break
		}
		least := room / 2
		if prefix == "" {
			least = max(least, indent+1) // never break inside the indentation
		}
		cut := breakAt(rs, least, room, " ")
		if cut < 0 {
			cut = breakAt(rs, least, room, ",;([{")
		}
		if cut < 0 {
			cut = room
		}
		rows = append(rows, prefix+strings.TrimRight(string(rs[:cut]), " "))
		rs = rs[cut:]
		for len(rs) > 0 && rs[0] == ' ' {
			rs = rs[1:]
		}
		prefix = hang
	}
	return rows
}

// breakAt is the last position in (least, room] just after one of chars in
// rs, or -1.
func breakAt(rs []rune, least, room int, chars string) int {
	for i := room; i > least; i-- {
		if strings.ContainsRune(chars, rs[i-1]) {
			return i
		}
	}
	return -1
}

// quote sets a blockquote as the site does: italic, ink-2, behind an accent
// rule that runs down every page the quote spans.
func (w *pdfWriter) quote(n *ast.Blockquote) {
	left, _, _, _ := w.pdf.GetMargins()
	saved := w.st
	w.st.italic, w.st.color = true, ink2RGB
	w.pdf.SetLeftMargin(left + quoteInset)
	w.pdf.SetX(left + quoteInset)
	firstPage, top := w.pdf.PageNo(), w.pdf.GetY()
	w.blocks(n, false)
	lastPage, bottom := w.pdf.PageNo(), w.pdf.GetY()
	w.st = saved

	w.setDraw(accentRGB, 0.5)
	for page := firstPage; page <= lastPage; page++ {
		y0, y1 := marginTop, pageHeight-marginBottom
		if page == firstPage {
			y0 = top
		}
		if page == lastPage {
			y1 = bottom
		}
		w.pdf.SetPage(page)
		w.pdf.Line(left+0.5, y0+0.8, left+0.5, y1-0.8)
	}
	w.pdf.SetPage(lastPage)
	w.setDraw(ruleRGB, 0.2)
	w.pdf.SetLeftMargin(left)
	w.pdf.SetXY(left, bottom)
}

// rule sets a thematic break as the site does: a hairline with a small
// accent square at its centre.
func (w *pdfWriter) rule() {
	w.need(8)
	left, _, _, _ := w.pdf.GetMargins()
	right := pageWidth - marginX
	y := w.pdf.GetY() + 3.5
	w.setDraw(ruleRGB, 0.25)
	w.pdf.Line(left, y, right, y)
	mid := (left + right) / 2
	w.setFill(whiteRGB)
	w.pdf.Rect(mid-3, y-1.5, 6, 3, "F")
	w.setFill(accentRGB)
	w.pdf.Rect(mid-1, y-1, 2, 2, "F")
	w.setDraw(ruleRGB, 0.2)
	w.pdf.SetXY(left, y+3.5)
}

// table sets a GFM table between rules: a bold header row over a dark rule,
// columns sized to their content, each cell's text wrapped and aligned.
func (w *pdfWriter) table(t *east.Table) {
	cols := len(t.Alignments)
	if cols == 0 {
		return
	}
	type row struct {
		cells  []ast.Node
		header bool
	}
	var rows []row
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		var cells []ast.Node
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			cells = append(cells, c)
		}
		_, header := r.(*east.TableHeader)
		rows = append(rows, row{cells, header})
	}

	const pad, padY, lead = 1.5, 1.6, 4.8
	cell := style{family: "Times", size: 9.5, color: inkRGB}
	left, _, _, _ := w.pdf.GetMargins()
	total := w.measure()
	// each column as wide as its longest cell needs, scaled down to fit,
	// but never below an equal share's half
	natural := make([]float64, cols)
	for _, r := range rows {
		for j, c := range r.cells {
			if j < cols {
				st := cell
				st.bold = r.header
				natural[j] = max(natural[j], w.width(w.enc(inlineString(c, w.source)), st)+2*pad)
			}
		}
	}
	sum := 0.0
	for _, v := range natural {
		sum += v
	}
	widths := make([]float64, cols)
	floor := total / float64(cols) / 2
	for j, v := range natural {
		widths[j] = max(floor, v*total/max(sum, 1))
	}
	if sum <= total {
		copy(widths, natural)
		widths[cols-1] += total - sum // the last column takes the slack
	} else {
		scale := 0.0
		for _, v := range widths {
			scale += v
		}
		for j := range widths {
			widths[j] *= total / scale
		}
	}

	saved := w.st
	defer func() { w.st = saved }()
	for _, r := range rows {
		paras := make([][][]word, cols)
		height := 1
		for j := 0; j < cols; j++ {
			p := &para{w: w}
			w.st = cell
			w.st.bold = r.header
			if j < len(r.cells) {
				w.inlines(p, r.cells[j])
			}
			paras[j] = p.lines(widths[j] - 2*pad)
			height = max(height, len(paras[j]))
		}
		h := float64(height)*lead + 2*padY
		w.need(h)
		y := w.pdf.GetY()
		x := left
		for j := 0; j < cols; j++ {
			w.pdf.SetLeftMargin(x + pad)
			w.pdf.SetXY(x+pad, y+padY)
			p := &para{w: w}
			p.emit(paras[j], lead, widths[j]-2*pad, t.Alignments[j])
			x += widths[j]
		}
		w.pdf.SetLeftMargin(left)
		if r.header {
			w.setDraw(inkRGB, 0.3)
		} else {
			w.setDraw(ruleRGB, 0.2)
		}
		w.pdf.Line(left, y+h, left+total, y+h)
		w.setDraw(ruleRGB, 0.2)
		w.pdf.SetXY(left, y+h)
	}
}

// inlines adds the inline children of n to p in the current style.
func (w *pdfWriter) inlines(p *para, n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		w.inline(p, c)
	}
}

func (w *pdfWriter) inline(p *para, n ast.Node) {
	saved := w.st
	defer func() { w.st = saved }()
	switch n := n.(type) {
	case *ast.Text:
		p.add(leafText(n, w.source), w.st)
		switch {
		case n.HardLineBreak():
			p.lineBreak()
		case n.SoftLineBreak():
			p.add(" ", w.st)
		}
	case *ast.String:
		p.add(leafText(n, w.source), w.st)
	case *ast.CodeSpan:
		w.st.family, w.st.bold, w.st.italic = "Courier", false, false
		w.st.size *= codeScale
		p.add(codeSpanText(n, w.source), w.st)
	case *ast.Emphasis:
		if n.Level >= 2 {
			w.st.bold = true
		} else {
			w.st.italic = true
		}
		w.inlines(p, n)
	case *ast.Link:
		w.linkTo(string(n.Destination))
		w.inlines(p, n)
	case *ast.AutoLink:
		dest := string(n.URL(w.source))
		if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(dest), "mailto:") {
			dest = "mailto:" + dest
		}
		w.linkTo(dest)
		p.add(string(n.Label(w.source)), w.st)
	case *ast.Image:
		// the description stands in for the picture
		w.st.italic, w.st.color = true, ink3RGB
		alt := "Image"
		if s := inlineString(n, w.source); s != "" {
			alt += ": " + s
		}
		p.add("["+alt+"]", w.st)
	case *east.TaskCheckBox:
		w.st.family, w.st.bold, w.st.italic = "Courier", false, false
		w.st.size *= codeScale
		box := "[ ]"
		if n.IsChecked {
			box = "[x]"
		}
		p.add(box+" ", w.st)
	case *ast.RawHTML:
		// inline markup has nothing the PDF can draw
	default:
		w.inlines(p, n) // strikethrough and the like: their text
	}
}

// linkTo links the following text to dest: a heading of the post ("#id")
// inside the PDF, anything else at its absolute address. Links are ink with
// an accent underline, as in the site's prose.
func (w *pdfWriter) linkTo(dest string) {
	if id, ok := strings.CutPrefix(dest, "#"); ok {
		if a, ok := w.anchors[id]; ok {
			w.st.anchor = a
			return
		}
	}
	if u, err := url.Parse(dest); err == nil {
		w.st.link = w.postURL.ResolveReference(u).String()
	}
}

// leafText is the text of a Text or String node as goldmark's HTML renderer
// writes it (backslash escapes and entity references resolved), unescaped.
func leafText(n ast.Node, source []byte) string {
	var value []byte
	switch n := n.(type) {
	case *ast.Text:
		if n.IsRaw() {
			return string(n.Segment.Value(source))
		}
		value = n.Segment.Value(source)
	case *ast.String:
		if n.IsCode() || n.IsRaw() {
			return string(n.Value)
		}
		value = n.Value
	default:
		return ""
	}
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	html.DefaultWriter.Write(bw, value)
	_ = bw.Flush()
	return stdhtml.UnescapeString(buf.String())
}

// codeSpanText is a code span's text as written (a line break is a space).
func codeSpanText(n *ast.CodeSpan, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Segment.Value(source))
		case *ast.String:
			b.Write(c.Value)
		}
	}
	return strings.ReplaceAll(b.String(), "\n", " ")
}

// inlineString is the plain text of n's inline content.
func inlineString(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || c == n {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.WriteString(leafText(c, source))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.WriteString(leafText(c, source))
		case *ast.CodeSpan:
			b.WriteString(codeSpanText(c, source))
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			b.Write(c.Label(source))
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

func headingID(h *ast.Heading) string {
	v, ok := h.AttributeString("id")
	if !ok {
		return ""
	}
	switch id := v.(type) {
	case []byte:
		return string(id)
	case string:
		return id
	}
	return ""
}

// A paragraph is laid out in words: the text between two spaces, in one or
// more styled pieces ("Go’s" from three text nodes, or a bold label and the
// no-break space and dash after it). Lines break only between words, so a
// word never splits where the markdown tree happens to, and a no-break space
// holds.
type piece struct {
	text  string // cp1252
	st    style
	width float64
}

type word struct {
	pieces []piece
	width  float64
	space  piece // the space after the word; empty at the end
	hard   bool  // a hard line break follows the word
}

type para struct {
	w     *pdfWriter
	words []word
	cur   word
}

// add appends UTF-8 text set in st.
func (p *para) add(s string, st style) {
	s = p.w.enc(s)
	for {
		i := strings.IndexByte(s, ' ')
		if i < 0 {
			break
		}
		p.piece(s[:i], st)
		p.endWord(piece{" ", st, p.w.width(" ", st)})
		s = s[i+1:]
	}
	p.piece(s, st)
}

func (p *para) piece(s string, st style) {
	if s == "" {
		return
	}
	pc := piece{s, st, p.w.width(s, st)}
	if n := len(p.cur.pieces); n > 0 && p.cur.pieces[n-1].st == st {
		p.cur.pieces[n-1].text += s
		p.cur.pieces[n-1].width += pc.width
	} else {
		p.cur.pieces = append(p.cur.pieces, pc)
	}
	p.cur.width += pc.width
}

// endWord ends the current word with the space sp after it. Spaces with no
// word before them collapse, as in HTML.
func (p *para) endWord(sp piece) {
	if len(p.cur.pieces) == 0 {
		return
	}
	p.cur.space = sp
	p.words = append(p.words, p.cur)
	p.cur = word{}
}

func (p *para) lineBreak() {
	p.endWord(piece{})
	if n := len(p.words); n > 0 {
		p.words[n-1].hard = true
	}
}

// lines breaks the paragraph into lines no wider than width; a word wider
// than a line is cut between characters.
func (p *para) lines(width float64) [][]word {
	p.endWord(piece{})
	var lines [][]word
	var line []word
	used := 0.0
	for _, whole := range p.words {
		for _, wd := range p.w.cut(whole, width) {
			if n := len(line); n > 0 {
				if used+line[n-1].space.width+wd.width > width {
					lines = append(lines, line)
					line, used = nil, 0
				} else {
					used += line[n-1].space.width
				}
			}
			line = append(line, wd)
			used += wd.width
			if wd.hard {
				lines = append(lines, line)
				line, used = nil, 0
			}
		}
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	return lines
}

// cut splits a word wider than width into parts that fit.
func (w *pdfWriter) cut(wd word, width float64) []word {
	if wd.width <= width {
		return []word{wd}
	}
	var parts []word
	var cur word
	for _, pc := range wd.pieces {
		for i := 0; i < len(pc.text); i++ {
			ch := pc.text[i : i+1]
			cw := w.width(ch, pc.st)
			if cur.width > 0 && cur.width+cw > width {
				parts = append(parts, cur)
				cur = word{}
			}
			if n := len(cur.pieces); n > 0 && cur.pieces[n-1].st == pc.st {
				cur.pieces[n-1].text += ch
				cur.pieces[n-1].width += cw
			} else {
				cur.pieces = append(cur.pieces, piece{ch, pc.st, cw})
			}
			cur.width += cw
		}
	}
	cur.space, cur.hard = wd.space, wd.hard
	return append(parts, cur)
}

// emit writes the lines from the current position down, lead millimetres
// apart; align sets each line in a column width wide. A line goes out as one
// cell per run of text in one style, spaces included.
func (p *para) emit(lines [][]word, lead, width float64, align east.Alignment) {
	pdf := p.w.pdf
	for _, line := range lines {
		var runs []piece
		used := 0.0
		add := func(pc piece) {
			used += pc.width
			if n := len(runs); n > 0 && runs[n-1].st == pc.st {
				runs[n-1].text += pc.text
				runs[n-1].width += pc.width
				return
			}
			runs = append(runs, pc)
		}
		for i, wd := range line {
			for _, pc := range wd.pieces {
				add(pc)
			}
			if i < len(line)-1 && wd.space.text != "" {
				add(wd.space)
			}
		}
		if align == east.AlignRight || align == east.AlignCenter {
			left, _, _, _ := pdf.GetMargins()
			offset := width - used
			if align == east.AlignCenter {
				offset /= 2
			}
			pdf.SetX(left + max(offset, 0))
		}
		for _, run := range runs {
			p.w.cell(run, lead)
		}
		pdf.Ln(lead)
	}
}

// cell writes one piece where the line has got to; a link is underlined in
// the accent.
func (w *pdfWriter) cell(pc piece, lead float64) {
	w.setFont(pc.st)
	w.pdf.CellFormat(pc.width, lead, pc.text, "", 0, "L", false, pc.st.anchor, pc.st.link)
	if pc.st.linked() {
		x, y := w.pdf.GetXY() // the cell ends where the next begins
		size := pc.st.size * ptToMM
		base := y + 0.5*lead + 0.3*size // where fpdf puts the baseline
		w.setDraw(accentRGB, 0.2)
		w.pdf.Line(x-pc.width, base+0.16*size, x, base+0.16*size)
		w.setDraw(ruleRGB, 0.2)
	}
}
