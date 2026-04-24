package indexer

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// decodeFileContent decodes raw file bytes into a UTF-8 string.
//
// Detection order:
//  1. Empty input -> "".
//  2. UTF-8 BOM (EF BB BF) -> strip BOM, return remainder.
//  3. UTF-16LE BOM (FF FE) -> decode as UTF-16 little-endian.
//  4. UTF-16BE BOM (FE FF) -> decode as UTF-16 big-endian.
//  5. Otherwise -> strings.ToValidUTF8 fallback (drops invalid byte sequences).
//
// The fallback always returns a nil error and a SQLite-safe string for
// typical Windows-1252 inputs (no NULs are introduced).
func decodeFileContent(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return string(data[3:]), nil
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		body := data[2:]
		if len(body)%2 != 0 {
			return "", fmt.Errorf("decode utf-16le: odd byte length %d", len(body))
		}
		u16 := make([]uint16, len(body)/2)
		for i := range u16 {
			u16[i] = binary.LittleEndian.Uint16(body[2*i:])
		}
		return string(utf16.Decode(u16)), nil
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		body := data[2:]
		if len(body)%2 != 0 {
			return "", fmt.Errorf("decode utf-16be: odd byte length %d", len(body))
		}
		u16 := make([]uint16, len(body)/2)
		for i := range u16 {
			u16[i] = binary.BigEndian.Uint16(body[2*i:])
		}
		return string(utf16.Decode(u16)), nil
	}
	return strings.ToValidUTF8(string(data), ""), nil
}
