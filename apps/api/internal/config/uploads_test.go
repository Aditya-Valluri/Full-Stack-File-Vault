package config

import "testing"

func TestUploadLimits(t *testing.T) {
	names := []string{"UPLOAD_MAX_FILE_BYTES", "UPLOAD_MAX_REQUEST_BYTES", "UPLOAD_MAX_FILES", "UPLOAD_MAX_CONCURRENT", "USER_CALLS_PER_SECOND", "UPLOAD_STAGING_DIR", "BLOB_STORAGE_DIR"}
	for _, name := range names {
		t.Setenv(name, "")
	}
	c, dir, limit, err := uploadConfig()
	if err != nil || c.MaxFileBytes != 20000000 || c.MaxRequestBytes != 21000000 || c.MaxFiles != 10 || c.MaxConcurrentRequests != 4 || limit != 2 || dir == "" {
		t.Fatal("incorrect defaults", c, err)
	}
	for _, tc := range []struct{ name, value string }{
		{"UPLOAD_MAX_FILE_BYTES", "0"}, {"UPLOAD_MAX_REQUEST_BYTES", "20000000"}, {"UPLOAD_MAX_FILES", "101"}, {"UPLOAD_MAX_CONCURRENT", "129"}, {"USER_CALLS_PER_SECOND", "0"}, {"USER_CALLS_PER_SECOND", "invalid"},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if _, _, _, err := uploadConfig(); err == nil {
				t.Fatal("invalid limit accepted")
			}
		})
	}
	t.Setenv("UPLOAD_MAX_FILE_BYTES", "30000000")
	t.Setenv("UPLOAD_MAX_REQUEST_BYTES", "31000000")
	t.Setenv("USER_CALLS_PER_SECOND", "5")
	c, _, limit, err = uploadConfig()
	if err != nil || c.MaxFileBytes != 30000000 || limit != 5 {
		t.Fatal("configurable limits failed")
	}
}
