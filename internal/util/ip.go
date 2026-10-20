package util

import (
	"net/netip"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func ExtractIP(ctx *fiber.Ctx) string {
	ip := ctx.IP()
	splitted := strings.SplitN(ip, ",", 2)
	if len(splitted) > 0 {
		return strings.TrimSpace(splitted[0])
	} else {
		return ""
	}
}

// TruncateIP keeps only the network part of ip, the /24 of an IPv4 address or
// the /48 of an IPv6 address, so that logs do not identify a single client.
// The result is still an address (e.g. "203.0.113.0") so that log pipelines can
// geolocate it. It returns "" when ip is not an IP address.
func TruncateIP(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return prefix.Addr().String()
}
