package servertest

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// Fingerprint is the colon-separated MD5 fingerprint of an OpenSSH public key line ("" if it is none).
func Fingerprint(pub string) string {
	f := strings.Fields(pub)
	if len(f) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return ""
	}
	sum := md5.Sum(blob)
	parts := make([]string, len(sum))
	for i, x := range sum {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, ":")
}

// FingerprintFile is the fingerprint of the OpenSSH public key in a file ("" if it cannot be read).
func FingerprintFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return Fingerprint(string(b))
}
