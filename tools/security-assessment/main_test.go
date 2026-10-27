package main

import (
	"os/exec"
	"testing"
)

func TestEndpointMappingRejectsInvalidControllerDestinations(t *testing.T) {
	for _, tc := range []struct {
		target, ip string
		valid      bool
	}{
		{"https://assessment.esf.invalid:9445", "192.0.2.50", true},
		{"https://assessment.esf.invalid", "2001:db8::50", true},
		{"http://assessment.esf.invalid:9445", "192.0.2.50", false},
		{"https://user@assessment.esf.invalid:9445", "192.0.2.50", false},
		{"https://assessment.esf.invalid:0", "192.0.2.50", false},
		{"https://assessment.esf.invalid:65536", "192.0.2.50", false},
		{"https://assessment.esf.invalid:9445", "other.esf.invalid", false},
		{"https://assessment.esf.invalid:9445", "192.0.2.50;echo unsafe", false},
	} {
		host, port, ip, err := endpointMapping(tc.target, tc.ip)
		if (err == nil) != tc.valid {
			t.Fatalf("mapping %q/%q: %v", tc.target, tc.ip, err)
		}
		if tc.valid && (host != "assessment.esf.invalid" || port == "" || ip == "") {
			t.Fatal("mapping lost exact endpoint")
		}
	}
}

func TestFixedResolverRejectsOtherDestinationsAndPreservesSocketOptions(t *testing.T) {
	// This test executes only in Cube alongside the assessment tests. The
	// launcher is loaded without __main__, so no backend or network starts.
	const contract = `
import socket, sys
namespace = {"__name__": "resolver_contract"}
exec(compile(sys.argv[1], "fixed-resolver.py", "exec"), namespace)
calls = []
def original(*args, **kwargs):
    calls.append((args, kwargs))
    return [("numeric-result",)]
make = namespace["endpoint_resolver"]
resolve = make("assessment.esf.invalid", 9445, "192.0.2.50", original)
assert resolve("assessment.esf.invalid", 9445, family=socket.AF_INET, type=socket.SOCK_STREAM, proto=socket.IPPROTO_TCP, flags=socket.AI_CANONNAME) == [("numeric-result",)]
assert calls == [(("192.0.2.50", 9445), {"family": socket.AF_INET, "type": socket.SOCK_STREAM, "proto": socket.IPPROTO_TCP, "flags": socket.AI_CANONNAME | socket.AI_NUMERICHOST})]
for host, port in [("other.esf.invalid", 9445), ("192.0.2.50", 9445), ("assessment.esf.invalid", 443), ("assessment.esf.invalid", "https"), ("assessment.esf.invalid.",9445), (None, 9445)]:
    try:
        resolve(host, port)
    except socket.gaierror:
        pass
    else:
        raise AssertionError("unapproved resolution accepted")
assert len(calls) == 1, "rejected destination caused DNS work"
for address in ["not-an-ip", "192.0.2.50;command", ""]:
    try:
        make("assessment.esf.invalid",9445,address,original)
    except ValueError:
        pass
    else:
        raise AssertionError("invalid numeric endpoint accepted")
assert len(calls) == 1
`
	command := exec.Command("python3", "-I", "-c", contract, resolverLauncher)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("resolver contract: %v: %s", err, output)
	}
}
