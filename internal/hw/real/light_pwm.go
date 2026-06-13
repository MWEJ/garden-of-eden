package real

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

const pwmChip = "/sys/class/pwm/pwmchip0"

// pwmLine drives one hardware-PWM channel via sysfs. periodNS is fixed; duty
// is set per brightness/speed.
type pwmLine struct {
	mu       sync.Mutex
	dir      string // e.g. /sys/class/pwm/pwmchip0/pwm0
	periodNS int
	pct      int
}

func newPWMLine(channel, freqHz int) (*pwmLine, error) {
	if _, err := os.Stat(filepath.Join(pwmChip, fmt.Sprintf("pwm%d", channel))); os.IsNotExist(err) {
		if err := os.WriteFile(filepath.Join(pwmChip, "export"), []byte(strconv.Itoa(channel)), 0o644); err != nil {
			return nil, fmt.Errorf("export pwm%d: %w (is dtoverlay=pwm enabled?)", channel, err)
		}
		time.Sleep(50 * time.Millisecond) // udev needs a moment to create the dir
	}
	dir := filepath.Join(pwmChip, fmt.Sprintf("pwm%d", channel))
	periodNS := int(time.Second) / freqHz
	l := &pwmLine{dir: dir, periodNS: periodNS}
	if err := l.write("period", strconv.Itoa(periodNS)); err != nil {
		return nil, fmt.Errorf("set pwm%d period: %w", channel, err)
	}
	if err := l.write("duty_cycle", "0"); err != nil {
		return nil, fmt.Errorf("set pwm%d duty_cycle: %w", channel, err)
	}
	if err := l.write("enable", "1"); err != nil {
		return nil, fmt.Errorf("enable pwm%d: %w", channel, err)
	}
	return l, nil
}

func (l *pwmLine) write(attr, val string) error {
	return os.WriteFile(filepath.Join(l.dir, attr), []byte(val), 0o644)
}

func (l *pwmLine) setPercent(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("duty %d out of range 0..100", pct)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	duty := l.periodNS * pct / 100
	if err := l.write("duty_cycle", strconv.Itoa(duty)); err != nil {
		return err
	}
	l.pct = pct
	return nil
}

func (l *pwmLine) percent() int { l.mu.Lock(); defer l.mu.Unlock(); return l.pct }

// LightPWM implements hw.Light on PWM channel 0 (GPIO18) at 8 kHz.
type LightPWM struct{ line *pwmLine }

func NewLightPWM() (*LightPWM, error) {
	l, err := newPWMLine(0, 8000)
	if err != nil {
		return nil, err
	}
	return &LightPWM{line: l}, nil
}

func (l *LightPWM) SetBrightness(pct int) error { return l.line.setPercent(pct) }
func (l *LightPWM) Brightness() int             { return l.line.percent() }
func (l *LightPWM) Off() error                  { return l.line.setPercent(0) }

var _ hw.Light = (*LightPWM)(nil)
