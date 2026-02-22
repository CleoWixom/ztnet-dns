package ztnet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
)

// ComputeRFC4193 computes a deterministic ULA for network+node pair.
func ComputeRFC4193(networkID, nodeID string) (net.IP, error) {
	nb, err := hex.DecodeString(networkID)
	if err != nil || len(nb) == 0 {
		return nil, fmt.Errorf("invalid networkID: %w", err)
	}
	node, err := hex.DecodeString(nodeID)
	if err != nil || len(node) == 0 {
		return nil, fmt.Errorf("invalid nodeID: %w", err)
	}
	h := sha256.Sum256(append(nb, node...))
	ip := make(net.IP, 16)
	ip[0] = 0xfd
	copy(ip[1:6], h[:5])
	copy(ip[6:16], node)
	return ip, nil
}

// Compute6plane computes fcXX:XXXX style address for network+node pair.
func Compute6plane(networkID, nodeID string) (net.IP, error) {
	nb, err := hex.DecodeString(networkID)
	if err != nil || len(nb) < 4 {
		return nil, fmt.Errorf("invalid networkID: %w", err)
	}
	node, err := hex.DecodeString(nodeID)
	if err != nil || len(node) < 5 {
		return nil, fmt.Errorf("invalid nodeID: %w", err)
	}
	ip := make(net.IP, 16)
	ip[0] = 0xfc
	ip[1] = nb[0]
	ip[2] = nb[1]
	ip[3] = nb[2]
	ip[4] = nb[3]
	copy(ip[5:10], node[:5])
	return ip, nil
}
