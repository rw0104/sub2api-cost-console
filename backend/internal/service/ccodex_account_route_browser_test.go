package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Browser fixture calls the production manager with a real installed runtime.
// Its only HTTP additions are an in-process bridge and synthetic test controls.
func ccodexAccountReuseBrowser(t *testing.T, manager *PluginManager, installation *PluginInstallation, script, aSession, bSession string, mismatch *atomic.Bool, repo *protectionMemoryRepository, requestB func()) {
	t.Helper()
	var failNext atomic.Bool
	finished := make(chan struct{})
	mux := http.NewServeMux()
	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.Dir(filepath.Join(installation.InstallPath, "ui")))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(ccodexReuseParent))
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var raw []byte
		var err error
		if r.Method == "PUT" {
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				http.Error(w, "invalid fixture input", 400)
				return
			}
			raw, _ = json.Marshal(input)
			if action, ok := input["core_request"].(map[string]any); ok && action["operation"] == "apply_account_route" && failNext.Swap(false) {
				repo.failSave = true
				raw, err = manager.SaveConfig(r.Context(), installation.ID, raw)
				repo.failSave = false
			} else {
				raw, err = manager.SaveConfig(r.Context(), installation.ID, raw)
			}
		} else {
			raw, err = manager.GetConfig(r.Context(), installation.ID)
		}
		if err != nil {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "fixture CAS conflict; original configuration restored"})
			return
		}
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		result, err := manager.Status(r.Context(), installation.ID)
		if err != nil {
			http.Error(w, "status unavailable", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/fixture/context", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"source_session": aSession, "target_session": bSession})
	})
	mux.HandleFunc("/fixture/allow-b", func(w http.ResponseWriter, r *http.Request) { mismatch.Store(false); w.WriteHeader(204) })
	mux.HandleFunc("/fixture/fail-next", func(w http.ResponseWriter, r *http.Request) { failNext.Store(true); w.WriteHeader(204) })
	mux.HandleFunc("/fixture/request-b", func(w http.ResponseWriter, r *http.Request) { requestB(); w.WriteHeader(204) })
	mux.HandleFunc("/fixture/finish", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		select {
		case <-finished:
		default:
			close(finished)
		}
	})
	server := httptest.NewUnstartedServer(mux)
	if script == "external" {
		require.NoError(t, server.Listener.Close())
		listener, err := net.Listen("tcp4", "0.0.0.0:0")
		require.NoError(t, err)
		server.Listener = listener
	}
	server.Start()
	defer server.Close()
	if script == "external" {
		_, port, err := net.SplitHostPort(server.Listener.Addr().String())
		require.NoError(t, err)
		t.Log("ACCOUNT_REUSE_BROWSER_PORT=" + port)
		ready := os.Getenv("SUB2API_CCODEX_BROWSER_READY_FILE")
		require.NotEmpty(t, ready)
		require.NoError(t, os.WriteFile(ready, []byte(port), 0600))
		select {
		case <-finished:
			t.Log("real signed-runtime browser reported completion")
		case <-time.After(80 * time.Second):
			t.Fatal("external browser did not finish")
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	python := os.Getenv("SUB2API_CCODEX_UI_PYTHON")
	require.NotEmpty(t, python)
	command := exec.CommandContext(ctx, python, script, "--url", server.URL, "--report-dir", os.Getenv("SUB2API_CCODEX_UI_REPORT_DIR"))
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
}

const ccodexReuseParent = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0}iframe{width:100%;height:900px;border:0}</style><iframe id="plugin" sandbox="allow-scripts"></iframe><script>
const token='synthetic-reuse-bridge',frame=document.getElementById('plugin');window.ready=false;
addEventListener('message',async event=>{
 const m=event.data;if(event.source!==frame.contentWindow||m?.source!=='sub2api-plugin-ui'||m.bridge_token!==token)return;
 if(m.type==='sub2api.plugin.ready'){window.ready=true;return;}if(m.type==='ui.resize')return;if(!m.request_id)return;
 const reply={source:'sub2api-plugin-host',bridge_token:token,request_id:m.request_id,ok:true};
 try{if(!['config.load','config.save','plugin.status'].includes(m.type))throw Error('unsupported bridge');const options=m.type==='config.save'?{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(m.config)}:{};const response=await fetch(m.type==='plugin.status'?'/status':'/config',options),data=await response.json();if(!response.ok)throw Error(data.message);if(m.type==='plugin.status')reply.result=data;else reply.config=data;}catch(error){reply.ok=false;reply.error=error.message;}
 frame.contentWindow.postMessage(reply,'*');
});frame.src='/ui/index.html#bridge_token='+token;
</script>`
