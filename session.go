package tunnel

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

type Session struct {
	conn     net.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	streams  map[uint32]*Stream
	nextID   atomic.Uint32
	done     chan struct{}
	onOpen   func(*Stream)
	closeOne sync.Once
}

type Stream struct {
	id        uint32
	Target    string
	s         *Session
	in        chan []byte
	done      chan struct{}
	ready     chan error
	current   []byte
	closeOnce sync.Once
}

func NewSession(conn net.Conn, onOpen func(*Stream)) *Session {
	s := &Session{conn: conn, streams: make(map[uint32]*Stream), done: make(chan struct{}), onOpen: onOpen}
	go s.readLoop()
	return s
}

func (s *Session) newStream(id uint32) *Stream {
	st := &Stream{id: id, s: s, in: make(chan []byte, 32), done: make(chan struct{}), ready: make(chan error, 1)}
	s.mu.Lock()
	s.streams[id] = st
	s.mu.Unlock()
	return st
}

func (s *Session) Open() (*Stream, error) { return s.OpenTarget("") }

func (s *Session) OpenTarget(target string) (*Stream, error) {
	id := s.nextID.Add(1)
	if id == 0 {
		id = s.nextID.Add(1)
	}
	st := s.newStream(id)
	if err := s.send(frame{typ: frameOpen, id: id, data: []byte(target)}); err != nil {
		s.remove(id)
		return nil, err
	}
	select {
	case err := <-st.ready:
		if err != nil {
			st.finish()
			s.remove(id)
			return nil, err
		}
		return st, nil
	case <-s.done:
		return nil, errors.New("tunnel session closed")
	}
}

func (s *Session) send(f frame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return errors.New("session closed")
	default:
	}
	if err := writeFrame(s.conn, f); err != nil {
		s.shutdown()
		return err
	}
	return nil
}

func (s *Session) get(id uint32) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

func (s *Session) remove(id uint32) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

func (s *Session) readLoop() {
	for {
		f, err := readFrame(s.conn)
		if err != nil {
			s.shutdown()
			return
		}
		switch f.typ {
		case frameOpen:
			st := s.newStream(f.id)
			st.Target = string(f.data)
			if s.onOpen != nil {
				go s.onOpen(st)
			} else {
				_ = st.Reject("opening streams is not supported")
			}
		case frameOpenOK:
			if st := s.get(f.id); st != nil {
				select {
				case st.ready <- nil:
				default:
				}
			}
		case frameOpenErr:
			if st := s.get(f.id); st != nil {
				msg := string(f.data)
				if msg == "" {
					msg = "remote target could not be opened"
				}
				select {
				case st.ready <- errors.New(msg):
				default:
				}
			}
		case frameData:
			if st := s.get(f.id); st != nil {
				select {
				case st.in <- f.data:
				case <-st.done:
				case <-s.done:
				}
			}
		case frameClose:
			if st := s.get(f.id); st != nil {
				st.finish()
				s.remove(f.id)
			}
		default:
			s.shutdown()
			return
		}
	}
}

func (s *Session) shutdown() {
	s.closeOne.Do(func() {
		close(s.done)
		_ = s.conn.Close()
		s.mu.Lock()
		for id, st := range s.streams {
			st.finish()
			delete(s.streams, id)
		}
		s.mu.Unlock()
	})
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (st *Stream) finish() {
	st.closeOnce.Do(func() { close(st.done) })
}

func (st *Stream) Read(p []byte) (int, error) {
	for len(st.current) == 0 {
		select {
		case b := <-st.in:
			st.current = b
		case <-st.done:
			return 0, io.EOF
		case <-st.s.done:
			return 0, io.EOF
		}
	}
	n := copy(p, st.current)
	st.current = st.current[n:]
	return n, nil
}

func (st *Stream) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxFrame {
			n = maxFrame
		}
		chunk := append([]byte(nil), p[:n]...)
		if err := st.s.send(frame{typ: frameData, id: st.id, data: chunk}); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (st *Stream) Close() error {
	st.closeOnce.Do(func() {
		_ = st.s.send(frame{typ: frameClose, id: st.id})
		st.s.remove(st.id)
		close(st.done)
	})
	return nil
}

func (st *Stream) Ready() error {
	return st.s.send(frame{typ: frameOpenOK, id: st.id})
}

func (st *Stream) Reject(message string) error {
	return st.s.send(frame{typ: frameOpenErr, id: st.id, data: []byte(message)})
}

// Proxy copies bytes in both directions and closes both ends when either direction ends.
func Proxy(a io.ReadWriteCloser, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(a, b); _ = a.Close(); _ = b.Close() }()
	go func() { defer wg.Done(); _, _ = io.Copy(b, a); _ = a.Close(); _ = b.Close() }()
	wg.Wait()
}
