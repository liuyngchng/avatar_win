module github.com/liuyngchng/avatar-pc

go 1.24.13

require (
	github.com/ebitengine/oto/v3 v3.4.1
	github.com/gen2brain/malgo v0.11.26
	github.com/go-ole/go-ole v1.2.6
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	github.com/moutend/go-wca v0.3.0
	github.com/mozillazg/go-pinyin v0.21.0
	github.com/zserge/lorca v0.1.10
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/jchv/go-webview2 => ./third_party/go-webview2

require (
	github.com/ebitengine/purego v0.9.0 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	golang.org/x/net v0.0.0-20200222125558-5a598a2470a0 // indirect
	golang.org/x/sys v0.36.0 // indirect
)
