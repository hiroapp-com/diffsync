package diffsync

import (
	"crypto/rand"
	mrand "math/rand"
	"time"
)

func firstNonEmpty(args ...string) string {
	for i := range args {
		if args[i] != "" {
			return args[i]
		}
	}
	return ""
}

func uuid() []byte {
	out := make([]byte, 16)
	if n, err := rand.Read(out); err != nil || n != len(out) {
		panic(err)
	}
	// RFC 4122
	out[8] = 0x80 // variant bits
	out[4] = 0x40 // v4
	return out
}

func randomString(length int) string {
	const src = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	bytes := make([]byte, length)
	n := int64(len(src))
	for i := range bytes {
		bytes[i] = src[mrand.Int63n(n)]
	}
	return string(bytes)
}

func init() {
	mrand.Seed(time.Now().UnixNano())
}
