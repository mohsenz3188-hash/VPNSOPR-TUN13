package tunnel

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestNodeCreateAuthenticateAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	r, err := NewNodeRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	node, secret, err := r.Create("remote-vps")
	if err != nil {
		t.Fatal(err)
	}
	if node.ID == "" || len(secret) != 64 {
		t.Fatalf("unexpected node credentials: %#v", node)
	}
	if !r.Authenticate(node.ID, secret) {
		t.Fatal("valid token rejected")
	}
	if r.Authenticate(node.ID, secret+"x") {
		t.Fatal("invalid token accepted")
	}

	reloaded, err := NewNodeRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Authenticate(node.ID, secret) {
		t.Fatal("persisted token hash did not authenticate")
	}
	if got := reloaded.List(); len(got) != 1 || got[0].Name != "remote-vps" {
		t.Fatalf("unexpected persisted nodes: %#v", got)
	}
}

func TestNodeDeleteAndUnknownNode(t *testing.T) {
	r, err := NewNodeRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := r.Create("test")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(n.ID); err != nil {
		t.Fatal(err)
	}
	if r.Authenticate(n.ID, "anything") {
		t.Fatal("deleted node still authenticates")
	}
}

func TestNodeTokenRotation(t *testing.T) {
	r, err := NewNodeRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	n, oldToken, err := r.Create("rotate-me")
	if err != nil {
		t.Fatal(err)
	}
	newToken, err := r.RotateToken(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Authenticate(n.ID, oldToken) {
		t.Fatal("old token remained valid after rotation")
	}
	if !r.Authenticate(n.ID, newToken) {
		t.Fatal("new token rejected")
	}
}

func TestNodeLimitTen(t *testing.T) {
	r, err := NewNodeRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, _, err := r.Create(fmt.Sprintf("node-%d", i)); err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
	}
	if _, _, err := r.Create("eleventh"); err == nil {
		t.Fatal("expected 10-node limit")
	}
}
