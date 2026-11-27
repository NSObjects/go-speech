// Package tc3 implements Tencent Cloud API 3.0 request signing.
package tc3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const ContentType = "application/json; charset=utf-8"

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSum(key []byte, text string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(text))
	return h.Sum(nil)
}

// Authorization implements Cloud API TC3 signing, not the WebSocket HMAC-SHA1
// scheme. Bind the action, host and exact serialized request bytes.
func Authorization(id, key, host, action, service string, now time.Time, body []byte) string {
	const signed = "content-type;host;x-tc-action"
	const algorithm = "TC3-HMAC-SHA256"
	date := now.UTC().Format("2006-01-02")
	scope := date + "/" + service + "/tc3_request"
	headers := "content-type:" + ContentType + "\nhost:" + strings.ToLower(host) + "\nx-tc-action:" + strings.ToLower(action) + "\n"
	canonical := "POST\n/\n\n" + headers + "\n" + signed + "\n" + digest(body)
	toSign := algorithm + "\n" + strconv.FormatInt(now.Unix(), 10) + "\n" + scope + "\n" + digest([]byte(canonical))
	secretDate := hmacSum([]byte("TC3"+key), date)
	secretService := hmacSum(secretDate, service)
	secretSigning := hmacSum(secretService, "tc3_request")
	return algorithm + " Credential=" + id + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + hex.EncodeToString(hmacSum(secretSigning, toSign))
}
