package network

import (
	"fmt"
	"net"
)

// Allocator manages subnet allocation for instances.
type Allocator struct {
	baseOffset net.IP
	subnetSize int
}

// NewAllocator creates a subnet allocator.
// baseOffset is the starting IP (e.g., "10.10.0.0") and subnetSize is the prefix length (e.g., 16).
func NewAllocator(baseOffset string, subnetSize int) (*Allocator, error) {
	ip := net.ParseIP(baseOffset)
	if ip == nil {
		return nil, fmt.Errorf("invalid base offset IP: %s", baseOffset)
	}
	ip = ip.To4()
	if ip == nil {
		return nil, fmt.Errorf("base offset must be IPv4: %s", baseOffset)
	}

	return &Allocator{
		baseOffset: ip,
		subnetSize: subnetSize,
	}, nil
}

// SubnetForIndex returns the subnet CIDR for a given index.
// Index 0 → base_offset (e.g., 10.10.0.0/16); index 1 → next /16; etc.
//
// Returns the wrapped CIDR even on overflow (octet > 255). Use SubnetForIndexChecked
// when you want an explicit error on overflow — Manager.Create does, so production
// allocations are bounds-checked. SubnetForIndex remains lenient for backwards
// compatibility with code paths that already validated the index.
func (a *Allocator) SubnetForIndex(index int) string {
	subnet, _ := a.subnetForIndex(index)
	return subnet
}

// SubnetForIndexChecked returns the subnet CIDR for a given index, or an error
// if the index would overflow into another /8 (i.e. wrap past 255 in any octet
// the allocator should be incrementing).
func (a *Allocator) SubnetForIndexChecked(index int) (string, error) {
	return a.subnetForIndex(index)
}

func (a *Allocator) subnetForIndex(index int) (string, error) {
	if index < 0 {
		return "", fmt.Errorf("subnet index must be non-negative, got %d", index)
	}

	ip := make(net.IP, 4)
	copy(ip, a.baseOffset)

	switch {
	case a.subnetSize <= 16:
		// Increment the second octet.
		total := int(ip[1]) + index
		if total > 255 {
			carry := total / 256
			if int(ip[0])+carry > 255 {
				return "", fmt.Errorf("subnet index %d overflows past 255.255 from base %s/%d",
					index, a.baseOffset, a.subnetSize)
			}
			ip[0] = byte(int(ip[0]) + carry)
		}
		ip[1] = byte(total % 256)
	case a.subnetSize <= 24:
		// Increment the third octet.
		total := int(ip[2]) + index
		if total > 255 {
			carry := total / 256
			if int(ip[1])+carry > 255 {
				return "", fmt.Errorf("subnet index %d overflows past 255.255.255 from base %s/%d",
					index, a.baseOffset, a.subnetSize)
			}
			ip[1] = byte(int(ip[1]) + carry)
		}
		ip[2] = byte(total % 256)
	default:
		// Increment the fourth octet.
		total := int(ip[3]) + index
		if total > 255 {
			carry := total / 256
			if int(ip[2])+carry > 255 {
				return "", fmt.Errorf("subnet index %d overflows the fourth octet from base %s/%d",
					index, a.baseOffset, a.subnetSize)
			}
			ip[2] = byte(int(ip[2]) + carry)
		}
		ip[3] = byte(total % 256)
	}

	return fmt.Sprintf("%s/%d", ip.String(), a.subnetSize), nil
}

// MetalLBIPForSubnet returns the MetalLB IP for a subnet.
// Given subnet "10.11.0.0/16" and hostOffset "10.10", returns "10.11.10.10".
func MetalLBIPForSubnet(subnet string, hostOffset string) (string, error) {
	ip, _, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parsing subnet %s: %w", subnet, err)
	}
	ip = ip.To4()
	if ip == nil {
		return "", fmt.Errorf("subnet must be IPv4: %s", subnet)
	}

	offsetIP := net.ParseIP(fmt.Sprintf("0.0.%s", hostOffset))
	if offsetIP == nil {
		var o3, o4 byte
		_, err := fmt.Sscanf(hostOffset, "%d.%d", &o3, &o4)
		if err != nil {
			return "", fmt.Errorf("invalid host offset %s: %w", hostOffset, err)
		}
		ip[2] = o3
		ip[3] = o4
	} else {
		offsetIP = offsetIP.To4()
		ip[2] = offsetIP[2]
		ip[3] = offsetIP[3]
	}

	return ip.String(), nil
}
