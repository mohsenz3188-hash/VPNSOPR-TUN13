package tunnel

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	frameOpen byte = iota + 1
	frameData
	frameClose
	frameOpenOK
	frameOpenErr
	maxFrame = 32 * 1024
)

type frame struct {
	typ  byte
	id   uint32
	data []byte
}

func readFrame(r io.Reader) (frame, error) {
	var h [9]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return frame{}, err
	}
	n := binary.BigEndian.Uint32(h[5:])
	if n > maxFrame {
		return frame{}, errors.New("frame exceeds maximum size")
	}
	f := frame{typ: h[0], id: binary.BigEndian.Uint32(h[1:5])}
	if n > 0 {
		f.data = make([]byte, n)
		if _, err := io.ReadFull(r, f.data); err != nil {
			return frame{}, err
		}
	}
	return f, nil
}

func writeFrame(w io.Writer, f frame) error {
	if len(f.data) > maxFrame {
		return errors.New("frame exceeds maximum size")
	}
	var h [9]byte
	h[0] = f.typ
	binary.BigEndian.PutUint32(h[1:5], f.id)
	binary.BigEndian.PutUint32(h[5:9], uint32(len(f.data)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	if len(f.data) > 0 {
		_, err := w.Write(f.data)
		return err
	}
	return nil
}
