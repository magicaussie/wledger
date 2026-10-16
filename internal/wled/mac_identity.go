package wled

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// NormalizeControllerMAC accepts WLED's compact JSON identity and standard MAC
// notation. Representation differences must not create independent pixel owners.
func NormalizeControllerMAC(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	var mac net.HardwareAddr
	var err error
	if len(raw) == 12 {
		var decoded []byte
		decoded, err = hex.DecodeString(raw)
		mac = net.HardwareAddr(decoded)
	} else {
		mac, err = net.ParseMAC(raw)
	}
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || mac.String() == "00:00:00:00:00:00" {
		return "", fmt.Errorf("invalid controller MAC identity %q", raw)
	}
	return mac.String(), nil
}
