package imdsserver

import (
	"log/slog"
	"net"
	"time"
)

// isAllowedIP reports whether ip is contained in any of nets.
func isAllowedIP(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// excludeSubnet returns a copy of nets without the entry that contains ip.
// If ip is nil or no subnet contains ip, nets is returned unchanged.
func excludeSubnet(nets []*net.IPNet, ip net.IP) []*net.IPNet {
	if ip == nil {
		return nets
	}
	for _, n := range nets {
		if n.Contains(ip) {
			result := make([]*net.IPNet, 0, len(nets)-1)
			for _, m := range nets {
				if m != n {
					result = append(result, m)
				}
			}
			return result
		}
	}
	return nets
}

// defaultRouteIP returns the local IP used for outbound traffic by performing
// a UDP "connect" (no packets are sent — this is a local routing table lookup).
func defaultRouteIP() (net.IP, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP, nil
}

// buildAllowList enumerates host network interface subnets, then removes the
// subnet used for outbound internet traffic (the default-route interface).
// If the default-route lookup fails, all subnets are returned (fail-open for
// this step; the listener itself remains fail-closed on enumeration errors).
func buildAllowList() ([]*net.IPNet, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	nets := make([]*net.IPNet, 0, len(addrs))
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			nets = append(nets, ipnet)
		}
	}

	outbound, err := defaultRouteIP()
	if err != nil {
		// No default route — allow all local subnets.
		return nets, nil
	}

	return excludeSubnet(nets, outbound), nil
}

// filteredListener wraps a net.Listener to reject connections from IPs not in
// the cached allow-list. Rejected connections are closed before any HTTP data
// is exchanged.
type filteredListener struct {
	net.Listener
	allowList *Cached[[]*net.IPNet]
	logger    *slog.Logger
}

func newFilteredListener(ln net.Listener, logger *slog.Logger) *filteredListener {
	return &filteredListener{
		Listener:  ln,
		allowList: NewCached[[]*net.IPNet](time.Minute, buildAllowList),
		logger:    logger,
	}
}

func (fl *filteredListener) Accept() (net.Conn, error) {
	for {
		conn, err := fl.Listener.Accept()
		if err != nil {
			return nil, err
		}

		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil {
			fl.logger.Warn("connection filter: unparseable remote address",
				"addr", conn.RemoteAddr())
			conn.Close()
			continue
		}

		ip := net.ParseIP(host)
		if ip == nil {
			fl.logger.Warn("connection filter: unparseable remote IP", "addr", host)
			conn.Close()
			continue
		}

		nets, err := fl.allowList.Get()
		if err != nil {
			fl.logger.Error("connection filter: interface enumeration failed; rejecting connection",
				"remote", host, "error", err)
			conn.Close()
			continue
		}

		if !isAllowedIP(ip, nets) {
			fl.logger.Warn("connection filter: rejected non-local connection", "remote", host)
			conn.Close()
			continue
		}

		return conn, nil
	}
}
