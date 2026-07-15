package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"NanoKVM-Server/common"
	"NanoKVM-Server/config"
	"NanoKVM-Server/logger"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/router"
	"NanoKVM-Server/service/button"
	"NanoKVM-Server/service/mesh"
	"NanoKVM-Server/service/mesh/glue"
	"NanoKVM-Server/service/vm/jiggler"

	"github.com/gin-gonic/gin"
	cors "github.com/rs/cors/wrapper/gin"
)

func main() {
	initialize()
	defer dispose()

	run()
}

func initialize() {
	logger.Init()

	// init screen parameters
	_ = common.GetScreen()

	// run mouse jiggler
	jiggler.GetJiggler().Run()

	// waiting for exit signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		sig := <-sigChan
		log.Printf("\nReceived signal: %v\n", sig)

		dispose()
		os.Exit(0)
	}()
}

func run() {
	conf := config.GetInstance()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Recovery())

	if conf.Authentication == "disable" {
		r.Use(cors.AllowAll())
	}

	if conf.Proto != "http" {
		r.Use(middleware.Tls())
	}

	// Give mesh-tunneled requests a session cookie so the web UI's client-side
	// login gate treats a mesh-authorized viewer as logged in (no KVM password).
	// Registered before router.Init so it sets the cookie on the SPA's own HTML
	// response; direct LAN requests are never mesh-marked, so they're unaffected.
	r.Use(middleware.MeshSessionCookie())

	router.Init(r)

	// Native AllMyStuff mesh bridge: join the cloud mesh, advertise this device
	// as a KVM appliance, and tunnel the web UI over the mesh "sites" plane. The
	// bridge is non-fatal and retries, so a missing daemon never blocks the LAN
	// server. RegisterRoutes is always called (nil-tolerant) so the web UI's
	// Mesh tab can report enabled:false when the bridge is off.
	var bridge *mesh.Bridge
	if conf.Mesh.Enabled {
		bridge = mesh.NewBridge(r, conf)
		// Wire the native (Slice 1) screen/HID path: the bridge is CGO-free, so
		// its H.264 encoder and HID gadget arrive as injected interfaces from the
		// on-device glue. A display route then streams the KVM's screen and an
		// input route injects its keyboard/mouse.
		bridge.SetVideoSource(glue.NewVideoSource())
		bridge.SetInputSink(glue.NewInputSink())
		go bridge.Start(make(chan struct{}))

		// Wire the physical user (USR) button to the CEC hand-raise. Off by
		// default on the Pro (the USR node isn't confirmed); self-disabling if
		// the input node isn't present.
		button.Watch(button.Config{
			Enabled: conf.Mesh.HandRaise.ButtonEnabled,
			Device:  conf.Mesh.HandRaise.InputDevice,
			KeyCode: conf.Mesh.HandRaise.KeyCode,
		}, bridge)
	}
	mesh.RegisterRoutes(r, bridge)

	httpAddr := fmt.Sprintf(":%d", conf.Port.Http)

	if conf.Proto == "http" {
		log.Printf("Starting HTTP server on %s\n", httpAddr)
		if err := r.Run(httpAddr); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	} else {
		httpsAddr := fmt.Sprintf(":%d", conf.Port.Https)
		log.Printf("Starting HTTPS server on %s, %s\n", httpAddr, httpsAddr)

		go runRedirect(httpAddr, httpsAddr)

		if err := r.RunTLS(httpsAddr, conf.Cert.Crt, conf.Cert.Key); err != nil {
			log.Fatalf("HTTPS server failed: %v", err)
		}
	}
}

func runRedirect(httpPort string, httpsPort string) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host, _, _ := net.SplitHostPort(req.Host)
		if host == "" {
			host = req.Host
		}

		targetURL := "https://" + host
		if httpsPort != ":443" {
			targetURL += httpsPort
		}
		targetURL += req.URL.String()

		http.Redirect(w, req, targetURL, http.StatusTemporaryRedirect)
	})

	if err := http.ListenAndServe(httpPort, handler); err != nil {
		log.Fatalf("HTTP redirect server failed: %v", err)
	}
}

func dispose() {
	common.GetKvmVision().Close()
}
