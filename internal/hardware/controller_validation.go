package hardware

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var (
	ErrInvalidControllerSettings = errors.New("invalid controller settings")
	ErrDuplicateController       = errors.New("a controller already uses this address and port")
)

const defaultControllerPort int64 = 80

// ValidateControllerSettings trims user-facing fields and rejects addresses that
// could be interpreted as URLs or authorities rather than a host name or IP.
func ValidateControllerSettings(name, address string, port int64) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("%w: controller name is required", ErrInvalidControllerSettings)
	}

	address = strings.TrimSpace(address)
	if err := validateControllerAddress(address); err != nil {
		return "", "", err
	}
	if port < 1 || port > 65535 {
		return "", "", fmt.Errorf("%w: port must be an integer from 1 to 65535", ErrInvalidControllerSettings)
	}
	return name, address, nil
}

func validateControllerAddress(address string) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s", ErrInvalidControllerSettings, reason)
	}
	if address == "" {
		return invalid("controller address is required")
	}
	if strings.ContainsAny(address, "/\\@?#") || strings.Contains(address, "://") {
		return invalid("enter only an IP address or hostname, without a URL scheme, path, credentials, or port")
	}
	if net.ParseIP(address) != nil {
		return nil
	}
	// Brackets only have meaning for IPv6 literals in URLs, and embedded ports
	// are not accepted in the address field.
	if strings.ContainsAny(address, ":[]") {
		return invalid("enter only an IP address or hostname; put the port in the port field")
	}
	if len(address) > 253 || strings.HasSuffix(address, ".") {
		return invalid("hostname must be a valid DNS hostname without a trailing dot")
	}
	allNumeric := true
	for _, r := range address {
		if (r < '0' || r > '9') && r != '.' {
			allNumeric = false
			break
		}
	}
	if allNumeric {
		return invalid("malformed IP address")
	}
	for _, label := range strings.Split(address, ".") {
		if len(label) == 0 || len(label) > 63 {
			return invalid("hostname must contain DNS labels of 1 to 63 characters")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return invalid("hostname labels cannot start or end with a hyphen")
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return invalid("hostname may contain only letters, digits, hyphens, and dots")
			}
		}
	}
	return nil
}

func controllerEndpointKey(address string, port int64) string {
	host := strings.TrimSpace(address)
	if parsedHost, parsedPort, err := net.SplitHostPort(host); err == nil {
		if embeddedPort, parseErr := strconv.ParseInt(parsedPort, 10, 64); parseErr == nil && embeddedPort > 0 && embeddedPort <= 65535 {
			host, port = parsedHost, embeddedPort
		}
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if parsed := net.ParseIP(host); parsed != nil {
		host = parsed.String()
	} else {
		host = strings.ToLower(host)
	}
	return net.JoinHostPort(host, strconv.FormatInt(port, 10))
}

func controllerAddressKey(address string) string {
	host := strings.TrimSpace(address)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.String()
	}
	return strings.ToLower(host)
}
