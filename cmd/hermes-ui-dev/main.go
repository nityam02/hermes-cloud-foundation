// Command hermes-ui-dev serves the Hermes UI over pretend clouds, for working on the UI without any
// cloud account. It serves ui/dist from disk, so `npm run build` is enough to see a change.
//
//	make ui-dev    # or: go run ./cmd/hermes-ui-dev -addr 127.0.0.1:8788
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/uiserver"
	"github.com/nityam02/hermes-cloud-foundation/internal/uiserver/fakecloud"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8788", "loopback address")
	static := flag.String("static", "ui/dist", "built UI to serve")
	demo := flag.String("demo", "ui/public/demo", "documents the pretend discovery returns")
	delay := flag.Duration("delay", 500*time.Millisecond, "unit of every pretend wait")
	state := flag.String("state", "", "directory to remember the connection in (default: memory only)")
	flag.Parse()
	if err := uiserver.ValidateAddr(*addr); err != nil {
		log.Fatal(err)
	}
	token := uiserver.NewToken()
	world := fakecloud.New(*delay, *demo)
	h, err := uiserver.New(uiserver.Config{Token: token, Actions: world.Actions(), Version: "dev (pretend clouds)", StateDir: *state}, os.DirFS(*static))
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Hermes UI over pretend clouds (no real account is used):\n\n  http://%s/?token=%s\n\n", ln.Addr(), token)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}
