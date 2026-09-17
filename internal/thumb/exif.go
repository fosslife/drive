package thumb

import (
	"encoding/binary"
	"io"
)

// A phone writes the photograph in the sensor's orientation and records how to
// turn it in one EXIF tag. Ignore the tag and every portrait photo is sideways.
//
// ponytail: this reads that one tag out of the JPEG header by hand rather than
// taking an EXIF library. Orientation is a fixed offset into a well-specified
// structure, the parse is bounded and total — anything it does not understand
// is orientation 1, the identity — and an EXIF library is a large dependency
// with a history of parser bugs for a 2-byte answer. Take one if thumbnails
// ever need the date, the camera, or the GPS position.
func orientation(src io.ReadSeeker) int {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return 1
	}
	// The EXIF segment is at the front of the file by construction; a fixed
	// window keeps a 100 MB image from being read into memory to find a flag.
	head := make([]byte, 64<<10)
	n, _ := io.ReadFull(src, head)
	return exifOrientation(head[:n])
}

// exifOrientation walks a JPEG's segments to the APP1/Exif one and reads tag
// 0x0112 out of IFD0. Every malformed or absent case answers 1.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 { // not a JPEG: no orientation
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1 // out of step with the segment structure
		}
		marker := b[i+1]
		switch {
		case marker == 0xFF: // fill byte
			i++
			continue
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			i += 2 // standalone markers carry no length
			continue
		case marker == 0xDA || marker == 0xD9: // image data starts, or the file ends
			return 1
		}
		length := int(binary.BigEndian.Uint16(b[i+2:]))
		if length < 2 || i+2+length > len(b) {
			return 1
		}
		data := b[i+4 : i+2+length]
		if marker == 0xE1 && len(data) > 6 && string(data[:6]) == "Exif\x00\x00" {
			return tiffOrientation(data[6:])
		}
		i += 2 + length
	}
	return 1
}

// tiffOrientation reads IFD0 of the TIFF block an EXIF segment carries.
func tiffOrientation(b []byte) int {
	if len(b) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(b[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(b[2:]) != 42 { // the TIFF magic number, in the stated order
		return 1
	}

	ifd := int(order.Uint32(b[4:]))
	if ifd < 8 || ifd+2 > len(b) {
		return 1
	}
	count := int(order.Uint16(b[ifd:]))
	for e := range count {
		// Each entry is tag(2) type(2) count(4) value(4), and a SHORT value sits
		// in the first two bytes of its own field rather than at an offset.
		at := ifd + 2 + e*12
		if at+12 > len(b) {
			return 1
		}
		if order.Uint16(b[at:]) != 0x0112 { // Orientation
			continue
		}
		if turn := int(order.Uint16(b[at+8:])); turn >= 1 && turn <= 8 {
			return turn
		}
		return 1
	}
	return 1
}
