package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func New(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("idgen: rand.Read failed: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

func Deployment() string { return New("dep") }
func APIKey() string     { return New("key") }
func Sandbox() string    { return New("sbx") }
func Job() string        { return New("job") }
func Log() string        { return New("log") }
func Resource() string   { return New("res") }
