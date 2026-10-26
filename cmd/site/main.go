// Command site builds and serves the dashboard's demo page.
//
//	site build --out dist [--version v0.1.0]
//	site serve --dir dist [--addr 127.0.0.1:8080] [--isolate]
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/go-wasmcontainers/dashboard/internal/site"
)

// osExit is replaced by tests.
var osExit = os.Exit

// listen is replaced by tests.
var listen = net.Listen

// RootCmd is the command tree.
func RootCmd() *cobra.Command {
	root := &cobra.Command{Use: "site", Short: "Build and serve the dashboard's demo page", SilenceUsage: true}
	var out, version, rootDir string
	build := &cobra.Command{
		Use:   "build",
		Short: "Build the site (page, wasm programs, demo images and their /v2 registry)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			files, err := site.Build(context.Background(), site.Options{Root: rootDir, Out: out, Version: version, Log: cmd.ErrOrStderr()})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %d files to %s\n", len(files), out)
			return nil
		},
	}
	build.Flags().StringVarP(&out, "out", "o", "dist", "output directory")
	build.Flags().StringVar(&version, "version", "dev", "build id stamped into the programs and asset URLs")
	build.Flags().StringVar(&rootDir, "root", ".", "the module's root directory")

	var dir, addr, prefix string
	var isolate bool
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve a built site locally",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := listen("tcp", addr)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "serving %s at http://%s%s\n", dir, l.Addr(), prefix)
			return http.Serve(l, site.Handler(dir, prefix, isolate))
		},
	}
	serve.Flags().StringVarP(&dir, "dir", "d", "dist", "the built site")
	serve.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "address to listen on (loopback by default)")
	serve.Flags().StringVar(&prefix, "prefix", "/dashboard/", "URL path the site is served under")
	serve.Flags().BoolVar(&isolate, "isolate", false, "send COOP/COEP headers (else the page's service worker isolates it)")
	root.AddCommand(build, serve)
	return root
}

func main() {
	if err := RootCmd().Execute(); err != nil {
		osExit(1)
	}
}
