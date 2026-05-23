module trayawake

go 1.26.0

require (
	github.com/tailscale/walk v0.0.0-20251016200523-963e260a8227
	golang.org/x/sys v0.43.0
)

require (
	github.com/akavel/rsrc v0.10.2 // indirect
	github.com/dblohm7/wingoes v0.0.0-20231019175336-f6e33aa7cc34 // indirect
	github.com/tailscale/win v0.0.0-20250213223159-5992cb43ca35 // indirect
	golang.org/x/exp v0.0.0-20230425010034-47ecfdc1ba53 // indirect
	gopkg.in/Knetic/govaluate.v3 v3.0.0 // indirect
)

tool (
	github.com/akavel/rsrc
	golang.org/x/sys/windows/mkwinsyscall
)
