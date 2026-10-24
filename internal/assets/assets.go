// Package assets computes the cache-busting version for static files.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Version returns the first 10 hex characters of a sha256 over the path and
// content of every regular file under dir. The walk is lexical, so the result
// only changes when a file is added, removed, renamed or edited.
func Version(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%x\n", filepath.ToSlash(rel), sha256.Sum256(data))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", dir, err)
	}
	return hex.EncodeToString(h.Sum(nil))[:10], nil
}
