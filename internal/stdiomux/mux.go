// Package stdiomux multiplexes full-duplex net.Conn streams over one reader
// and writer. It is deliberately small and transport agnostic; DAW uses it on
// top of the stdin/stdout pipes returned by sbx exec.
package stdiomux

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	frameOpen   byte = 1
	frameData   byte = 2
	frameClose  byte = 3
	frameReset  byte = 4
	maxFrame         = 1 << 20
	maxBuffered      = 64 << 20
)

type Role uint32

const (
	Host   Role = 1
	Runner Role = 2
)

type Mux struct {
	r         io.ReadCloser
	w         io.WriteCloser
	writeMu   sync.Mutex
	mu        sync.Mutex
	streams   map[uint32]*stream
	accept    chan net.Conn
	next      atomic.Uint32
	done      chan struct{}
	closeOnce sync.Once
	err       error
}

type stream struct {
	mux           *Mux
	id            uint32
	mu            sync.Mutex
	buffer        []byte
	wake          chan struct{}
	remoteClosed  bool
	localClosed   bool
	err           error
	readDeadline  time.Time
	writeDeadline time.Time
}

type dummyAddr string

func (a dummyAddr) Network() string { return "stdio" }
func (a dummyAddr) String() string  { return string(a) }

func New(r io.ReadCloser, w io.WriteCloser, role Role) (*Mux, error) {
	if r == nil || w == nil {
		return nil, errors.New("stdio mux requires a reader and writer")
	}
	if role != Host && role != Runner {
		return nil, errors.New("invalid stdio mux role")
	}
	m := &Mux{r: r, w: w, streams: map[uint32]*stream{}, accept: make(chan net.Conn, 64), done: make(chan struct{})}
	m.next.Store(uint32(role) - 2) // Add(2) yields 1 for host, 2 for runner.
	go m.readLoop()
	return m, nil
}

func (m *Mux) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	select {
	case <-m.done:
		return nil, m.closeError()
	default:
	}
	id := m.next.Add(2)
	s := newStream(m, id)
	m.mu.Lock()
	m.streams[id] = s
	m.mu.Unlock()
	if err := m.writeFrame(frameOpen, id, nil); err != nil {
		m.remove(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		_ = s.Close()
		return nil, ctx.Err()
	default:
		return s, nil
	}
}

func (m *Mux) Accept() (net.Conn, error) {
	select {
	case conn := <-m.accept:
		return conn, nil
	case <-m.done:
		return nil, m.closeError()
	}
}
func (m *Mux) Addr() net.Addr { return dummyAddr("stdio-mux") }
func (m *Mux) Close() error {
	m.shutdown(net.ErrClosed)
	return nil
}
func (m *Mux) Done() <-chan struct{} { return m.done }

func newStream(m *Mux, id uint32) *stream {
	return &stream{mux: m, id: id, wake: make(chan struct{}, 1)}
}
func (m *Mux) readLoop() {
	var header [9]byte
	for {
		if _, err := io.ReadFull(m.r, header[:]); err != nil {
			m.shutdown(err)
			return
		}
		typ := header[0]
		id := binary.BigEndian.Uint32(header[1:5])
		size := binary.BigEndian.Uint32(header[5:9])
		if size > maxFrame {
			m.shutdown(fmt.Errorf("stdio mux frame too large: %d", size))
			return
		}
		payload := make([]byte, int(size))
		if _, err := io.ReadFull(m.r, payload); err != nil {
			m.shutdown(err)
			return
		}
		switch typ {
		case frameOpen:
			if size != 0 {
				m.shutdown(errors.New("invalid open frame"))
				return
			}
			s := newStream(m, id)
			m.mu.Lock()
			if m.streams[id] != nil {
				m.mu.Unlock()
				m.shutdown(errors.New("duplicate stream ID"))
				return
			}
			if len(m.streams) >= 256 {
				m.mu.Unlock()
				_ = m.writeFrame(frameReset, id, nil)
				continue
			}
			m.streams[id] = s
			m.mu.Unlock()
			select {
			case m.accept <- s:
			case <-m.done:
				return
			default:
				m.remove(id)
				_ = m.writeFrame(frameReset, id, nil)
			}
		case frameData:
			if s := m.get(id); s != nil {
				s.receive(payload)
			}
		case frameClose:
			if s := m.get(id); s != nil {
				s.remoteClose(nil)
			}
		case frameReset:
			if s := m.get(id); s != nil {
				s.remoteClose(errors.New("stdio stream reset"))
			}
		default:
			m.shutdown(fmt.Errorf("unknown stdio mux frame %d", typ))
			return
		}
	}
}
func (m *Mux) get(id uint32) *stream { m.mu.Lock(); defer m.mu.Unlock(); return m.streams[id] }
func (m *Mux) remove(id uint32)      { m.mu.Lock(); delete(m.streams, id); m.mu.Unlock() }
func (m *Mux) writeFrame(typ byte, id uint32, payload []byte) error {
	select {
	case <-m.done:
		return m.closeError()
	default:
	}
	var header [9]byte
	header[0] = typ
	binary.BigEndian.PutUint32(header[1:5], id)
	binary.BigEndian.PutUint32(header[5:9], uint32(len(payload)))
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if _, err := m.w.Write(header[:]); err != nil {
		m.shutdown(err)
		return err
	}
	if len(payload) > 0 {
		if _, err := m.w.Write(payload); err != nil {
			m.shutdown(err)
			return err
		}
	}
	return nil
}
func (m *Mux) shutdown(err error) {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.err = err
		streams := m.streams
		m.streams = map[uint32]*stream{}
		close(m.done)
		m.mu.Unlock()
		_ = m.r.Close()
		_ = m.w.Close()
		for _, s := range streams {
			s.remoteClose(err)
		}
	})
}
func (m *Mux) closeError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	return net.ErrClosed
}

func (s *stream) receive(data []byte) {
	overflow := false
	s.mu.Lock()
	if !s.remoteClosed && !s.localClosed {
		if len(s.buffer)+len(data) > maxBuffered {
			s.err = errors.New("stdio stream receive buffer exceeded")
			s.remoteClosed = true
			overflow = true
		} else {
			s.buffer = append(s.buffer, data...)
		}
	}
	s.mu.Unlock()
	s.signal()
	if overflow {
		_ = s.mux.writeFrame(frameReset, s.id, nil)
	}
}
func (s *stream) remoteClose(err error) {
	s.mu.Lock()
	s.remoteClosed = true
	if err != nil {
		s.err = err
	}
	local := s.localClosed
	s.mu.Unlock()
	s.signal()
	if local {
		s.mux.remove(s.id)
	}
}
func (s *stream) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *stream) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.buffer) > 0 {
			n := copy(p, s.buffer)
			s.buffer = s.buffer[n:]
			s.mu.Unlock()
			return n, nil
		}
		if s.err != nil {
			e := s.err
			s.mu.Unlock()
			return 0, e
		}
		if s.remoteClosed {
			s.mu.Unlock()
			return 0, io.EOF
		}
		deadline := s.readDeadline
		s.mu.Unlock()
		if deadline.IsZero() {
			select {
			case <-s.wake:
			case <-s.mux.done:
				return 0, s.mux.closeError()
			}
		} else {
			wait := time.Until(deadline)
			if wait <= 0 {
				return 0, osDeadline{}
			}
			timer := time.NewTimer(wait)
			select {
			case <-s.wake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
				return 0, osDeadline{}
			case <-s.mux.done:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return 0, s.mux.closeError()
			}
		}
	}
}
func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	closed := s.localClosed
	deadline := s.writeDeadline
	s.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	if !deadline.IsZero() && time.Now().After(deadline) {
		return 0, osDeadline{}
	}
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxFrame {
			n = maxFrame
		}
		if err := s.mux.writeFrame(frameData, s.id, p[:n]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}
func (s *stream) Close() error {
	s.mu.Lock()
	if s.localClosed {
		s.mu.Unlock()
		return nil
	}
	s.localClosed = true
	remote := s.remoteClosed
	s.mu.Unlock()
	s.signal()
	err := s.mux.writeFrame(frameClose, s.id, nil)
	if remote {
		s.mux.remove(s.id)
	}
	return err
}
func (s *stream) LocalAddr() net.Addr  { return dummyAddr("local") }
func (s *stream) RemoteAddr() net.Addr { return dummyAddr("remote") }
func (s *stream) SetDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline = t
	s.writeDeadline = t
	s.mu.Unlock()
	s.signal()
	return nil
}
func (s *stream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline = t
	s.mu.Unlock()
	s.signal()
	return nil
}
func (s *stream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.writeDeadline = t
	s.mu.Unlock()
	return nil
}

type osDeadline struct{}

func (osDeadline) Error() string   { return "i/o timeout" }
func (osDeadline) Timeout() bool   { return true }
func (osDeadline) Temporary() bool { return true }

var _ net.Listener = (*Mux)(nil)
var _ net.Conn = (*stream)(nil)
