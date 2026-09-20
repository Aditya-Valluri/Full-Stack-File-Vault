package admin

import "testing"

func TestQuotaInputPreservesBigint(t *testing.T) {
	for _, value := range []string{"0", "10000001", "9223372036854775807"} {
		if _, err := quotaInput(value); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"-1", "+1", "01", "1.0", "1e6", " 1", "9223372036854775808", ""} {
		if _, err := quotaInput(value); err == nil {
			t.Fatal("invalid quota accepted", value)
		}
	}
}
