package imdsserver

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func TestIsAllowedIP(t *testing.T) {
	localNets := []*net.IPNet{
		mustParseCIDR("127.0.0.0/8"),
		mustParseCIDR("172.17.0.0/16"),
		mustParseCIDR("::1/128"),
	}

	tests := []struct {
		name    string
		ip      string
		allowed bool
	}{
		{"loopback IPv4", "127.0.0.1", true},
		{"docker bridge", "172.17.0.2", true},
		{"loopback IPv6", "::1", true},
		{"public IP", "8.8.8.8", false},
		{"LAN IP not in list", "192.168.1.50", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			assert.Equal(t, tt.allowed, isAllowedIP(ip, localNets))
		})
	}
}

func TestExcludeSubnet(t *testing.T) {
	loopback := mustParseCIDR("127.0.0.0/8")
	bridge := mustParseCIDR("172.17.0.0/16")
	lan := mustParseCIDR("192.168.1.0/24")
	all := []*net.IPNet{loopback, bridge, lan}

	t.Run("removes subnet containing the IP", func(t *testing.T) {
		result := excludeSubnet(all, net.ParseIP("192.168.1.1"))
		assert.Len(t, result, 2)
		assert.Contains(t, result, loopback)
		assert.Contains(t, result, bridge)
		assert.NotContains(t, result, lan)
	})

	t.Run("returns all when no subnet matches", func(t *testing.T) {
		result := excludeSubnet(all, net.ParseIP("10.0.0.1"))
		assert.Equal(t, all, result)
	})

	t.Run("returns all when IP is nil", func(t *testing.T) {
		result := excludeSubnet(all, nil)
		assert.Equal(t, all, result)
	})
}
