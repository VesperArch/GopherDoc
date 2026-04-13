module github.com/vesperarch/gopherdoc/analysis/releasecheck

go 1.26.1

require golang.org/x/tools v0.44.0

require (
	golang.org/x/mod v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
)

// replace points at the parent workspace module so integration tests and the
// standalone binary can analyse the real engine package without a published release.
replace github.com/vesperarch/gopherdoc => ../..
