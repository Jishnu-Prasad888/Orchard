package media

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
