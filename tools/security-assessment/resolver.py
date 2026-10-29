"""Fixed endpoint resolver for the native MCP process; contains no target code."""

import ipaddress
import runpy
import socket
import sys


def endpoint_resolver(hostname, endpoint_port, address, original):
    numeric = str(ipaddress.ip_address(address))
    if not isinstance(hostname, str) or not hostname or not 1 <= endpoint_port <= 65535:
        raise ValueError("invalid fixed endpoint identity")

    def resolve(host, port, family=0, type=0, proto=0, flags=0):
        # The original resolver sees only the controller-supplied numeric IP.
        # No second destination, DNS lookup, URL rewrite or TLS-name rewrite is
        # available through this mapping.
        valid_port = (isinstance(port, int) and not isinstance(port, bool) and port == endpoint_port)
        valid_port = valid_port or (isinstance(port, str) and port == str(endpoint_port))
        if host != hostname or not valid_port:
            raise socket.gaierror(socket.EAI_NONAME, "destination outside fixed endpoint")
        return original(numeric, port, family=family, type=type, proto=proto,
                        flags=flags | socket.AI_NUMERICHOST)

    return resolve


if __name__ == "__main__":
    if len(sys.argv) != 4:
        raise SystemExit("fixed resolver requires endpoint hostname, port and numeric address")
    hostname, port, address = sys.argv[1:]
    socket.getaddrinfo = endpoint_resolver(hostname, int(port), address, socket.getaddrinfo)
    sys.argv = ["opencyber.server"]
    runpy.run_module("opencyber.server", run_name="__main__")
