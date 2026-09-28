package mailer

import "encoding/base64"

// AdministratorInbox is operator-owned, never selected by a visitor.
const AdministratorInbox = "fullstackfilevault@gmail.com"

func encodeContactBody(body string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	result := ""
	for len(encoded) > 76 {
		result += encoded[:76] + "\r\n"
		encoded = encoded[76:]
	}
	return result + encoded + "\r\n"
}
