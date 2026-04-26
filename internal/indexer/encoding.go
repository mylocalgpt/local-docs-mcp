package indexer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// decodeFileContent decodes raw file bytes into a UTF-8 string.
//
// Detection order:
//  1. Empty input -> "".
//  2. UTF-8 BOM (EF BB BF) -> strip BOM, strictly validate remainder.
//  3. UTF-32 BOMs -> unsupported encoding error.
//  4. UTF-16LE BOM (FF FE) -> decode as UTF-16 little-endian.
//  5. UTF-16BE BOM (FE FF) -> decode as UTF-16 big-endian.
//  6. Otherwise -> strings.ToValidUTF8 fallback (drops invalid byte sequences).
//
// Every successful output rejects embedded NUL bytes so it is safe for the
// store encoding health scan. The no-BOM fallback preserves legacy behavior for
// typical Windows-1252 inputs when sanitization does not leave NUL bytes.
func decodeFileContent(data []byte) (string, error) {
	decoded, err := decodeFileContentRaw(data)
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(decoded, '\x00') {
		return "", errors.New("decode content: embedded nul byte")
	}
	return decoded, nil
}

func decodeFileContentRaw(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		if !utf8.Valid(data[3:]) {
			return "", errors.New("decode utf-8 bom: invalid utf-8")
		}
		return string(data[3:]), nil
	}
	if len(data) >= 4 && data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
		return "", errors.New("decode utf-32le: unsupported encoding")
	}
	if len(data) >= 4 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
		return "", errors.New("decode utf-32be: unsupported encoding")
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
