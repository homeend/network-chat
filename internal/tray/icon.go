package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"runtime"
)

// iconPNG draws the netchat icon (blue rounded square, white speech bubble).
func iconPNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	blue := color.NRGBA{0x2f, 0x6f, 0xde, 0xff}
	white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
	s := float64(size) / 32 // design grid is 32x32
	in := func(x, y float64, x0, y0, x1, y1, r float64) bool {
		x0, y0, x1, y1, r = x0*s, y0*s, x1*s, y1*s, r*s
		if x < x0 || x > x1 || y < y0 || y > y1 {
			return false
		}
		cx := min(max(x, x0+r), x1-r)
		cy := min(max(y, y0+r), y1-r)
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
	}
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			x, y := float64(px)+.5, float64(py)+.5
			switch {
			case in(x, y, 6, 8, 26, 21, 2.5): // bubble
				img.SetNRGBA(px, py, white)
			case x >= 10*s && x <= 15*s && y >= 20*s && y <= 26*s && (y-20*s) <= (15*s-x)*1.2: // tail
				img.SetNRGBA(px, py, white)
			case in(x, y, 0, 0, 32, 32, 7):
				img.SetNRGBA(px, py, blue)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// iconBytes returns ICO (Windows) or PNG (elsewhere).
func iconBytes() []byte {
	if runtime.GOOS != "windows" {
		return iconPNG(64)
	}
	p := iconPNG(32)
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	w(uint16(0)) // reserved
	w(uint16(1)) // type: icon
	w(uint16(1)) // count
	w([4]uint8{32, 32, 0, 0})
	w(uint16(1))      // planes
	w(uint16(32))     // bpp
	w(uint32(len(p))) // size
	w(uint32(6 + 16)) // offset
	b.Write(p)        // PNG-in-ICO (Vista+)
	return b.Bytes()
}
