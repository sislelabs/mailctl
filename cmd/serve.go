package cmd

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sislelabs/mailctl/internal/store"
	"github.com/sislelabs/mailctl/internal/ui"
	"github.com/sislelabs/mailctl/internal/web"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the local web panel",
	Long: `Run mailctl's web panel on this machine.

The panel reads the same config the CLI does, which means it holds your
Cloudflare token — DNS edit rights across every zone that token can see. It
binds to localhost and has no authentication, so it is safe on your own machine
and unsafe anywhere else. Binding to another address requires --i-know.`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

var (
	serveAddr   string
	serveUnsafe bool
)

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:7777", "Address to listen on")
	serveCmd.Flags().BoolVar(&serveUnsafe, "i-know", false, "Allow binding to a non-loopback address")
}

func runServe(cmd *cobra.Command, args []string) error {
	st := store.NewYAML()
	if _, err := st.Load(); err != nil {
		return err
	}

	if !serveUnsafe && !isLoopback(serveAddr) {
		return fmt.Errorf("refusing to bind %s: the panel has no authentication and holds your Cloudflare token.\n"+
			"Use a loopback address, or pass --i-know if you have your own protection in front of it", serveAddr)
	}

	srv, err := web.NewServer(st)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              serveAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Println()
	fmt.Println(ui.SuccessPanel.Render(
		ui.Success.Bold(true).Render("mailctl panel") + "\n\n" +
			ui.Dim.Render("Listening on ") + ui.Highlight.Render("http://"+serveAddr) + "\n" +
			ui.Dim.Render("Press Ctrl+C to stop."),
	))
	fmt.Println()

	return httpSrv.ListenAndServe()
}

// isLoopback reports whether addr binds only to the local machine. An empty
// host means every interface, which is exactly what we are guarding against.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
