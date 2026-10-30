package main

import (
	"crypto/tls"
	"encoding/binary"
	"flag"
	"io"
	"log"
	"net"
	"time"

	"github.com/OWNER/VPNSOPR/internal/panel"
	"github.com/OWNER/VPNSOPR/internal/tunnel"
)

func main() {
	control := flag.String("listen", "0.0.0.0:8443", "TLS agent listener")
	cert := flag.String("cert", "server.crt", "TLS certificate")
	key := flag.String("key", "server.key", "TLS private key")
	panelAddr := flag.String("panel", "127.0.0.1:7777", "management panel bind address")
	panelToken := flag.String("panel-token", "", "management API token (min 24 chars)")
	state := flag.String("state", "vpsopr-forwards.json", "forward config file")
	nodeState := flag.String("nodes", "vpsopr-nodes.json", "registered VPS nodes file")
	flag.Parse()
	if len(*panelToken) < 24 {
		log.Fatal("panel-token must be at least 24 characters")
	}
	certificate, err := tls.LoadX509KeyPair(*cert, *key)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := tls.Listen("tcp", *control, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13})
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	nodes, err := tunnel.NewNodeRegistry(*nodeState)
	if err != nil {
		log.Fatal(err)
	}
	var manager *tunnel.ForwardManager
	manager, err = tunnel.NewForwardManager(*state, func(id string) *tunnel.Session { return nodes.Session(id) })
	if err != nil {
		log.Fatal(err)
	}
	defer manager.Close()
	started := time.Now()
	ps := panel.Server(*panelAddr, *panelToken, func() any {
		return map[string]any{"service": "VPNSOPR", "version": "0.5.0", "registered_nodes": len(nodes.List()), "connected_nodes": connectedCount(nodes.List()), "tunnels": len(manager.List()), "accepted_connections": manager.Accepted(), "uptime": time.Since(started).Round(time.Second).String()}
	}, manager, nodes)
	defer ps.Close()
	log.Printf("VPNSOPR panel %s; agent listener %s", *panelAddr, ln.Addr())
	for {
		conn, e := ln.Accept()
		if e != nil {
			log.Printf("accept: %v", e)
			continue
		}
		go func(c net.Conn) {
			tc := c.(*tls.Conn)
			if e := tc.Handshake(); e != nil {
				_ = c.Close()
				return
			}
			var idLen uint16
			if e := binary.Read(tc, binary.BigEndian, &idLen); e != nil || idLen == 0 || idLen > 64 {
				_ = c.Close()
				return
			}
			id := make([]byte, idLen)
			if _, e := io.ReadFull(tc, id); e != nil {
				_ = c.Close()
				return
			}
			var tokLen uint16
			if e := binary.Read(tc, binary.BigEndian, &tokLen); e != nil || tokLen < 24 || tokLen > 256 {
				_ = c.Close()
				return
			}
			tok := make([]byte, tokLen)
			if _, e := io.ReadFull(tc, tok); e != nil {
				_ = c.Close()
				return
			}
			nodeID := string(id)
			if !nodes.Authenticate(nodeID, string(tok)) {
				_ = c.Close()
				return
			}
			sess := tunnel.NewSession(tc, nil)
			if e := nodes.Attach(nodeID, sess); e != nil {
				_ = c.Close()
				return
			}
			log.Printf("node %s connected from %s", nodeID, c.RemoteAddr())
			<-sess.Done()
			nodes.Detach(nodeID, sess)
			log.Printf("node %s disconnected", nodeID)
		}(conn)
	}
}
func connectedCount(ns []tunnel.Node) int {
	n := 0
	for _, x := range ns {
		if x.Connected {
			n++
		}
	}
	return n
}
