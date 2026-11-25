package main

import (
	"encoding/json"
	"ezpanel-sdk/panelsdk"
	"log"
	"net/http"
)

func main() {
	p, e := panelsdk.New()
	if e != nil {
		log.Fatal(e)
	}
	e = p.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		info, e := p.Host.Sysinfo(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if e != nil {
			w.WriteHeader(502)
			json.NewEncoder(w).Encode(map[string]any{"code": 2001, "msg": "无法读取 Host 信息", "data": nil})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"user": r.Header.Get("X-Panel-User"), "hostname": info.Hostname, "arch": info.Arch}})
	})
	if e != nil {
		log.Fatal(e)
	}
	if e = p.Run(); e != nil {
		log.Fatal(e)
	}
}
