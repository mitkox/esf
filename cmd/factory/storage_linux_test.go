//go:build linux

package main

import "testing"

func TestSupportedSQLiteFSType(t *testing.T) {
	for _, fs := range []uint64{0xef53, 0x58465342, 0x9123683e, 0xf2f52010, 0x2fc12fc1} {
		if !supportedSQLiteFSType(fs) {
			t.Fatalf("local filesystem %#x was rejected", fs)
		}
	}
	for _, fs := range []uint64{0x6969, 0xff534d42, 0x00c36400, 0x794c7630, 0x01021994} {
		if supportedSQLiteFSType(fs) {
			t.Fatalf("network or ephemeral filesystem %#x was accepted", fs)
		}
	}
}
