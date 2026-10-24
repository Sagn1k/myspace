package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/sagnikb/myspace/internal/models"
)

// A page path with a trailing slash answers with a 301 to the slash-less
// path (query kept), so the canonical URL, og:url and aria-current are only
// ever computed from the canonical form; the redirect never leaves the site.
func TestTrailingSlashRedirect(t *testing.T) {
	app := newApp(newRealSite(t).h)

	redirects := []struct{ method, target, location string }{
		{http.MethodGet, "/blog/", "/blog"},
		{http.MethodGet, "/about/", "/about"},
		{http.MethodGet, "/projects/", "/projects"},
		{http.MethodGet, "/blog/building-cli-tools-in-go/", "/blog/building-cli-tools-in-go"},
		{http.MethodGet, "/tags/go/", "/tags/go"},
		{http.MethodGet, "/search/?q=go%20cli&page=2", "/search?q=go%20cli&page=2"},
		{http.MethodGet, "/blog//", "/blog"},
		{http.MethodGet, "/nope/", "/nope"},
		{http.MethodHead, "/contact/", "/contact"},
		// never a protocol-relative Location (another host)
		{http.MethodGet, "//evil.example/", "/evil.example"},
		{http.MethodGet, "///evil.example//", "/evil.example"},
		{http.MethodGet, "/%09/evil.example/", "/%09/evil.example"},
	}
	for _, tt := range redirects {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			resp := do(t, app, tt.method, tt.target)
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != tt.location {
				t.Errorf("Location = %q, want %q", loc, tt.location)
			}
		})
	}

	// Not redirected: the root, the static trees, other methods, and paths
	// a browser could read as another host or reinterpret.
	kept := []struct {
		method, target string
		status         int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/blog", http.StatusOK},
		{http.MethodGet, "/search?q=go", http.StatusOK},
		{http.MethodGet, "/static/css/", http.StatusNotFound},
		{http.MethodGet, "/workwatch/", http.StatusNotFound},
		{http.MethodPost, "/about/", http.StatusNotFound},
		{http.MethodGet, `/\evil.example/`, http.StatusNotFound},
	}
	for _, tt := range kept {
		t.Run("kept "+tt.method+" "+tt.target, func(t *testing.T) {
			resp := do(t, app, tt.method, tt.target)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if loc := resp.Header.Get("Location"); loc != "" {
				t.Errorf("unexpected Location %q", loc)
			}
		})
	}

	// The canonical page carries the canonical URL and the exact nav state.
	body := get(t, app, "/blog").body
	for _, want := range []string{
		`<link rel="canonical" href="https://sagnikbhowmick.com/blog">`,
		`<meta property="og:url" content="https://sagnikbhowmick.com/blog">`,
		`<a href="/blog" aria-current="page">Writing</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/blog is missing %s", want)
		}
	}
}

func do(t *testing.T, app *fiber.App, method, target string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, "/", nil)
	req.RequestURI = target
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// Tag links are one path segment each, so a tag holding "/", "#", "?" or a
// space still reaches its own page from every list that links it.
func TestTagLinksEscapeTheirPath(t *testing.T) {
	contentDir := t.TempDir()
	post := "---\ntitle: \"Odd Tags\"\ndate: 2026-03-01\ntags: [\"ci/cd\", \"c#\", \"a?b\", \"web dev\"]\n---\n\n## One\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(contentDir, "odd-tags.md"), []byte(post), 0o644); err != nil {
		t.Fatal(err)
	}
	config := models.DefaultConfig()
	config.AssetVersion = "testver"
	app := newApp(newSite(t, contentDir, realProjects, realTemplates, config).h)

	tags := map[string]string{"ci/cd": "/tags/ci%2Fcd", "c#": "/tags/c%23", "a?b": "/tags/a%3Fb", "web dev": "/tags/web%20dev"}
	for _, page := range []string{"/", "/blog", "/tags", "/blog/odd-tags", "/tags/c%23"} {
		body := get(t, app, page).body
		for tag, href := range tags {
			if !strings.Contains(body, `href="`+href+`"`) {
				t.Errorf("%s: no link %s for tag %q", page, href, tag)
			}
		}
	}
	for tag, href := range tags {
		resp := get(t, app, href)
		if resp.status != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", href, resp.status)
			continue
		}
		if want := `<link rel="canonical" href="https://sagnikbhowmick.com` + href + `">`; !strings.Contains(resp.body, want) {
			t.Errorf("GET %s: canonical missing %s", href, want)
		}
		if !strings.Contains(resp.body, `<span class="hash" aria-hidden="true">#</span>`+template.HTMLEscapeString(tag)+`</h1>`) {
			t.Errorf("GET %s: the title does not name %q", href, tag)
		}
	}
}
