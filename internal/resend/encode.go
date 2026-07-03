package resend

import "encoding/base64"

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
