package main

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestResolveRevision(t *testing.T) {
	const sha = "10ef88e3a1b2c3d4e5f60718293a4b5c6d7e8f90"
	tests := []struct {
		name, build, env, want string
	}{
		{"default", "dev", "", "dev"},
		{"build short sha", "10ef88e", "", "10ef88e"},
		{"build full sha is shortened", sha, "", "10ef88e"},
		{"env overrides build", "abc1234", sha, "10ef88e"},
		{"env short sha", "dev", "10ef88e", "10ef88e"},
		{"uppercase hex", "dev", "10EF88E3A1B2", "10EF88E"},
		{"env whitespace is trimmed", "dev", "  " + sha + "\n", "10ef88e"},
		{"blank env keeps build", "abc1234", "   ", "abc1234"},
		{"non-hex value is kept whole", "dev", "v1.2.3-rc1", "v1.2.3-rc1"},
		{"hex of exactly seven", "dev", "abcdef0", "abcdef0"},
		{"empty everywhere", "", "", "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveRevision(tt.build, tt.env); got != tt.want {
				t.Errorf("resolveRevision(%q, %q) = %q, want %q", tt.build, tt.env, got, tt.want)
			}
		})
	}
}

func TestSiteIcon(t *testing.T) {
	const svg, svgType = "img/favicon.svg", "image/svg+xml"
	empty := t.TempDir()
	withICO := t.TempDir()
	if err := os.WriteFile(filepath.Join(withICO, "favicon.ico"), []byte("ico"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirICO := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirICO, "favicon.ico"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, dir, path, mime string
	}{
		{"no favicon.ico keeps the default", empty, svg, svgType},
		{"owner's favicon.ico wins", withICO, "favicon.ico", "image/x-icon"},
		{"a directory is not an icon", dirICO, svg, svgType},
		{"missing static dir", filepath.Join(empty, "nope"), svg, svgType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, mime := siteIcon(tt.dir, svg, svgType)
			if path != tt.path || mime != tt.mime {
				t.Errorf("siteIcon = %q, %q; want %q, %q", path, mime, tt.path, tt.mime)
			}
		})
	}
}

// Files that are compressed already go out as they are, with their length;
// everything else is still compressed.
func TestCompression(t *testing.T) {
	archive := bytes.Repeat([]byte("already compressed "), 4096)
	page := bytes.Repeat([]byte("<p>compressible page text</p>"), 400)
	app := fiber.New()
	app.Use(compression())
	app.Get("/workwatch/releases/workwatch-1.0.0-darwin-arm64.tar.gz", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "application/gzip")
		return c.Send(archive)
	})
	app.Get("/blog/:slug/download/epub", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "application/epub+zip")
		return c.Send(archive)
	})
	app.Get("/blog/:slug/download/pdf", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "application/pdf")
		return c.Send(archive)
	})
	app.Get("/", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.Send(page)
	})

	for _, tc := range []struct {
		path       string
		body       []byte
		compressed bool
	}{
		{"/workwatch/releases/workwatch-1.0.0-darwin-arm64.tar.gz", archive, false},
		{"/blog/post/download/epub", archive, false},
		{"/blog/post/download/pdf", archive, false},
		{"/", page, true},
	} {
		for _, enc := range []string{"gzip", "br"} {
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Set("Accept-Encoding", enc)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			ce := resp.Header.Get("Content-Encoding")
			if tc.compressed {
				if ce != enc {
					t.Errorf("GET %s (%s): Content-Encoding = %q, want %s", tc.path, enc, ce, enc)
				}
				continue
			}
			if ce != "" {
				t.Errorf("GET %s (%s): Content-Encoding = %q, want none", tc.path, enc, ce)
			}
			if cl := resp.Header.Get("Content-Length"); cl != strconv.Itoa(len(tc.body)) {
				t.Errorf("GET %s (%s): Content-Length = %q, want %d", tc.path, enc, cl, len(tc.body))
			}
			if !bytes.Equal(got, tc.body) {
				t.Errorf("GET %s (%s): body changed (%d bytes, want %d)", tc.path, enc, len(got), len(tc.body))
			}
		}
	}
}
