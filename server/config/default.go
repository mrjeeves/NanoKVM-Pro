package config

var defaultConfig = &Config{
	Proto: "https",
	Port: Port{
		Http:  80,
		Https: 443,
	},
	Cert: Cert{
		Crt: "/etc/kvm/server.crt",
		Key: "/etc/kvm/server.key",
	},
	Logger: Logger{
		Level: "info",
		File:  "stdout",
	},
	JWT: JWT{
		SecretKey:            "",
		RefreshTokenDuration: 2678400,
		RevokeTokensOnLogout: true,
	},
	Stun: "stun.l.google.com:19302",
	Turn: Turn{
		TurnAddr: "",
		TurnUser: "",
		TurnCred: "",
	},
	Authentication: "enable",
	Mesh: Mesh{
		Enabled: true,
		Name:    "CEC-KVM",
		Home:    "/data/myownmesh",
		Socket:  "/run/myownmesh/daemon.sock",
		// Empty = the per-device joining mesh (cec-kvm-xxxxx-xxxxx, derived
		// from the daemon identity). Set only to pin a custom joining mesh.
		NetworkId: "",
		Label:     "CEC KVM Joining Mesh",
		Relays:    nil,
		// Claims over the public mesh are OFF unless the operator flips this
		// in /etc/kvm/server.yaml — claiming is local-network only by default
		// (the Go zero value; spelled out here for self-documentation).
		PublicClaims: false,
		DaemonBin:    "/kvmapp/system/bin/myownmesh",
		// The USR button is not surfaced to Linux by the Pro firmware, so the
		// button watcher is OFF by default (the web UI / API still raise a
		// hand). To enable it, confirm the USR button's evdev node on the
		// device, set inputDevice to it, and flip buttonEnabled in server.yaml.
		HandRaise: HandRaise{
			ButtonEnabled: false,
			InputDevice:   "/dev/input/event0",
			KeyCode:       0,
		},
	},
}

func checkDefaultValue() {
	if instance.JWT.SecretKey == "" {
		instance.JWT.SecretKey = generateRandomSecretKey()
		instance.JWT.RevokeTokensOnLogout = true
	}

	if instance.JWT.RefreshTokenDuration == 0 {
		instance.JWT.RefreshTokenDuration = 2678400
	}

	if instance.Stun == "" {
		instance.Stun = "stun.l.google.com:19302"
	}

	if instance.Authentication == "" {
		instance.Authentication = "enable"
	}

	// Fill mesh defaults for a config.yaml written before the mesh block
	// existed (viper leaves the zero value otherwise). We can't distinguish a
	// user-set Enabled:false from an absent block here, so only the string
	// fields are defaulted — Enabled defaults via the viper.IsSet check in
	// initialize().
	if instance.Mesh.Name == "" {
		instance.Mesh.Name = "CEC-KVM" // default brand/display name on the graph
	}
	if instance.Mesh.Home == "" {
		instance.Mesh.Home = "/data/myownmesh"
	}
	if instance.Mesh.Socket == "" {
		// runtime tmpfs default (systemd RuntimeDirectory=myownmesh) — keeps the
		// socket off the data partition regardless of its filesystem. Must match
		// the daemon's control_socket (set via the myownmesh systemd unit /
		// $Home/config.json).
		instance.Mesh.Socket = "/run/myownmesh/daemon.sock"
	}
	// NetworkId: empty is MEANINGFUL — it selects the per-device joining mesh
	// (cec-kvm-xxxxx-xxxxx). The retired shared default from earlier releases is
	// migrated to empty so those devices pick up their own joining mesh too;
	// only a genuinely custom value survives.
	if instance.Mesh.NetworkId == "cec-backend-client-mesh" {
		instance.Mesh.NetworkId = ""
	}
	if instance.Mesh.Label == "" || instance.Mesh.Label == "CEC Backend Client Mesh" {
		instance.Mesh.Label = "CEC KVM Joining Mesh"
	}
	if instance.Mesh.DaemonBin == "" {
		instance.Mesh.DaemonBin = "/kvmapp/system/bin/myownmesh"
	}

	instance.Hardware = getHardware()
}
