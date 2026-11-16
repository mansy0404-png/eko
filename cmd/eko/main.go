package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"eko/internal/api"
	"eko/internal/bus"
	"eko/internal/ca"
	"eko/internal/config"
	"eko/internal/intercept"
	"eko/internal/project"
	"eko/internal/proxy"
)

const version = "0.1.0"

type runtimeInfo struct {
	PID       int    `json:"pid"`
	ProxyAddr string `json:"proxyAddr"`
	APIAddr   string `json:"apiAddr"`
	Token     string `json:"token"`
	URL       string `json:"url"`
}

func runtimePath() string { return filepath.Join(config.DataDir(), "runtime.json") }

func readRuntime() (*runtimeInfo, error) {
	b, err := os.ReadFile(runtimePath())
	if err != nil {
		return nil, fmt.Errorf("eko is not running (no runtime file)")
	}
	var info runtimeInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func usage() {
	fmt.Printf(`eko %s - a small MITM proxy for bug hunting

usage:
  eko                 start proxy + control UI
  eko url             print the control UI URL (with token)
  eko stop            stop the background daemon
  eko ca              print the path of the root CA certificate
  eko version         print the version

The proxy and UI bind to loopback only. Point FoxyProxy at the proxy address,
usually 127.0.0.1:8080, then install the root CA in the browser.
`, version)
}

func main() {
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix("eko: ")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		case "url":
			info, err := readRuntime()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println(info.URL)
			return
		case "stop":
			info, err := readRuntime()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			p, err := os.FindProcess(info.PID)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if err := p.Signal(syscall.SIGTERM); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			for i := 0; i < 40; i++ {
				if err := p.Signal(syscall.Signal(0)); err != nil {
					fmt.Println("eko: stopped")
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			fmt.Println("eko: stop requested (pid", info.PID, ")")
			return
		case "ca":
			fmt.Println(filepath.Join(config.ConfigDir(), "ca.pem"))
			return
		case "help", "-h", "--help":
			usage()
			return
		}
	}

	fs := flag.NewFlagSet("eko", flag.ExitOnError)
	proxyAddr := fs.String("proxy", "", "proxy listen address (default from settings)")
	apiAddr := fs.String("api", "", "control UI listen address (default from settings)")
	fs.Usage = usage
	_ = fs.Parse(os.Args[1:])

	box, err := config.Load()
	if err != nil {
		log.Fatalf("loading settings: %v", err)
	}
	s := box.Get()
	if *proxyAddr != "" {
		s.ProxyAddr = *proxyAddr
	}
	if *apiAddr != "" {
		s.APIAddr = *apiAddr
	}

	rootCA, err := ca.LoadOrCreate(config.ConfigDir())
	if err != nil {
		log.Fatalf("loading CA: %v", err)
	}
	pm, err := project.Open(config.DataDir(), s.MaxFlows, s.InlineBodyLimit)
	if err != nil {
		log.Fatalf("opening projects: %v", err)
	}
	defer pm.Close()

	b := bus.New()
	ic := intercept.New(b)
	px := proxy.New(rootCA, pm, b, ic, box)
	ic.Sync(s)
	box.OnChange(func(ns config.Settings) {
		px.Sync(ns)
		ic.Sync(ns)
		pm.SetLimits(ns.MaxFlows, ns.InlineBodyLimit)
	})

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Fatalf("generating token: %v", err)
	}
	token := hex.EncodeToString(tokenBytes)

	proxyLn, err := net.Listen("tcp", s.ProxyAddr)
	if err != nil {
		log.Fatalf("proxy listener %s: %v (is eko already running?)", s.ProxyAddr, err)
	}
	apiLn, err := net.Listen("tcp", s.APIAddr)
	if err != nil {
		proxyLn.Close()
		if info, rerr := readRuntime(); rerr == nil {
			log.Fatalf("UI listener %s: %v (already running: %s)", s.APIAddr, err, info.URL)
		}
		log.Fatalf("UI listener %s: %v", s.APIAddr, err)
	}

	uiURL := "http://" + s.APIAddr + "/?token=" + token
	info := runtimeInfo{PID: os.Getpid(), ProxyAddr: s.ProxyAddr, APIAddr: s.APIAddr, Token: token, URL: uiURL}
	if b, err := json.MarshalIndent(info, "", "  "); err == nil {
		if err := os.WriteFile(runtimePath(), b, 0o600); err != nil {
			log.Printf("writing runtime file: %v", err)
		}
	}

	stop := make(chan struct{})
	var stopOnce sync.Once
	requestStop := func() { stopOnce.Do(func() { close(stop) }) }

	apiServer := api.New(token, version, box, pm, b, ic, px, rootCA, requestStop)
	proxyServer := &http.Server{Handler: px, ReadHeaderTimeout: 30 * time.Second}
	uiServer := &http.Server{Handler: apiServer.Handler(), ReadHeaderTimeout: 30 * time.Second}

	go func() {
		if err := proxyServer.Serve(proxyLn); err != nil && err != http.ErrServerClosed {
			log.Printf("proxy server: %v", err)
		}
	}()
	go func() {
		if err := uiServer.Serve(apiLn); err != nil && err != http.ErrServerClosed {
			log.Printf("ui server: %v", err)
		}
	}()

	fmt.Printf(`eko %s
  proxy   %s
  ui      %s
  ca      %s
  data    %s
`, version, s.ProxyAddr, uiURL, filepath.Join(config.ConfigDir(), "ca.pem"), config.DataDir())
	log.Printf("ready - point FoxyProxy at %s", s.ProxyAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
	case <-stop:
	}
	log.Printf("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = uiServer.Shutdown(ctx)
	_ = proxyServer.Shutdown(ctx)
	_ = os.Remove(runtimePath())
}
