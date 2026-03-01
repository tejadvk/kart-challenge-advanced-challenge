package coupon

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	MinLength = 8
	MaxLength = 10
	MinFiles  = 2
)

// Validator validates promo codes against coupon base files.
// File content is loaded into memory at startup and refreshed by a background worker.
// IsValid performs only in-memory lookups—no file I/O on the order path.
type Validator struct {
	mu        sync.RWMutex
	filePaths []string
	content   [][]byte // decompressed content of each file; immutable after swap
}

// NewValidator creates a validator that checks codes against the given gzip file paths.
func NewValidator(filePaths []string) *Validator {
	return &Validator{
		filePaths: filePaths,
		content:   nil,
	}
}

// NewValidatorFromDataDir looks for couponbase1.gz, couponbase2.gz, couponbase3.gz in dataDir.
func NewValidatorFromDataDir(dataDir string) *Validator {
	paths := []string{
		filepath.Join(dataDir, "couponbase1.gz"),
		filepath.Join(dataDir, "couponbase2.gz"),
		filepath.Join(dataDir, "couponbase3.gz"),
	}
	return NewValidator(paths)
}

// CheckFilesExist verifies all coupon files exist and are readable.
// Returns error on first failure. Call at startup to fail fast if files are missing.
func (v *Validator) CheckFilesExist() error {
	for _, path := range v.filePaths {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		f.Close()
	}
	return nil
}

// LoadContent reads and decompresses all coupon files into memory.
// Content is uppercased so validation is case-insensitive regardless of file format.
// Replaces the current in-memory index. Call at startup and from background loader.
func (v *Validator) LoadContent() error {
	newContent := make([][]byte, 0, len(v.filePaths))
	for _, path := range v.filePaths {
		data, err := readAndDecompress(path)
		if err != nil {
			return err
		}
		newContent = append(newContent, bytes.ToUpper(data))
	}
	v.mu.Lock()
	v.content = newContent
	v.mu.Unlock()
	return nil
}

func readAndDecompress(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	return io.ReadAll(gz)
}

// StartBackgroundLoader runs a goroutine that periodically reloads coupon files into memory.
// File I/O happens only here—order path never touches disk.
// Stops when ctx is cancelled.
func (v *Validator) StartBackgroundLoader(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := v.LoadContent(); err != nil {
					log.Printf("[coupon] background reload failed: %v", err)
				}
			}
		}
	}()
}

// IsValid returns true if the promo code is valid:
// - length between 8 and 10 characters
// - found in at least 2 of the coupon files
// - case-insensitive: "happyhours" and "HAPPYHOURS" are equivalent
//
// Uses only in-memory content; no file I/O. Returns false if content not yet loaded.
func (v *Validator) IsValid(code string) bool {
	code = strings.TrimSpace(strings.ToUpper(code))
	if len(code) < MinLength || len(code) > MaxLength {
		return false
	}

	v.mu.RLock()
	content := v.content
	v.mu.RUnlock()

	if content == nil || len(content) == 0 {
		return false
	}

	codeBytes := []byte(code)
	count := 0
	for _, data := range content {
		if bytes.Contains(data, codeBytes) {
			count++
			if count >= MinFiles {
				return true
			}
		}
	}
	return count >= MinFiles
}
