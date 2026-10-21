//go:build !linux && !darwin

package middlewares

import "net"

// peerClosed is only implemented on linux and darwin; elsewhere a closed
// connection is never reported.
func peerClosed(conn net.Conn) bool {
	return false
}
