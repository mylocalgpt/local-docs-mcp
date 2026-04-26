package indexer

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mylocalgpt/local-docs-mcp/internal/store"
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
		{name: "multilingual", rawData: []byte("# Heading\n日本語 🎉\n")},
		{name: "utf8_bom", fixture: "utf8_bom.md"},
		{name: "utf16le", fixture: "utf16le.md"},
		{name: "utf16be", fixture: "utf16be.md"},
		{name: "windows1252", fixture: "windows1252.md"},
		{name: "utf16_truncated", fixture: "utf16_truncated.md", wantErr: true, skipValid: true},
		{name: "utf8_bom_invalid", rawData: []byte{0xEF, 0xBB, 0xBF, 0xFF}, wantErr: true, skipValid: true},
		{name: "embedded_nul", rawData: []byte("# Heading\nhi\x00there\n"), wantErr: true, skipValid: true},
		{name: "bomless_utf16_looking", rawData: []byte{'#', 0x00, ' ', 0x00}, wantErr: true, skipValid: true},
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

func TestEncodingContract_DecoderOutputIsStorageClean(t *testing.T) {
	windows1252, err := os.ReadFile(filepath.Join("testdata", "windows1252.md"))
	if err != nil {
		t.Fatalf("read windows1252 fixture: %v", err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: []byte{}},
		{name: "plain_ascii", data: []byte("# Heading\nplain ascii markdown\n")},
		{name: "multilingual_utf8", data: []byte("# Heading\n日本語 🎉\n")},
		{name: "utf8_bom_valid", data: append([]byte{0xEF, 0xBB, 0xBF}, []byte("# Heading\nwith bom\n")...)},
		{name: "utf16le_bom_valid", data: encodeUTF16LEWithBOM("# Heading\nutf16 le\n")},
		{name: "utf16be_bom_valid", data: encodeUTF16BEWithBOM("# Heading\nutf16 be\n")},
		{name: "embedded_nul", data: []byte("# Heading\nhi\x00there\n")},
		{name: "windows1252_fallback", data: windows1252},
		{name: "bomless_utf16_looking", data: []byte{'#', 0x00, ' ', 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := decodeFileContent(tt.data)
			if err != nil {
				return
			}

			s, err := store.NewStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close() //nolint:errcheck

			repoID, err := s.UpsertRepo("contract", "test", "[]", "local")
			if err != nil {
				t.Fatalf("UpsertRepo: %v", err)
			}
			docs := []store.Document{
				{
					RepoID:       repoID,
					Path:         "doc.md",
					DocTitle:     "Doc",
					SectionTitle: "Section",
					Content:      decoded,
					Tokens:       1,
				},
			}
			if err := s.ReplaceDocuments(repoID, docs); err != nil {
				t.Fatalf("ReplaceDocuments: %v", err)
			}

			invalid, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
			if err != nil {
				t.Fatalf("RepoHasInvalidEncoding: %v", err)
			}
			if invalid {
				t.Fatal("successful decoder output was stored as invalid encoding")
			}
		})
	}
}

func encodeUTF16LEWithBOM(s string) []byte {
	u16 := utf16.Encode([]rune(s))
	out := []byte{0xFF, 0xFE}
	for _, v := range u16 {
		out = binary.LittleEndian.AppendUint16(out, v)
	}
	return out
}

func encodeUTF16BEWithBOM(s string) []byte {
	u16 := utf16.Encode([]rune(s))
	out := []byte{0xFE, 0xFF}
	for _, v := range u16 {
		out = binary.BigEndian.AppendUint16(out, v)
	}
	return out
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
