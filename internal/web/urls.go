package web

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// URL is where to open the web UI on this computer
func URL(host string, port int) string {
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return address(host, port)
}

// ReceiverURL is where a mitm has to send its traffic
func ReceiverURL(host string, port int) string {
	switch host {
	case "", "0.0.0.0", "::":
		if inContainer() {
			// a container only knows its own address, not the one of the computer it runs on
			host = "<your computer's IP>"
		} else {
			host = lanIP()
		}
	}
	return address(host, port)
}

func address(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + strconv.Itoa(port)
}

func inContainer() bool {
	// docker and podman create these
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

// lanIP is the address your phone can reach this computer at
func lanIP() string {
	// nothing is sent, connecting a udp socket only picks the interface
	conn, err := net.Dial("udp4", "10.254.254.254:1")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if local, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return local.IP.String()
	}
	return "127.0.0.1"
}

// OpenBrowser opens the web UI in the default browser, if there is one
func OpenBrowser(url string) {
	if inContainer() {
		return
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if command.Start() == nil {
		go func() { _ = command.Wait() }()
	}
}
