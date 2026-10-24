package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShortSummary(t *testing.T) {
	tests := []struct {
		name string
		p    Project
		want string
	}{
		{"summary wins", Project{Summary: " Task queue ", Description: "A distributed task queue. Uses Redis."}, "Task queue"},
		{"first sentence", Project{Description: "A distributed task queue built in Go. Uses Redis for broker."}, "A distributed task queue built in Go"},
		{"single sentence", Project{Description: "Personal development environment configuration."}, "Personal development environment configuration"},
		{"no final period", Project{Description: "Zero-dependency migrations"}, "Zero-dependency migrations"},
		{"version numbers do not end a sentence", Project{Description: "Supports Go 1.22 and up. Fast."}, "Supports Go 1.22 and up"},
		{"domains do not end a sentence", Project{Description: "Runs on example.com today. More soon."}, "Runs on example.com today"},
		{"question mark is kept", Project{Description: "Why another queue? Because."}, "Why another queue?"},
		{"exclamation mark is kept", Project{Description: "It is fast! Really."}, "It is fast!"},
		{"line break after a period", Project{Description: "First line.\nSecond line."}, "First line"},
		{"whitespace collapses", Project{Description: "  Spaced   out\n text.  "}, "Spaced out text"},
		{"blank summary falls back", Project{Summary: "   ", Description: "Desc. More."}, "Desc"},
		{"empty", Project{}, ""},
		{"a leading title is dropped", Project{Title: "pixelQR", Description: "pixelQR: QR code generation. More."}, "QR code generation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.ShortSummary(); got != tt.want {
				t.Errorf("ShortSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBlurb(t *testing.T) {
	for _, tt := range []struct {
		title, desc, want string
	}{
		{"pixelQR", "pixelQR: QR Code Generation and Management.", "QR Code Generation and Management."},
		{"Tool", "tool — a small helper.", "A small helper."},
		{"Tool", "Tool – fast", "Fast"},
		{"Tool", "Tool - fast", "Fast"},
		{"WorkWatch", "A macOS utility that reads mail.", "A macOS utility that reads mail."},
		{"Go", "Gopher tools: many.", "Gopher tools: many."},                    // the title must be the whole first word
		{"pixelQR", "pixelQR is a QR code tool.", "pixelQR is a QR code tool."}, // no separator: a sentence
		{"pixelQR", "pixelQR:", "pixelQR:"},
		{"", "Plain.", "Plain."},
		{"X", "", ""},
	} {
		if got := (Project{Title: tt.title, Description: tt.desc}).Blurb(); got != tt.want {
			t.Errorf("Blurb(%q, %q) = %q, want %q", tt.title, tt.desc, got, tt.want)
		}
	}
}

func TestProjectJSON(t *testing.T) {
	var p Project
	data := `{"id":"ww","title":"WorkWatch","summary":"Short","figure":"workwatch","install":"curl -fsSL https://example.com/install.sh | bash","featured":true}`
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatal(err)
	}
	if p.Summary != "Short" || p.Figure != "workwatch" || p.Install != "curl -fsSL https://example.com/install.sh | bash" || !p.Featured {
		t.Errorf("decoded %+v", p)
	}
	out, err := json.Marshal(Project{ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"summary", "figure", "install"} {
		if strings.Contains(string(out), `"`+key+`"`) {
			t.Errorf("empty %s is not omitted: %s", key, out)
		}
	}
}
