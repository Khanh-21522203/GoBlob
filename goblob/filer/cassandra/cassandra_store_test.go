package cassandra

import "testing"

func TestParseHosts(t *testing.T) {
	hosts := parseHosts(" 127.0.0.1, cassandra.local , ")
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}
	if hosts[0] != "127.0.0.1" || hosts[1] != "cassandra.local" {
		t.Fatalf("unexpected hosts: %#v", hosts)
	}
}

func TestSanitizeIdentifier(t *testing.T) {
	if got := sanitizeIdentifier("goblob_1"); got != "goblob_1" {
		t.Fatalf("unexpected sanitized identifier: %s", got)
	}
	if got := sanitizeIdentifier("bad-name"); got != "goblob" {
		t.Fatalf("expected fallback for invalid identifier, got %s", got)
	}
}
