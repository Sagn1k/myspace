package assets

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVersion(t *testing.T) {
	base := map[string]string{
		"css/style.css": "body{}",
		"js/main.js":    "console.log(1)",
	}
	tests := []struct {
		name  string
		files map[string]string
		same  bool
	}{
		{"identical tree", base, true},
		{"edited file", map[string]string{"css/style.css": "body{color:red}", "js/main.js": "console.log(1)"}, false},
		{"renamed file", map[string]string{"css/site.css": "body{}", "js/main.js": "console.log(1)"}, false},
		{"added file", map[string]string{"css/style.css": "body{}", "js/main.js": "console.log(1)", "img/a.svg": "<svg/>"}, false},
	}

	baseDir := t.TempDir()
	writeFiles(t, baseDir, base)
	want, err := Version(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{10}$`).MatchString(want) {
		t.Fatalf("Version = %q, want 10 lowercase hex chars", want)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tt.files)
			got, err := Version(dir)
			if err != nil {
				t.Fatal(err)
			}
			if (got == want) != tt.same {
				t.Errorf("Version = %q, base = %q, want same=%v", got, want, tt.same)
			}
		})
	}
}

func TestVersionMissingDir(t *testing.T) {
	if _, err := Version(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing directory")
	}
}
