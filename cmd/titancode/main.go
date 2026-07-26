package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/titancode-dev/titancode/internal/project"
	"github.com/titancode-dev/titancode/internal/server"
)

func main() {
	var (
		repository = flag.String("repo", ".", "repository to observe")
		address    = flag.String("addr", "127.0.0.1:7331", "HTTP listen address")
		noOpen     = flag.Bool("no-open", false, "do not open the browser")
	)
	flag.Parse()

	root, err := filepath.Abs(*repository)
	if err != nil {
		log.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		log.Fatalf("repository is not a directory: %s", root)
	}

	app := server.New(project.NewScanner(root))
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	fmt.Printf("TitanCode is observing %s\n", root)
	fmt.Printf("Dashboard: %s\n", url)

	if !*noOpen {
		go openBrowser(url)
	}
	if err := http.Serve(listener, app); err != nil {
		log.Fatal(err)
	}
}

func openBrowser(url string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		command = exec.Command("xdg-open", url)
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = command.Run()
}
