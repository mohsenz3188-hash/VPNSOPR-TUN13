package panel

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/OWNER/VPNSOPR/internal/tunnel"
)

type Snapshot func() any

type NodeController interface {
	List() []tunnel.Node
	Create(string) (tunnel.Node, string, error)
	RotateToken(string) (string, error)
	Delete(string) error
}
type TunnelController interface {
	List() []tunnel.Forward
	Add(tunnel.Forward) (tunnel.Forward, error)
	Delete(string) error
}

func Server(addr, token string, snapshot Snapshot, tunnels TunnelController, nodes NodeController) *http.Server {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(got) != len(token) || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "VPNSOPR"})
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(snapshot())
	})
	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(nodes.List())
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			var req struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", 400)
				return
			}
			n, secret, err := nodes.Create(req.Name)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"node": n, "token": secret, "warning": "Copy this token now. It is shown only once."})
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
		if id == "" {
			http.Error(w, "invalid node id", 400)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(id, "/rotate-token") {
			nodeID := strings.TrimSuffix(id, "/rotate-token")
			if nodeID == "" || strings.Contains(nodeID, "/") {
				http.Error(w, "invalid node id", 400)
				return
			}
			secret, err := nodes.RotateToken(nodeID)
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": secret, "warning": "Copy this token now. It is shown only once."})
			return
		}
		if strings.Contains(id, "/") {
			http.Error(w, "invalid node id", 400)
			return
		}
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE, POST")
			http.Error(w, "method not allowed", 405)
			return
		}
		if err := nodes.Delete(id); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/tunnels", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(tunnels.List())
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
			var f tunnel.Forward
			if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
				http.Error(w, "invalid JSON", 400)
				return
			}
			created, err := tunnels.Add(f)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(created)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/tunnels/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			http.Error(w, "method not allowed", 405)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
		if id == "" || strings.Contains(id, "/") {
			http.Error(w, "invalid tunnel id", 400)
			return
		}
		if err := tunnels.Delete(id); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(dashboard))
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "panel listener failed: "+err.Error(), 503)
		})}
	}
	secureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: secureHandler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return srv
}

const dashboard = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>VPNSOPR Control</title><style>
:root{color-scheme:dark;font-family:Inter,system-ui,sans-serif;background:#08111f;color:#e8f0ff}*{box-sizing:border-box}
body{margin:0;padding:28px;background:radial-gradient(ellipse at 20% 0,#142d50,#08111f 55%)}main{max-width:1050px;margin:auto}
h1{letter-spacing:.12em;margin:0;font-size:clamp(28px,5vw,42px)}.muted{color:#8fa7c7}.top{display:flex;justify-content:space-between;align-items:center}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px;margin:24px 0}
.card{background:#111f33;border:1px solid #263d5c;border-radius:16px;padding:18px}.value{font-size:23px;font-weight:700;margin-top:8px}
input,button{background:#0a1728;color:#e8f0ff;border:1px solid #345273;border-radius:9px;padding:11px;min-width:0}
button{cursor:pointer;background:#1456a0}button.danger{background:#722b3b}.auth,.form{display:flex;gap:8px;flex-wrap:wrap;margin:18px 0}
.form input{flex:1 1 170px}.section{margin-top:28px}.tablewrap{overflow:auto;border:1px solid #263d5c;border-radius:14px}
table{width:100%;border-collapse:collapse;background:#111f33}td,th{text-align:left;padding:13px;border-bottom:1px solid #263d5c;white-space:nowrap}
#status{margin:12px 0;color:#8fa7c7}.hint{font-size:13px;color:#8fa7c7}
</style></head><body><main>
<div class="top"><div><h1>VPNSOPR</h1><div class="muted">VPS NETWORK CONTROL CENTER</div></div><div class="muted">v0.5.0</div></div>
<div class="auth"><input id="token" type="password" placeholder="Panel API token" autocomplete="off"><button onclick="connect()">Connect</button></div>
<div id="status">Connect using the management token.</div>
<div class="grid">
<div class="card"><div class="muted">Service</div><div class="value" id="service">—</div></div>
<div class="card"><div class="muted">Remote VPS agent</div><div class="value" id="client">—</div></div>
<div class="card"><div class="muted">Accepted connections</div><div class="value" id="connections">—</div></div>
<div class="card"><div class="muted">Uptime</div><div class="value" id="uptime">—</div></div>
</div>
<section class="section"><h2>Registered VPS nodes</h2>
<div class="hint">Maximum 10 VPS nodes. Create a node here, then run its agent using the one-time token.</div>
<div class="form"><input id="nodeName" maxlength="64" placeholder="VPS name / location"><button onclick="addNode()">Register VPS</button></div>
<div class="tablewrap"><table><thead><tr><th>Name</th><th>Node ID</th><th>Status</th><th>Action</th></tr></thead><tbody id="nodes"><tr><td colspan="4">No nodes registered</td></tr></tbody></table></div>
<section class="section"><h2>New VPS tunnel / port forward</h2>
<div class="hint">Public listen address is on this VPS. Target address is reached from the connected remote VPS.</div>
<div class="form"><input id="name" placeholder="Tunnel name"><input id="listen" placeholder="0.0.0.0:8443"><input id="target" placeholder="127.0.0.1:443"><input id="nodeID" placeholder="Remote node ID"><button onclick="addTunnel()">Create tunnel</button></div>
<div class="tablewrap"><table><thead><tr><th>Name</th><th>This VPS listens</th><th>Remote VPS target</th><th>Node ID</th><th>Action</th></tr></thead><tbody id="tunnels"><tr><td colspan="4">No tunnels loaded</td></tr></tbody></table></div>
</section>
</main><script>
function token(){return document.getElementById("token").value||sessionStorage.getItem("vpsopr-token")||""}
function headers(){return {"Authorization":"Bearer "+token(),"Content-Type":"application/json"}}
function connect(){sessionStorage.setItem("vpsopr-token",document.getElementById("token").value);refresh()}
async function api(path,opts={}){const r=await fetch(path,{...opts,headers:{...headers(),...(opts.headers||{})},cache:"no-store"});if(!r.ok)throw new Error(await r.text()||("HTTP "+r.status));if(r.status===204)return null;return r.json()}
async function refresh(){
 const t=token();if(!t)return;document.getElementById("token").value=t;
 try{
  const [s,ts,ns]=await Promise.all([api("/api/status"),api("/api/tunnels"),api("/api/nodes")]);
  document.getElementById("service").textContent=s.service||"VPNSOPR";
  document.getElementById("client").textContent=s.client_connected?"Connected":"Disconnected";
  document.getElementById("connections").textContent=s.connected_nodes+" / "+s.registered_nodes+" nodes";
  document.getElementById("uptime").textContent=s.uptime||"—";
  const nb=document.getElementById("nodes");nb.replaceChildren();
  for(const n of ns){const tr=document.createElement("tr");for(const v of [n.name,n.id,n.connected?"Connected":"Offline"]){const td=document.createElement("td");td.textContent=v;tr.appendChild(td)}
   const td=document.createElement("td"),rot=document.createElement("button"),b=document.createElement("button");rot.textContent="Rotate token";rot.onclick=()=>rotateNode(n.id);b.textContent="Delete";b.className="danger";b.onclick=()=>removeNode(n.id);td.appendChild(rot);td.appendChild(b);tr.appendChild(td);nb.appendChild(tr)}
  if(!ns.length){const tr=document.createElement("tr"),td=document.createElement("td");td.colSpan=4;td.textContent="No VPS nodes registered";tr.appendChild(td);nb.appendChild(tr)}
  const body=document.getElementById("tunnels");body.replaceChildren();
  for(const t of ts){const tr=document.createElement("tr");for(const v of [t.name,t.listen,t.target,t.node_id]){const td=document.createElement("td");td.textContent=v;tr.appendChild(td)}
   const td=document.createElement("td"),b=document.createElement("button");b.textContent="Delete";b.className="danger";b.onclick=()=>removeTunnel(t.id);td.appendChild(b);tr.appendChild(td);body.appendChild(tr)}
  if(!ts.length){const tr=document.createElement("tr"),td=document.createElement("td");td.colSpan=5;td.textContent="No tunnels configured";tr.appendChild(td);body.appendChild(tr)}
  document.getElementById("status").textContent="Live data refreshed at "+new Date().toLocaleTimeString();
 }catch(e){document.getElementById("status").textContent="Error: "+e.message}
}
async function addNode(){try{const r=await api("/api/nodes",{method:"POST",body:JSON.stringify({name:document.getElementById("nodeName").value})});alert("Node ID: "+r.node.id+"\\nAgent token (copy now): "+r.token+"\\nRun client with -node-id "+r.node.id+" -token TOKEN");document.getElementById("nodeName").value="";refresh()}catch(e){alert(e.message)}}
async function rotateNode(id){if(!confirm("Rotate token? The current agent will disconnect until updated with the new token."))return;try{const r=await api("/api/nodes/"+encodeURIComponent(id)+"/rotate-token",{method:"POST"});alert("New one-time agent token: "+r.token+"\\nUpdate the agent configuration now.");}catch(e){alert(e.message)}}
async function removeNode(id){if(!confirm("Delete this node? Disconnect its agent first."))return;try{await api("/api/nodes/"+encodeURIComponent(id),{method:"DELETE"});refresh()}catch(e){alert(e.message)}}
async function addTunnel(){try{await api("/api/tunnels",{method:"POST",body:JSON.stringify({name:document.getElementById("name").value,listen:document.getElementById("listen").value,target:document.getElementById("target").value,node_id:document.getElementById("nodeID").value})});document.getElementById("name").value="";refresh()}catch(e){alert(e.message)}}
async function removeTunnel(id){if(!confirm("Delete this tunnel?"))return;try{await api("/api/tunnels/"+encodeURIComponent(id),{method:"DELETE"});refresh()}catch(e){alert(e.message)}}
refresh();setInterval(refresh,5000);
</script></body></html>`
