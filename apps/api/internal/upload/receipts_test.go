package upload

import (
	"context"
	"strings"
	"testing"
)

func TestUploadFingerprint(t *testing.T) {
	stage := func(name, data string) *Staged {
		t.Helper()
		f, err := Stage(context.Background(), t.TempDir(), strings.NewReader(data), Metadata{Name: name}, 100)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}
	a, b := stage("one.txt", "same"), stage("two.txt", "same")
	first, _ := uploadFingerprint([]*Staged{a, b})
	replay, _ := uploadFingerprint([]*Staged{a, b})
	reverse, _ := uploadFingerprint([]*Staged{b, a})
	single, _ := uploadFingerprint([]*Staged{a})
	changed, _ := uploadFingerprint([]*Staged{stage("one.txt", "changed"), b})
	if first != replay || first == reverse || first == single || first == changed {
		t.Fatal("fingerprint must bind ordered content and metadata")
	}
	if !receiptKey.MatchString("12345678-1234-4234-9234-123456789abc") || receiptKey.MatchString("anything") {
		t.Fatal("key validation")
	}
}
