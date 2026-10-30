package tunnel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

type Node struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
	tokenHash string
}

type NodeRegistry struct {
	mu       sync.RWMutex
	nodes    map[string]*Node
	sessions map[string]*Session
	path     string
}

func NewNodeRegistry(path string) (*NodeRegistry, error) {
	r := &NodeRegistry{nodes: map[string]*Node{}, sessions: map[string]*Session{}, path: path}
	if path != "" {
		b, e := os.ReadFile(path)
		if e == nil {
			var rows []struct {
				Node Node   `json:"node"`
				Hash string `json:"token_hash"`
			}
			if e = json.Unmarshal(b, &rows); e != nil {
				return nil, e
			}
			for _, row := range rows {
				n := row.Node
				n.Connected = false
				n.tokenHash = row.Hash
				r.nodes[n.ID] = &n
			}
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	return r, nil
}
func (r *NodeRegistry) Create(name string) (Node, string, error) {
	if name == "" || len(name) > 64 {
		return Node{}, "", errors.New("node name must be 1-64 characters")
	}
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return Node{}, "", e
	}
	var idraw [8]byte
	if _, e := rand.Read(idraw[:]); e != nil {
		return Node{}, "", e
	}
	id := hex.EncodeToString(idraw[:])
	token := hex.EncodeToString(raw[:])
	n := &Node{ID: id, Name: name, CreatedAt: time.Now().UTC(), tokenHash: hashToken(token)}
	r.mu.Lock()
	if len(r.nodes) >= 10 {
		r.mu.Unlock()
		return Node{}, "", errors.New("maximum of 10 registered VPS nodes reached")
	}
	r.nodes[id] = n
	r.mu.Unlock()
	if e := r.save(); e != nil {
		r.mu.Lock()
		delete(r.nodes, id)
		r.mu.Unlock()
		return Node{}, "", e
	}
	return *n, token, nil
}

func (r *NodeRegistry) RotateToken(id string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	r.mu.Lock()
	n := r.nodes[id]
	if n == nil {
		r.mu.Unlock()
		return "", errors.New("node not found")
	}
	n.tokenHash = hashToken(token)
	err := r.saveLocked()
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	return token, nil
}

func hashToken(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (r *NodeRegistry) Authenticate(id, token string) bool {
	r.mu.RLock()
	n := r.nodes[id]
	if n == nil {
		r.mu.RUnlock()
		return false
	}
	expected := n.tokenHash
	r.mu.RUnlock()
	return expected != "" && subtleCompare(expected, hashToken(token))
}
func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
func (r *NodeRegistry) Attach(id string, s *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.nodes[id]
	if n == nil {
		return errors.New("unknown node")
	}
	if old := r.sessions[id]; old != nil {
		select {
		case <-old.Done():
		default:
			return errors.New("node already connected")
		}
	}
	r.sessions[id] = s
	n.Connected = true
	n.LastSeen = time.Now().UTC()
	return r.saveLocked()
}
func (r *NodeRegistry) Detach(id string, s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions[id] == s {
		delete(r.sessions, id)
		if n := r.nodes[id]; n != nil {
			n.Connected = false
			n.LastSeen = time.Now().UTC()
		}
		_ = r.saveLocked()
	}
}
func (r *NodeRegistry) Session(id string) *Session {
	r.mu.RLock()
	s := r.sessions[id]
	r.mu.RUnlock()
	if s != nil {
		select {
		case <-s.Done():
			return nil
		default:
			return s
		}
	}
	return nil
}
func (r *NodeRegistry) List() []Node {
	r.mu.RLock()
	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, *n)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *NodeRegistry) Delete(id string) error {
	r.mu.Lock()
	if s := r.sessions[id]; s != nil {
		select {
		case <-s.Done():
		default:
			r.mu.Unlock()
			return errors.New("disconnect node before deleting it")
		}
	}
	if r.nodes[id] == nil {
		r.mu.Unlock()
		return errors.New("node not found")
	}
	delete(r.nodes, id)
	delete(r.sessions, id)
	e := r.saveLocked()
	r.mu.Unlock()
	return e
}
func (r *NodeRegistry) save() error { r.mu.Lock(); defer r.mu.Unlock(); return r.saveLocked() }
func (r *NodeRegistry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	type row struct {
		Node Node   `json:"node"`
		Hash string `json:"token_hash"`
	}
	rows := make([]row, 0, len(r.nodes))
	for _, n := range r.nodes {
		copy := *n
		copy.tokenHash = ""
		rows = append(rows, row{copy, n.tokenHash})
	}
	b, e := json.MarshalIndent(rows, "", "  ")
	if e != nil {
		return e
	}
	tmp := r.path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	if e = os.Rename(tmp, r.path); e != nil {
		return fmt.Errorf("persist nodes: %w", e)
	}
	return nil
}
