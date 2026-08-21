package remoteactionhttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestPublicAddressPolicyRejectsIPv6SpecialPurposeRanges(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{name: "well-known translation", address: "64:ff9b::808:808"},
		{name: "local-use translation", address: "64:ff9b:1::1"},
		{name: "discard only", address: "100::1"},
		{name: "dummy prefix", address: "100:0:0:1::1"},
		{name: "teredo", address: "2001::1"},
		{name: "protocol anycast", address: "2001:1::1"},
		{name: "benchmarking", address: "2001:2::1"},
		{name: "amt", address: "2001:3::1"},
		{name: "as112 v6", address: "2001:4:112::1"},
		{name: "deprecated orchid", address: "2001:10::1"},
		{name: "orchid v2", address: "2001:20::1"},
		{name: "drone remote id", address: "2001:30::1"},
		{name: "documentation legacy", address: "2001:db8::1"},
		{name: "six to four", address: "2002:c000:201::1"},
		{name: "direct delegation as112", address: "2620:4f:8000::1"},
		{name: "documentation current", address: "3fff::1"},
		{name: "segment routing sid", address: "5f00::1"},
		{name: "deprecated site local", address: "fec0::1"},
		{name: "outside global allocation", address: "4000::1"},
		{name: "legacy ipv4 compatible private", address: "::c0a8:101"},
		{name: "ipv4 translatable private", address: "::ffff:0:c0a8:101"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address := netip.MustParseAddr(test.address)
			if isPublicAddress(address) {
				t.Fatalf("isPublicAddress(%s)=true, want false", address)
			}
		})
	}
}

func TestPublicAddressPolicyAllowsOrdinaryPublicAddresses(t *testing.T) {
	tests := []string{
		"1.1.1.1",
		"8.8.8.8",
		"2001:200::1",
		"2001:4860:4860::8888",
		"2606:4700:4700::1111",
		"2a00:1450:4001:81b::200e",
	}

	for _, raw := range tests {
		address := netip.MustParseAddr(raw)
		if !isPublicAddress(address) {
			t.Errorf("isPublicAddress(%s)=false, want true", address)
		}
	}
}

func TestPublicNetworkDialerRejectsIPv6SpecialPurposeBeforeDial(t *testing.T) {
	tests := []string{
		"64:ff9b::808:808",
		"64:ff9b:1::1",
		"100::1",
		"100:0:0:1::1",
		"2001:2::1",
		"2001:20::1",
		"2002:c000:201::1",
		"3fff::1",
		"5f00::1",
		"fec0::1",
		"4000::1",
		"::c0a8:101",
		"::ffff:0:c0a8:101",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			dialCalls := 0
			dialer := &publicNetworkDialer{
				lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP(raw)}}, nil
				},
				dialContext: func(context.Context, string, string) (net.Conn, error) {
					dialCalls++
					return nil, errors.New("unexpected dial")
				},
			}

			_, err := dialer.DialContext(
				context.Background(),
				"tcp",
				"module.example:443",
			)
			if !errors.Is(err, ErrEndpointNotPublic) {
				t.Fatalf("DialContext() error=%v, want %v", err, ErrEndpointNotPublic)
			}
			if dialCalls != 0 {
				t.Fatalf("dial calls=%d, want 0", dialCalls)
			}
		})
	}
}
