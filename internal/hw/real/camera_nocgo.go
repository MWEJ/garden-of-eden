//go:build !cgo

package real

import "fmt"

// V4L2Camera is a stub used when CGO is disabled (e.g. cross-compile with
// CGO_ENABLED=0). The camera is not available in that configuration.
type V4L2Camera struct{}

func NewV4L2Camera(_, _ string) (*V4L2Camera, error) {
	return nil, fmt.Errorf("camera unavailable: built without CGO")
}

func (c *V4L2Camera) Capture() ([]byte, error) {
	return nil, fmt.Errorf("camera unavailable: built without CGO")
}
