package pairing

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	got, err := Normalize("  abcd-2345 ")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != "ABCD2345" {
		t.Fatalf("unexpected normalized code: %q", got)
	}
	for _, invalid := range []string{"short", "ABCD-10IO", "ABCD 2345", "ABCD-2345-extra"} {
		if _, err := Normalize(invalid); err == nil {
			t.Fatalf("invalid code %q accepted", invalid)
		}
	}
}

func TestGenerateCodeUsesUnambiguousAlphabet(t *testing.T) {
	service := &Service{random: bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7})}
	code, err := service.generateCode()
	if err != nil {
		t.Fatalf("generateCode: %v", err)
	}
	if len(code) != codeLength || strings.ContainsAny(code, "01IO") {
		t.Fatalf("ambiguous code generated: %q", code)
	}
}
