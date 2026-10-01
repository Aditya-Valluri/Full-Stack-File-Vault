package files

import "testing"

func TestPreviewAllowlist(t *testing.T) {
	for _, media := range []string{"image/png", "image/jpeg", "image/webp", "text/plain", "text/plain; charset=utf-8", "application/pdf"} {
		if !PreviewAllowed(media) {
			t.Fatal("expected passive preview type allowed")
		}
	}
	for _, media := range []string{"text/html", "image/svg+xml", "application/javascript", "application/octet-stream", ""} {
		if PreviewAllowed(media) {
			t.Fatal("unsafe preview type", media)
		}
	}
}

func TestTextPreviewRequiresTXTNameAndDetectedMIME(t *testing.T) {
	for _, tc := range []struct {
		name, media string
		allowed     bool
	}{
		{"notes.TXT", "text/plain; charset=utf-8", true},
		{"script.js", "text/plain; charset=utf-8", false},
		{"page.html", "text/plain", false},
		{"image.svg", "image/svg+xml", false},
		{"fake.txt", "text/html", false},
		{"fake.pdf", "application/octet-stream", false},
		{"document.pdf", "application/pdf", true},
	} {
		if got := FilePreviewAllowed(tc.name, tc.media); got != tc.allowed {
			t.Errorf("preview policy for %q / %q: got %v", tc.name, tc.media, got)
		}
	}
}
