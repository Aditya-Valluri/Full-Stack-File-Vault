package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	const name = "VAULT_TEST_SECRET"
	t.Setenv(name, "")
	t.Setenv(name+"_FILE", "")
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("private-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(name+"_FILE", path)
	value, err := Read(name)
	if err != nil || value != "private-value" {
		t.Fatal("file secret read failed")
	}
	t.Setenv(name, "other")
	if _, err = Read(name); err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatal("conflicting sources accepted or secret leaked")
	}
	t.Setenv(name, "")
	t.Setenv(name+"_FILE", path+"-missing")
	if _, err = Read(name); err == nil || strings.Contains(err.Error(), path) {
		t.Fatal("missing secret accepted or path leaked")
	}
}
