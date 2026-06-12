package real

import (
	"sync"

	"periph.io/x/conn/v3/i2c"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/host/v3"
)

// Bus is a mutex-guarded shared I2C bus.
type Bus struct {
	mu  sync.Mutex
	bus i2c.BusCloser
}

func OpenBus() (*Bus, error) {
	if _, err := host.Init(); err != nil {
		return nil, err
	}
	b, err := i2creg.Open("/dev/i2c-1")
	if err != nil {
		return nil, err
	}
	return &Bus{bus: b}, nil
}

// Tx performs a serialized transaction against addr.
func (b *Bus) Tx(addr uint16, w, r []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bus.Tx(addr, w, r)
}

func (b *Bus) Close() error { return b.bus.Close() }
