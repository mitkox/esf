//go:build !linux

package factory

import (
	"fmt"
	"net"
)

func qualityPeerUID(net.Conn) (uint32, error) {
	return 0, fmt.Errorf("quality peer authentication requires Linux")
}

func qualityCheckDirectory(string) error {
	return fmt.Errorf("controlled quality service requires Linux peer credentials")
}
