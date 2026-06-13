package state

import "sync"

// Frames holds the latest JPEG per camera.
type Frames struct {
	mu    sync.RWMutex
	upper []byte
	lower []byte
}

func NewFrames() *Frames { return &Frames{} }

func (f *Frames) SetUpper(b []byte) { f.mu.Lock(); f.upper = b; f.mu.Unlock() }
func (f *Frames) SetLower(b []byte) { f.mu.Lock(); f.lower = b; f.mu.Unlock() }

// Upper returns the latest JPEG bytes for the upper camera. The returned
// slice is shared; callers must not modify it.
func (f *Frames) Upper() []byte { f.mu.RLock(); defer f.mu.RUnlock(); return f.upper }

// Lower returns the latest JPEG bytes for the lower camera. The returned
// slice is shared; callers must not modify it.
func (f *Frames) Lower() []byte { f.mu.RLock(); defer f.mu.RUnlock(); return f.lower }
