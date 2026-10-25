// Package appicon 画 dsh-desktop 的图标。运行时（窗口、托盘）和打包时（resources/icon.png）共用，
// 保证两处一模一样。
package appicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// 托盘角标的颜色：右下角一个带白边的圆点。
var (
	BadgeRunning   = color.NRGBA{0x22, 0xc5, 0x5e, 0xff} // 绿：有会话在跑
	BadgeAttention = color.NRGBA{0xf5, 0x9e, 0x0b, 0xff} // 橙：等你审批 / 回答
	BadgeError     = color.NRGBA{0xef, 0x44, 0x44, 0xff} // 红：dsh 没在正常运行
)

// PNG 现画一个 size×size 的图标：DeepSeek 蓝的圆角方块，中间一个白环。省得仓库里放二进制资源。
func PNG(size int) []byte { return encode(draw(size)) }

// Badged 是带右下角状态圆点的 PNG。角标画得比较大：托盘里图标只有 16–24 像素，小了看不出来。
func Badged(size int, badge color.NRGBA) []byte {
	img := draw(size)
	s := float64(size)
	r := s * 0.25 // 圆点半径
	edge := s * 0.07
	cx, cy := s-r-s*0.01, s-r-s*0.01
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			outer := clamp01(r + edge - d + 0.5) // 白边 + 圆点的覆盖度
			if outer == 0 {
				continue
			}
			inner := clamp01(r - d + 0.5) // 圆点本身
			base := img.NRGBAAt(x, y)
			white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
			c := blend(blend(base, white, outer), badge, inner)
			img.SetNRGBA(x, y, c)
		}
	}
	return encode(img)
}

func draw(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	blue := color.NRGBA{0x4d, 0x6b, 0xfe, 0xff}
	s := float64(size)
	radius := s * 0.22
	c := s / 2
	ringOuter, ringInner := s*0.30, s*0.17
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			// 圆角方块：到最近内角点的距离，抗锯齿用 1px 过渡
			dx := math.Max(math.Max(radius-px, px-(s-radius)), 0)
			dy := math.Max(math.Max(radius-py, py-(s-radius)), 0)
			a := clamp01(radius - math.Hypot(dx, dy) + 0.5)
			if a == 0 {
				continue
			}
			col := blue
			d := math.Hypot(px-c, py-c)
			w := clamp01(math.Min(ringOuter-d, d-ringInner) + 0.5) // 白环覆盖度
			col.R = mix(col.R, 0xff, w)
			col.G = mix(col.G, 0xff, w)
			col.B = mix(col.B, 0xff, w)
			col.A = uint8(a * 255)
			img.SetNRGBA(x, y, col)
		}
	}
	return img
}

// blend 把 top 以覆盖度 t 叠到 base 上（base 可以是透明的圆角外侧）。
func blend(base, top color.NRGBA, t float64) color.NRGBA {
	if t <= 0 {
		return base
	}
	ba := float64(base.A) / 255
	a := t + ba*(1-t)
	if a == 0 {
		return color.NRGBA{}
	}
	ch := func(b, f uint8) uint8 {
		return uint8((float64(f)*t+float64(b)*ba*(1-t))/a + 0.5)
	}
	return color.NRGBA{ch(base.R, top.R), ch(base.G, top.G), ch(base.B, top.B), uint8(a*255 + 0.5)}
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func mix(a, b uint8, t float64) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t + 0.5) }
