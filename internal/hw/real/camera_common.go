package real

import (
	"fmt"
	"strconv"
	"strings"
)

// parseRes parses a "WxH" resolution string (e.g. "640x480"). Kept in an
// untagged file (not the cgo-only camera.go) so it builds and tests under both
// CGO_ENABLED=0 and =1.
func parseRes(s string) (uint32, uint32, error) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("bad resolution %q", s)
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("bad resolution %q", s)
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("bad resolution %q", s)
	}
	return uint32(w), uint32(h), nil
}
