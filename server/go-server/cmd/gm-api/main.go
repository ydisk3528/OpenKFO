package main

import (
	"encoding/json"
	"flag"
	"golang.org/x/crypto/bcrypt"
	"kungfu.local/server/internal/adminhttp"
	"kungfu.local/server/internal/desktop"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/releases"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:19092", "management listener")
	root := flag.String("root", ".", "directory containing runtime-local/client")
	cert := flag.String("tls-cert", "", "HTTPS certificate; omit only behind a local HTTPS proxy")
	key := flag.String("tls-key", "", "HTTPS private key")
	flag.Parse()
	token := os.Getenv("KK_GM_TOKEN")
	if len(token) < 32 {
		log.Fatal("KK_GM_TOKEN must contain at least 32 bytes")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		log.Fatal(err)
	}
	if *cert == "" || *key == "" {
		ip := net.ParseIP(host)
		if *cert != "" || *key != "" || ip == nil || !ip.IsLoopback() {
			log.Fatal("plain HTTP is allowed only on a literal loopback address behind an HTTPS proxy")
		}
	}
	store, err := persistence.OpenExisting(os.Getenv("KK_MYSQL_DSN"))
	if err != nil {
		log.Fatal("cannot connect management database")
	}
	defer store.DB.Close()
	admin := desktop.New(*root)
	admin.Remote = func(req persistence.AdminRequest) (json.RawMessage, error) {
		var result any
		var err error
		if req.Operation == "stages_get" || req.Operation == "stages_save" {
			executable, _ := os.Executable()
			dir := filepath.Dir(executable)
			updates := os.Getenv("OPENKFO_UPDATES_DIR")
			if updates == "" {
				updates = filepath.Join(dir, "updates")
			}
			var hash string
			hash, err = releases.ActiveConfigHash(filepath.Join(dir, "config.json"), updates)
			if err == nil {
				result, err = store.AdminStages(req, hash)
			}
		} else {
			result, err = store.Admin(req)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	handler := adminhttp.New(token, admin.Call)
	if file := os.Getenv("KK_GM_ACCOUNTS_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			log.Fatal("cannot read GM accounts file")
		}
		var accounts map[string]string
		if json.Unmarshal(data, &accounts) != nil || len(accounts) == 0 {
			log.Fatal("invalid GM accounts file")
		}
		for _, hash := range accounts {
			if _, err := bcrypt.Cost([]byte(hash)); err != nil {
				log.Fatal("invalid GM password hash")
			}
		}
		dummy, _ := bcrypt.GenerateFromPassword([]byte("unused-password"), bcrypt.DefaultCost)
		handler = adminhttp.WithLogin(handler, token, func(account, password string) bool {
			hash, ok := accounts[account]
			if !ok {
				hash = string(dummy)
			}
			valid := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
			return valid && ok && account != ""
		})
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	log.Printf("GM API listening on %s", *listen)
	if *cert != "" {
		log.Fatal(server.ListenAndServeTLS(*cert, *key))
	}
	log.Fatal(server.ListenAndServe())
}
