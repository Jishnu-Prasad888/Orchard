package thumbs

import (
	"bufio"
	"encoding/binary"
	"io"
)

// exifOrientation reads the EXIF Orientation tag (1-8) from a JPEG stream,
// or returns 1 (upright) when there is none or it cannot be read. Phones
// store photos sideways and record which way is up only here, so ignoring
// it would show half of a phone's portrait shots on their side.
func exifOrientation(r io.Reader) int {
	br := bufio.NewReader(r)
	var soi [2]byte
	if _, err := io.ReadFull(br, soi[:]); err != nil || soi != [2]byte{0xFF, 0xD8} {
		return 1
	}
	for {
		var marker [2]byte
		if _, err := io.ReadFull(br, marker[:]); err != nil || marker[0] != 0xFF {
			return 1
		}
		if marker[1] == 0xDA || marker[1] == 0xD9 { // image data / end: no EXIF
			return 1
		}
		var lenBuf [2]byte
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			return 1
		}
		n := int(binary.BigEndian.Uint16(lenBuf[:])) - 2
		if n < 0 {
			return 1
		}
		if marker[1] != 0xE1 { // not APP1
			if _, err := br.Discard(n); err != nil {
				return 1
			}
			continue
		}
		seg := make([]byte, n)
		if _, err := io.ReadFull(br, seg); err != nil {
			return 1
		}
		if len(seg) < 6 || string(seg[:6]) != "Exif\x00\x00" {
			continue // XMP or another APP1
		}
		return tiffOrientation(seg[6:])
	}
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:8]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	entries := int(order.Uint16(t[ifd : ifd+2]))
	for i := range entries {
		e := ifd + 2 + i*12
		if e+12 > len(t) {
			return 1
		}
		if order.Uint16(t[e:e+2]) == 0x0112 { // Orientation, a SHORT
			if v := int(order.Uint16(t[e+8 : e+10])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
