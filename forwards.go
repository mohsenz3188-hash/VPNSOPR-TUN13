package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type Forward struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Listen string `json:"listen"`
	Target string `json:"target"`
	NodeID string `json:"node_id"`
}

type ForwardManager struct {
	mu        sync.RWMutex
	items     map[string]Forward
	listeners map[string]net.Listener
	session   func(string) *Session
	statePath string
	accepted  atomic.Uint64
}

func NewForwardManager(statePath string, session func(string) *Session) (*ForwardManager, error) {
	m := &ForwardManager{items: map[string]Forward{}, listeners: map[string]net.Listener{}, session: session, statePath: statePath}
	if statePath != "" {
		data, err := os.ReadFile(statePath)
		if err == nil {
			var saved []Forward
			if err := json.Unmarshal(data, &saved); err != nil {
				return nil, fmt.Errorf("decode forward state: %w", err)
			}
			for _, f := range saved {
				if err := m.add(f, false); err != nil {
					return nil, fmt.Errorf("restore forward %s: %w", f.Name, err)
				}
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return m, nil
}

func (m *ForwardManager) Add(f Forward) (Forward, error) {
	if f.ID == "" {
		f.ID = randomID()
	}
	if strings.TrimSpace(f.Name) == "" || len(f.Name) > 64 {
		return Forward{}, errors.New("name must be 1-64 characters")
	}
	if f.NodeID == "" {
		return Forward{}, errors.New("node_id is required")
	}
	if _, _, err := net.SplitHostPort(f.Listen); err != nil {
		return Forward{}, fmt.Errorf("invalid listen address: %w", err)
	}
	if _, _, err := net.SplitHostPort(f.Target); err != nil {
		return Forward{}, fmt.Errorf("invalid target address: %w", err)
	}
	if err := m.add(f, true); err != nil {
		return Forward{}, err
	}
	return f, nil
}

func (m *ForwardManager) add(f Forward, persist bool) error {
	ln, err := net.Listen("tcp", f.Listen)
	if err != nil {
		return err
	}
	m.mu.Lock()
	for _, old := range m.items {
		if old.Listen == f.Listen {
			m.mu.Unlock()
			_ = ln.Close()
			return fmt.Errorf("listen address %s is already configured", f.Listen)
		}
	}
	m.items[f.ID] = f
	m.listeners[f.ID] = ln
	m.mu.Unlock()
	go m.acceptLoop(f.ID, ln, f.Target)
	if persist {
		return m.save()
	}
	return nil
}

func (m *ForwardManager) acceptLoop(id string, ln net.Listener, target string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		m.accepted.Add(1)
		go func(c net.Conn) {
			sess := m.session(func() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.items[id].NodeID }())
			if sess == nil {
				_ = c.Close()
				return
			}
			st, err := sess.OpenTarget(target)
			if err != nil {
				_ = c.Close()
				return
			}
			Proxy(c, st)
		}(conn)
	}
}

func (m *ForwardManager) nodeID(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.items[id].NodeID
}

func (m *ForwardManager) Delete(id string) error {
	m.mu.Lock()
	ln, ok := m.listeners[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("tunnel not found")
	}
	delete(m.listeners, id)
	delete(m.items, id)
	m.mu.Unlock()
	_ = ln.Close()
	return m.save()
}

func (m *ForwardManager) List() []Forward {
	m.mu.RLock()
	out := make([]Forward, 0, len(m.items))
	for _, f := range m.items {
		out = append(out, f)
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *ForwardManager) Accepted() uint64 { return m.accepted.Load() }

func (m *ForwardManager) save() error {
	if m.statePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.List(), "", "  ")
	if err != nil {
		return err
	}
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, m.statePath)
}

func (m *ForwardManager) Close() {
	m.mu.Lock()
	for id, ln := range m.listeners {
		_ = ln.Close()
		delete(m.listeners, id)
	}
	m.mu.Unlock()
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback-%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}
