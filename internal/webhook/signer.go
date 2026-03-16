// Package webhook provides the outbound delivery engine and HMAC signing utilities.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// SignPayload builds the X-OpenPay-Smart-Signature header value.
//
// Format:  t=<unix_timestamp>,v1=<hex_hmac_sha256>
// Payload: "<timestamp>.<body>"
//
// Consumers verify by:
//  1. Splitting the header on ","
//  2. Extracting the timestamp and checking it is within tolerance (e.g. 5 min)
//  3. Re-computing HMAC-SHA256(secret, "<timestamp>.<body>") and comparing
func SignPayload(secret string, body []byte) string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	payload := ts + "." + string(body)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("t=%s,v1=%s", ts, sig)
}

// VerifySignature validates an incoming X-OpenPay-Smart-Signature header.
// toleranceSec is the maximum age of a valid signature (prevents replay attacks).
func VerifySignature(secret, header string, body []byte, toleranceSec int64) bool {
	var ts, v1 string
	for _, part := range splitCSV(header) {
		if k, v, ok := splitKV(part); ok {
			switch k {
			case "t":
				ts = v
			case "v1":
				v1 = v
			}
		}
	}

	if ts == "" || v1 == "" {
		return false
	}

	unixTS, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}

	if time.Now().Unix()-unixTS > toleranceSec {
		return false // signature too old
	}

	payload := ts + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(v1), []byte(expected))
}

func splitCSV(s string) []string {
	var parts []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			parts = append(parts, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

func splitKV(s string) (string, string, bool) {
	for i, r := range s {
		if r == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
