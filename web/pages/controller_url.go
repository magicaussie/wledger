package pages

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/tuxedocurly/wledger/internal/db"
)

func controllerManagementURL(controller db.Controller) string {
	host := strings.TrimSpace(controller.IpAddress)
	port := int64(80)
	if controller.Port.Valid && controller.Port.Int64 >= 1 && controller.Port.Int64 <= 65535 {
		port = controller.Port.Int64
	}
	if parsedHost, parsedPort, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
		if configuredPort, parseErr := strconv.ParseInt(parsedPort, 10, 64); parseErr == nil && configuredPort >= 1 && configuredPort <= 65535 {
			port = configuredPort
		}
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if port < 1 || port > 65535 {
		port = 80
	}
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, strconv.FormatInt(port, 10)),
	}).String()
}
