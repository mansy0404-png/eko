package flow

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

// DecodeBody transparently decodes common content encodings so the UI shows
// readable text. The second return value reports whether decoding happened.
func DecodeBody(data []byte, encoding string) ([]byte, bool) {
	enc := strings.ToLower(strings.TrimSpace(encoding))
	if enc == "" || enc == "identity" || len(data) == 0 {
		return data, false
	}
	// Content-Encoding can be a list; decode from the outermost inward.
	parts := strings.Split(enc, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		out, ok := decodeOne(data, part)
		if !ok {
			return data, false
		}
		data = out
	}
	return data, true
}

func decodeOne(data []byte, enc string) ([]byte, bool) {
	switch enc {
	case "gzip", "x-gzip":
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		defer r.Close()
		out, err := io.ReadAll(r)
		return out, err == nil
	case "deflate":
		if r, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
			out, rerr := io.ReadAll(r)
			r.Close()
			if rerr == nil {
				return out, true
			}
		}
		r := flate.NewReader(bytes.NewReader(data))
		defer r.Close()
		out, err := io.ReadAll(r)
		return out, err == nil
	case "br":
		out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
		return out, err == nil
	}
	return nil, false
}

// LooksText reports whether data is plausibly human readable, so the UI can
// decide between a text viewer and a binary placeholder.
func LooksText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	bad := 0
	for len(sample) > 0 {
		r, size := decodeRune(sample)
		if r == 0xFFFD && size == 1 {
			return false
		}
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			bad++
		}
		sample = sample[size:]
	}
	return bad*20 < len(data) || len(data) < 32
}

// decodeRune is a minimal UTF-8 decoder to avoid pulling in utf8 for one loop.
func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return 0xFFFD, 1
	}
	c := b[0]
	if c < 0x80 {
		return rune(c), 1
	}
	n := 0
	switch {
	case c&0xE0 == 0xC0:
		n = 2
	case c&0xF0 == 0xE0:
		n = 3
	case c&0xF8 == 0xF0:
		n = 4
	default:
		return 0xFFFD, 1
	}
	if len(b) < n {
		return 0xFFFD, 1
	}
	for i := 1; i < n; i++ {
		if b[i]&0xC0 != 0x80 {
			return 0xFFFD, 1
		}
	}
	var r rune
	switch n {
	case 2:
		r = rune(c&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		r = rune(c&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	case 4:
		r = rune(c&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
	return r, n
}
