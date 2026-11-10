package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html static
var files embed.FS

// Index is the demo page served at /.
func Index() []byte {
	body, err := files.ReadFile("index.html")
	if err != nil {
		panic(err)
	}
	return body
}

// Static is the asset tree served below /assets/.
func Static() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
