package covers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"testing"
)

func TestPlaceholdersAreNotCovers(t *testing.T) {
	encode := func(shade uint8) []byte {
		img := image.NewGray(image.Rect(0, 0, 100, 150))
		for i := range img.Pix {
			img.Pix[i] = shade
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	blank, real := encode(200), encode(50)
	sum := sha256.Sum256(blank)
	key := hex.EncodeToString(sum[:])
	placeholders[key] = true
	defer delete(placeholders, key)

	if usable(blank) {
		t.Error("the image of a website with no cover was taken for a cover")
	}
	if !usable(real) {
		t.Error("an image that is not a placeholder was refused")
	}
}
