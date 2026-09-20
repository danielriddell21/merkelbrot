// letsgo.mod

// The binary lives in the renderer module, which is separate so that importing
// the core never pulls in templ. go.work ties the two together; this names
// which one is released.
module web

// The six targets the GoReleaser config built. letsgo's default matrix is
// five, so windows/arm64 is named to keep the published asset list the same.
build (
	linux/amd64
	linux/arm64
	darwin/amd64
	darwin/arm64
	windows/amd64
	windows/arm64
)

brew danielriddell21/tap

// The shared GoReleaser workflow marked releases as pre-releases after
// publishing; letsgo does it while publishing, so promote.yaml still fires on
// manual promotion.
release prerelease=true
