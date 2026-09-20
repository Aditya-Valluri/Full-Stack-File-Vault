package files

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func str(value string) *string { return &value }
func TestFilterValidation(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := from.Add(time.Hour)
	for _, f := range []Filter{
		{MinSizeBytes: str("-1")}, {MinSizeBytes: str("+1")}, {MinSizeBytes: str(" 1")},
		{MaxSizeBytes: str("9223372036854775808")}, {MinSizeBytes: str("9"), MaxSizeBytes: str("8")},
		{MIMEType: str("image/*")}, {MIMEType: str("text/plain; charset=utf-8")},
		{NameContains: str("bad\x00name")}, {NameContains: str(strings.Repeat("x", 256))},
		{CreatedFrom: &before, CreatedBefore: &from},
	} {
		if _, err := validateFilter(f); err == nil {
			t.Fatalf("invalid filter accepted: %+v", f)
		}
	}
	f, err := validateFilter(Filter{MIMEType: str("TEXT/PLAIN"), MinSizeBytes: str("0"), MaxSizeBytes: str("9223372036854775807"), CreatedFrom: &from, CreatedBefore: &before})
	if err != nil || f.mime != "text/plain" || *f.min != 0 {
		t.Fatal("valid filter rejected", err)
	}
	if escapeLike("100%_!back\\slash") != "100!%!_!!back\\slash" {
		t.Fatal("literal escaping changed")
	}
}
func TestCursorValidation(t *testing.T) {
	owner := "00000000-0000-4000-8000-000000000001"
	c := cursor{Version: 1, Owner: owner, ID: owner, CreatedAt: time.Date(2026, 1, 1, 12, 0, 0, 123456000, time.UTC)}
	encoded, err := encodeCursor(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCursor(encoded, owner)
	if err != nil || decoded != c {
		t.Fatal("cursor round trip failed", err)
	}
	if _, err = decodeCursor(encoded, "another-owner"); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	for _, value := range []string{"", "!", strings.Repeat("a", 513), base64.RawURLEncoding.EncodeToString([]byte("{}")), base64.RawURLEncoding.EncodeToString([]byte("{} {}"))} {
		if _, err = decodeCursor(value, owner); err == nil {
			t.Fatal("malformed cursor accepted")
		}
	}
}
