package tunnel

import (
	"testing"
)

func TestForwardRequiresNodeAndValidAddresses(t *testing.T) {
	m, err := NewForwardManager("", func(string) *Session { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Add(Forward{Name: "missing-node", Listen: "127.0.0.1:0", Target: "127.0.0.1:80"}); err == nil {
		t.Fatal("expected missing node ID to be rejected")
	}
	if _, err := m.Add(Forward{Name: "bad-address", NodeID: "node", Listen: "bad", Target: "127.0.0.1:80"}); err == nil {
		t.Fatal("expected invalid listen address to be rejected")
	}
}
