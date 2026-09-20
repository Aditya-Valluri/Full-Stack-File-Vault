package files

import "testing"

func TestPreviewAllowlist(t *testing.T) {
	for _, media := range []string{"image/png", "image/jpeg", "image/webp"} {
		if !PreviewAllowed(media) {
			t.Fatal("expected image allowed")
		}
	}
	for _, media := range []string{"text/html", "image/svg+xml", "application/pdf", "application/javascript", "text/plain", "application/octet-stream", ""} {
		if PreviewAllowed(media) {
			t.Fatal("unsafe preview type", media)
		}
	}
}
