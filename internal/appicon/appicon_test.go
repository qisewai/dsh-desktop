package appicon

import (
	"bytes"
	"image/png"
	"testing"
)

func TestBadged(t *testing.T) {
	plain, err := png.Decode(bytes.NewReader(PNG(64)))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(Badged(64, BadgeAttention)))
	if err != nil {
		t.Fatal(err)
	}
	// 圆点中心是角标色，左上角和原图一样
	cx := 64 - 16 - 1
	if r, g, b, _ := img.At(cx, cx).RGBA(); r>>8 != 0xf5 || g>>8 != 0x9e || b>>8 != 0x0b {
		t.Errorf("badge center = %x %x %x", r>>8, g>>8, b>>8)
	}
	if img.At(20, 20) != plain.At(20, 20) {
		t.Errorf("top-left changed")
	}
}
