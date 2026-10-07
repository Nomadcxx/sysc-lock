package art

import (
	"bytes"
	_ "embed"
	"image"
	_ "image/png"
	"sync"
)

// LogoW and LogoH are the intrinsic dimensions of the embedded SYSC wordmark.
const (
	LogoW = 873
	LogoH = 140
)

//go:embed logo.png
var logoPNG []byte

var (
	logoOnce sync.Once
	logoImg  image.Image
)

// Logo returns the SYSC wordmark (transparent background, white glyphs; tint
// it by mixing with its alpha). nil when the embedded PNG cannot decode.
func Logo() image.Image {
	logoOnce.Do(func() {
		if img, _, err := image.Decode(bytes.NewReader(logoPNG)); err == nil {
			logoImg = img
		}
	})
	return logoImg
}
