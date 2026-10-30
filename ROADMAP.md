# VPNSOPR roadmap

## Implemented in v0.6.0
- Multi-VPS registry, limited to 10 nodes per gateway
- Per-node random credentials, SHA-256 hashes at rest, and one-time token display
- Rotate/revoke node credentials from the management API
- TLS 1.3 authenticated node agents with reconnect
- Multiple TCP forwards routed to selected nodes
- Web panel for node registration, status, token rotation and tunnel management
- Persistent state, security headers and health endpoint
- GitHub Actions CI and Linux amd64/arm64 release workflow
- One-line installer with release checksum verification and systemd integration

## Planned
- [ ] UDP forwarding and bounded flow tracking
- [ ] QUIC, WebSocket/WSS and KCP/FEC transports
- [ ] Health probes, latency/jitter/loss history
- [ ] Failover groups and weighted load balancing
- [ ] Per-node bandwidth and connection quotas
- [ ] TOTP MFA, scoped API keys and audit log
- [ ] Backup/restore and signed auto-updater
- [ ] Telegram alerts and service watchdog
- [ ] SOCKS5/HTTP CONNECT proxy
- [ ] Direct L3 TUN routing after security review

Planned items are not implemented until shipped and tested.
