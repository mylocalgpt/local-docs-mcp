package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDecodeFileContent(t *testing.T) {
	tests := []struct {
		name      string
		fixture   string // empty -> use rawData
		rawData   []byte
		wantErr   bool
		skipValid bool // skip content assertions (used for error cases)
	}{
		{name: "empty", rawData: []byte{}, wantErr: false},
		{name: "plain", fixture: "plain.md"},
		{name: "utf8_bom", fixture: "utf8_bom.md"},
		{name: "utf16le", fixture: "utf16le.md"},
		{name: "utf16be", fixture: "utf16be.md"},
		{name: "windows1252", fixture: "windows1252.md"},
		{name: "utf16_truncated", fixture: "utf16_truncated.md", wantErr: true, skipValid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.rawData
			if tt.fixture != "" {
				b, err := os.ReadFile(filepath.Join("testdata", tt.fixture))
				if err != nil {
					t.Fatalf("read fixture: %v", err)
				}
				data = b
			}

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("decodeFileContent panicked: %v", r)
				}
			}()

			got, err := decodeFileContent(data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.skipValid {
				return
			}
			if tt.name == "empty" {
				if got != "" {
					t.Fatalf("expected empty result, got %q", got)
				}
				return
			}
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8")
			}
			if strings.ContainsRune(got, '\x00') {
				t.Errorf("result contains NUL byte")
			}
			if !strings.HasPrefix(got, "# Heading") {
				t.Errorf("result does not start with %q, got prefix %q", "# Heading", safePrefix(got, 32))
			}
		})
	}
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
