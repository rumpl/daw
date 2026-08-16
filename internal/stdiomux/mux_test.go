package stdiomux

import (
	"context"
	"io"
	"testing"
)

func pair(t *testing.T) (*Mux, *Mux) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a, err := New(ar, aw, Host)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(br, bw, Runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func TestBidirectionalStreams(t *testing.T) {
	a, b := pair(t)
	check := func(dial *Mux, accept *Mux, message string) {
		t.Helper()
		c, err := dial.DialContext(context.Background(), "", "")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		s, err := accept.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		go func() { _, _ = c.Write([]byte(message)); _ = c.Close() }()
		got, err := io.ReadAll(s)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != message {
			t.Fatalf("got %q", got)
		}
	}
	check(a, b, "host to runner")
	check(b, a, "runner to host")
}
func TestServesHTTPOverMux(t *testing.T) {
	a, b := pair(t)
	conn, err := a.DialContext(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	accepted, err := b.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	go func() { buf := make([]byte, 4); _, _ = io.ReadFull(accepted, buf); _, _ = accepted.Write(buf) }()
	_, _ = conn.Write([]byte("ping"))
	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	if err != nil || string(buf) != "ping" {
		t.Fatalf("round trip %q, %v", buf, err)
	}
}
