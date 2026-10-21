//go:build linux || darwin

package middlewares

import (
	"net"
	"syscall"
)

// peerClosed reports whether the other end of conn has already closed it (sent
// a FIN or a RST), by peeking at the socket without consuming anything: a peek
// that returns no bytes and no error is the end of the stream. A connection
// that cannot be inspected, such as the in-memory one of app.Test, or one with
// unread bytes before its end (a pipelined request), reports false.
//
// fasthttp never reads the connection while the handler runs, so a closed
// connection stays visible here until the response is written. Behind
// ingress-nginx the peer is the ingress, which closes the upstream connection
// when the client gives up (499) or when its own upstream timeout fires (504).
// A client that only shuts down its writing side but still waits for the
// response is reported as closed too; ingress-nginx never does that.
func peerClosed(conn net.Conn) bool {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return false
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return false
	}
	closed := false
	var b [1]byte
	// Control rather than Read: Read waits for data, and fails once the read
	// deadline fasthttp set for reading the request has passed.
	err = rc.Control(func(fd uintptr) {
		n, _, err := syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		closed = (err == nil && n == 0) || err == syscall.ECONNRESET
	})
	return err == nil && closed
}
