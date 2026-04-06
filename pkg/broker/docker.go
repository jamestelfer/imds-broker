package broker

import (
	"context"
	"log/slog"
	"net"
	"strings"
)

const dockerGatewayFormat = `{{(index .IPAM.Config 0).Gateway}}`

// discoverDockerGateway runs docker network inspect bridge and returns the
// bridge gateway IP. Returns an empty string if discovery fails or the result
// is not a valid IP address.
func discoverDockerGateway(ctx context.Context, exec CommandExecutor, logger *slog.Logger) string {
	out, err := exec.Execute(ctx, "docker", "network", "inspect", "bridge", "--format", dockerGatewayFormat)
	if err != nil {
		logger.Info("docker gateway discovery failed", "error", err)
		return ""
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" || net.ParseIP(ip) == nil {
		logger.Info("docker gateway discovery: invalid result", "value", ip)
		return ""
	}
	logger.Info("docker gateway discovered", "ip", ip)
	return ip
}
