package real

import "testing"

func TestParseRes(t *testing.T) {
	w, h, err := parseRes("640x480")
	if err != nil || w != 640 || h != 480 {
		t.Errorf("parseRes(640x480) = %d, %d, %v", w, h, err)
	}
	if _, _, err := parseRes("bad"); err == nil {
		t.Error("expected error for 'bad'")
	}
	if _, _, err := parseRes("640xfoo"); err == nil {
		t.Error("expected error for '640xfoo'")
	}
	if _, _, err := parseRes("0x480"); err == nil {
		t.Error("expected error for '0x480'")
	}
}
