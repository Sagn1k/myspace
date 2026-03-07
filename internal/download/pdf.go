package download

import (
	"bytes"
	"strings"

	"github.com/go-pdf/fpdf"
	"github.com/sagnikb/myspace/internal/models"
)

func GeneratePDF(blog *models.Blog) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	// Title
	pdf.SetFont("Helvetica", "B", 24)
	pdf.SetTextColor(23, 23, 23)
	pdf.MultiCell(0, 10, blog.Title, "", "L", false)
	pdf.Ln(4)

	// Metadata line
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(107, 114, 128)
	meta := blog.Date.Format("January 2, 2006")
	if len(blog.Tags) > 0 {
		meta += "  |  " + strings.Join(blog.Tags, ", ")
	}
	meta += "  |  " + formatReadingTime(blog.ReadingTime)
	pdf.MultiCell(0, 5, meta, "", "L", false)
	pdf.Ln(2)

	// Divider
	pdf.SetDrawColor(229, 229, 229)
	pdf.Line(10, pdf.GetY(), 200, pdf.GetY())
	pdf.Ln(6)

	// Description
	if blog.Description != "" {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(107, 114, 128)
		pdf.MultiCell(0, 6, blog.Description, "", "L", false)
		pdf.Ln(6)
	}

	// Content
	renderContentToPDF(pdf, blog.Content)

	// Footer
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(158, 158, 158)
	pdf.SetY(-15)
	pdf.Cell(0, 10, "sagnikbhowmick.com")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderContentToPDF(pdf *fpdf.Fpdf, content string) {
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			pdf.Ln(3)
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "### "):
			pdf.SetFont("Helvetica", "B", 13)
			pdf.SetTextColor(23, 23, 23)
			pdf.Ln(4)
			pdf.MultiCell(0, 6, strings.TrimPrefix(trimmed, "### "), "", "L", false)
			pdf.Ln(2)

		case strings.HasPrefix(trimmed, "## "):
			pdf.SetFont("Helvetica", "B", 16)
			pdf.SetTextColor(23, 23, 23)
			pdf.Ln(6)
			pdf.MultiCell(0, 8, strings.TrimPrefix(trimmed, "## "), "", "L", false)
			pdf.Ln(3)

		case strings.HasPrefix(trimmed, "# "):
			// Skip h1, we already have the title

		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			pdf.SetFont("Helvetica", "", 11)
			pdf.SetTextColor(55, 55, 55)
			text := strings.TrimLeft(trimmed, "-* ")
			pdf.Cell(8, 6, "")
			pdf.MultiCell(0, 6, "  "+string(rune(8226))+"  "+text, "", "L", false)

		case strings.HasPrefix(trimmed, "```"):
			// Code block marker, skip

		default:
			pdf.SetFont("Helvetica", "", 11)
			pdf.SetTextColor(55, 55, 55)
			// Clean markdown formatting
			text := cleanMarkdownInline(trimmed)
			pdf.MultiCell(0, 6, text, "", "L", false)
		}
	}
}

func cleanMarkdownInline(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

func formatReadingTime(minutes int) string {
	if minutes == 1 {
		return "1 min read"
	}
	return strings.Replace("N min read", "N", itoa(minutes), 1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
