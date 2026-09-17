// Package thumb makes and caches small preview images.
//
// Everything here is derived data: a thumbnail can be deleted at any moment and
// the only cost is making it again. Nothing in this package may be the only
// copy of anything.
package thumb

import (
	"errors"
	"fmt"
	"image"
	"io"

	// The formats the spec names. Decoders only; the output is always JPEG.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"golang.org/x/image/draw"
	"image/jpeg"
)

// ErrUnsupported is a file this package cannot make a picture of. It is not a
// failure of the request: the file is still listed, downloaded, and shared.
var ErrUnsupported = errors.New("no thumbnail is available for this file")

// Size is the longest edge of a generated thumbnail, in pixels. One size, not a
// parameter: a caller-chosen size is a cache key per caller and a way to ask
// the server to resize a 100-megapixel image 400 times.
const Size = 256

// quality trades a few kilobytes against visible artefacts at this size.
const quality = 80

// Generate decodes an image, turns it the right way up, and returns a JPEG no
// larger than Size on its longest edge, with the aspect ratio preserved.
//
// It takes a ReadSeeker because orientation is read from the file's own header
// before the decoder gets to it.
func Generate(src io.ReadSeeker) ([]byte, error) {
	turn := orientation(src)
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewinding the image: %w", err)
	}

	img, _, err := image.Decode(src)
	if err != nil {
		// Unknown format and corrupt file are the same answer: there is no
		// picture to be had, and the caller records that rather than retrying.
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}

	small := scale(img)
	return encode(rotate(small, turn))
}

// scale reduces an image to fit inside Size, preserving the aspect ratio. An
// image already smaller than that is returned untouched: upscaling a thumbnail
// produces a bigger file that looks worse.
func scale(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= Size && h <= Size {
		return img
	}
	if w > h {
		h = max(h*Size/w, 1)
		w = Size
	} else {
		w = max(w*Size/h, 1)
		h = Size
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// CatmullRom: the output is small and made once, so the sharper resampler is
	// worth the arithmetic.
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

func encode(img image.Image) ([]byte, error) {
	var buf writeBuffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encoding a thumbnail: %w", err)
	}
	return buf, nil
}

// writeBuffer is bytes.Buffer's one useful method. A thumbnail is tens of
// kilobytes, so there is nothing here to stream.
type writeBuffer []byte

func (b *writeBuffer) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}

// rotate applies an EXIF orientation to an already-scaled image. The eight
// orientations are the four rotations, each optionally mirrored.
func rotate(img image.Image, turn int) image.Image {
	if turn <= 1 || turn > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// 5 to 8 turn the image on its side, so the destination swaps its axes.
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if turn >= 5 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := range h {
		for x := range w {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			switch turn {
			case 2: // mirrored
				dst.Set(w-1-x, y, c)
			case 3: // 180°
				dst.Set(w-1-x, h-1-y, c)
			case 4: // mirrored, 180°
				dst.Set(x, h-1-y, c)
			case 5: // mirrored, 90° clockwise
				dst.Set(y, x, c)
			case 6: // 90° clockwise
				dst.Set(h-1-y, x, c)
			case 7: // mirrored, 90° anticlockwise
				dst.Set(h-1-y, w-1-x, c)
			case 8: // 90° anticlockwise
				dst.Set(y, w-1-x, c)
			}
		}
	}
	return dst
}
