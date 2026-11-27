package media

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
)

var ErrInvalidMP3 = errors.New("unsupported or damaged MP3")

// ValidateMP3 checks the framing of a completed staging file without decoding its contents.
func ValidateMP3(path string, size int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return validateMP3(file, size)
}

func validateMP3(input io.Reader, size int64) error {
	if size < 4 || size > MaxSize {
		return ErrInvalidMP3
	}
	r := bufio.NewReader(input)
	prefix, err := r.Peek(3)
	if err != nil {
		return err
	}
	if string(prefix) == "ID3" {
		used, err := skipID3(r, size)
		if err != nil {
			return err
		}
		size -= used
	}

	frames := 0
	var firstVersion, firstRate byte
	for size >= 4 {
		header, err := r.Peek(4)
		if err != nil {
			return err
		}
		if size == 128 && frames > 0 && string(header[:3]) == "TAG" {
			_, err := r.Discard(128)
			return err
		}
		version, rate := header[1]>>3&3, header[2]>>2&3
		length := frameLength(header)
		if length == 0 || int64(length) > size {
			return ErrInvalidMP3
		}
		if frames == 0 {
			firstVersion, firstRate = version, rate
		} else if version != firstVersion || rate != firstRate {
			return ErrInvalidMP3
		}
		if _, err := r.Discard(length); err != nil {
			return err
		}
		frames++
		size -= int64(length)
	}
	if frames == 0 || size != 0 {
		return ErrInvalidMP3
	}
	return nil
}

func skipID3(r *bufio.Reader, available int64) (int64, error) {
	var header [10]byte
	if available < int64(len(header)) {
		return 0, ErrInvalidMP3
	}
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, err
	}
	version, flags := header[3], header[5]
	if version < 2 || version > 4 || header[4] == 0xff || flags&(0xff>>version) != 0 {
		return 0, ErrInvalidMP3
	}
	body := 0
	for _, b := range header[6:] {
		if b >= 128 {
			return 0, ErrInvalidMP3
		}
		body = body<<7 | int(b)
	}
	hasFooter := version == 4 && flags&0x10 != 0
	total := int64(10 + body)
	if hasFooter {
		total += 10
	}
	if total > available {
		return 0, ErrInvalidMP3
	}
	if _, err := r.Discard(body); err != nil {
		return 0, err
	}
	if hasFooter {
		var footer [10]byte
		if _, err := io.ReadFull(r, footer[:]); err != nil {
			return 0, err
		}
		if string(footer[:3]) != "3DI" || !bytes.Equal(footer[3:], header[3:]) {
			return 0, ErrInvalidMP3
		}
	}
	return total, nil
}

func frameLength(h []byte) int {
	version, layer := h[1]>>3&3, h[1]>>1&3
	bitrateIndex, rateIndex := h[2]>>4, h[2]>>2&3
	if h[0] != 0xff || h[1]&0xe0 != 0xe0 || version == 1 || layer != 1 ||
		bitrateIndex == 0 || bitrateIndex == 15 || rateIndex == 3 || h[3]&3 == 2 {
		return 0
	}
	rate := [...]int{44100, 48000, 32000}[rateIndex]
	bitrates := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	scale, side := 144000, 32
	mono := h[3]>>6 == 3
	if mono {
		side = 17
	}
	if version != 3 {
		bitrates = [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		scale, side = 72000, 17
		if mono {
			side = 9
		}
		rate /= 2
		if version == 0 {
			rate /= 2
		}
	}
	length := scale*bitrates[bitrateIndex]/rate + int(h[2]>>1&1)
	minimum := 4 + side
	if h[1]&1 == 0 {
		minimum += 2
	}
	if length < minimum {
		return 0
	}
	return length
}
