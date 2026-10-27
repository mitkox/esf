package route

import "testing"

func TestGuardRejectsSharedOrUnownedAddresses(t *testing.T) {
	owner := "qualification-0123456789abcdef0123456789abcdef"
	for _, g := range []HostGuard{{Owner: owner, IP: "192.168.0.16", Port: 9445}, {Owner: owner, IP: "127.0.0.1", Port: 9445}, {Owner: "other", IP: "192.0.2.50", Port: 9445}, {Owner: owner, IP: "192.0.2.50", Port: 80}} {
		if g.validate() == nil {
			t.Fatalf("accepted unsafe guard scope: %+v", g)
		}
	}
	g := HostGuard{Owner: owner, IP: "192.0.2.50", Port: 9445}
	if err := g.validate(); err != nil {
		t.Fatal(err)
	}
	if len(g.chain()) > 28 || len(g.label()) > 15 {
		t.Fatal("invalid kernel identifier bounds")
	}
}

func TestRecoveryRequiresFullChainOwner(t *testing.T) {
	g := HostGuard{Owner: "qualification-0123456789abcdef0123456789abcdef", IP: "192.0.2.50", Port: 9445}
	for _, rules := range []string{"-N " + g.chain(), "-A " + g.chain() + " -j ACCEPT", "-A " + g.chain() + " -m comment --comment qualification-0123456789abcdef0000000000000000 -j REJECT"} {
		if g.ownsRules(rules) {
			t.Fatal("accepted unowned or ambiguous recovery chain")
		}
	}
	if !g.ownsRules("-A " + g.chain() + " -m comment --comment " + g.Owner + " -j REJECT") {
		t.Fatal("rejected complete owner identity")
	}
}
