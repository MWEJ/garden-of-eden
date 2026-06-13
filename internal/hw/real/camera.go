package real

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/vladimirvivien/go4vl/device"
	"github.com/vladimirvivien/go4vl/v4l2"
)

// V4L2Camera implements hw.Camera. resolution is "WxH" e.g. "640x480".
type V4L2Camera struct {
	devPath string
	w, h    uint32
}

func NewV4L2Camera(devPath, resolution string) (*V4L2Camera, error) {
	w, h, err := parseRes(resolution)
	if err != nil {
		return nil, err
	}
	return &V4L2Camera{devPath: devPath, w: w, h: h}, nil
}

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

func (c *V4L2Camera) Capture() ([]byte, error) {
	dev, err := device.Open(c.devPath,
		device.WithPixFormat(v4l2.PixFormat{
			PixelFormat: v4l2.PixelFmtMJPEG, Width: c.w, Height: c.h,
		}))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", c.devPath, err)
	}
	defer dev.Close()

	const captureTimeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
	defer cancel()

	if err := dev.Start(ctx); err != nil {
		return nil, fmt.Errorf("start %s: %w", c.devPath, err)
	}

	frames := dev.GetOutput()
	var frame []byte
	const warmup = 2
	for i := 0; i <= warmup; i++ {
		select {
		case f, ok := <-frames:
			if !ok {
				return nil, fmt.Errorf("camera stream closed")
			}
			frame = f
		case <-ctx.Done():
			return nil, fmt.Errorf("camera %s timeout after %s", c.devPath, captureTimeout)
		}
	}
	if len(frame) == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	result := make([]byte, len(frame))
	copy(result, frame)
	return result, nil
}

var _ hw.Camera = (*V4L2Camera)(nil)
