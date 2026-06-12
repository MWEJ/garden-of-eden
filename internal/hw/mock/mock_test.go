package mock

import "testing"

func TestLightBrightnessClamp(t *testing.T) {
	l := &Light{}
	if err := l.SetBrightness(70); err != nil {
		t.Fatal(err)
	}
	if l.Brightness() != 70 {
		t.Errorf("brightness = %d, want 70", l.Brightness())
	}
	if err := l.SetBrightness(150); err == nil {
		t.Error("expected error for out-of-range brightness")
	}
	if err := l.SetBrightness(-1); err == nil {
		t.Error("expected error for negative brightness")
	}
	if err := l.Off(); err != nil {
		t.Fatal(err)
	}
	if l.Brightness() != 0 {
		t.Errorf("after Off brightness = %d, want 0", l.Brightness())
	}
}

func TestPumpSpeed(t *testing.T) {
	p := &Pump{}
	if err := p.SetSpeed(40); err != nil {
		t.Fatal(err)
	}
	if p.Speed() != 40 {
		t.Errorf("speed = %d, want 40", p.Speed())
	}
	if err := p.SetSpeed(150); err == nil {
		t.Error("expected error for out-of-range speed")
	}
	if err := p.SetSpeed(-1); err == nil {
		t.Error("expected error for negative speed")
	}
	if err := p.Off(); err != nil {
		t.Fatal(err)
	}
	if p.Speed() != 0 {
		t.Errorf("after Off speed = %d, want 0", p.Speed())
	}
}
