package files

import "testing"

func TestFolderNames(t *testing.T) {
	for _, name := range []string{"", " ", ".", "..", "a/b", "a\\b", "line\nbreak", string([]byte{0xff})} {
		if _, err := folderName(name); err == nil {
			t.Fatalf("accepted invalid folder name %q", name)
		}
	}
	for _, name := range []string{"Projects", "Résumé", "财务"} {
		if value, err := folderName(" " + name + " "); err != nil || value != name {
			t.Fatal("valid folder name rejected")
		}
	}
}
