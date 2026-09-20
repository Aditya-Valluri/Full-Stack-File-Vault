package files

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]string{" Work ", "work", "tax-2026"})
	if err != nil || !reflect.DeepEqual(got, []string{"tax-2026", "work"}) {
		t.Fatal(got, err)
	}
	for _, bad := range [][]string{{""}, {"a,b"}, {"private\nvalue"}, {strings.Repeat("a", 33)}, make([]string, 21)} {
		if _, err := NormalizeTags(bad); err == nil {
			t.Fatalf("accepted invalid tags %#v", bad)
		}
	}
	if tags, err := NormalizeTags(nil); err != nil || len(tags) != 0 {
		t.Fatal(tags, err)
	}
}
