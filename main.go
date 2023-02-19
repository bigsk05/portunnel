package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Config struct {
	Forwardings []Forwarding `json:"forwardings"`
}

type Forwarding struct {
	LocalAddress  string `json:"local_address"`
	LocalPort     int    `json:"local_port"`
	RemoteAddress string `json:"remote_address"`
	RemotePort    int    `json:"remote_port"`
}

func main() {
	// Read configuration file
	configFile := "config.json"
	config, err := readConfig(configFile)
	if err != nil {
		fmt.Printf("Failed to read config file: %v\n", err)
		return
	}

	// Start forwarding task
	for _, f := range config.Forwardings {
		go runForwarding(f)
	}

	// Listen for configuration file changes
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("Failed to create watcher: %v\n", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(configFile); err != nil {
		fmt.Printf("Failed to watch config file: %v\n", err)
		return
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				fmt.Printf("Config file changed, reloading...\n")

				// Wait for the file system to complete the write operation
				time.Sleep(100 * time.Millisecond)

				// Re-read configuration file
				newConfig, err := readConfig(configFile)
				if err != nil {
					fmt.Printf("Failed to read config file: %v\n", err)
					continue
				}

				// Stop old forwarding task
				for _, f := range config.Forwardings {
					stopForwarding(f)
				}

				// Start a new forwarding task
				for _, f := range newConfig.Forwardings {
					go runForwarding(f)
				}

				config = newConfig
			}
		case err := <-watcher.Errors:
			fmt.Printf("Watcher error: %v\n", err)
		case sig := <-sigCh:
			fmt.Printf("Received signal %v, shutting down...\n", sig)
			// Stop all forwarding tasks
			for _, f := range config.Forwardings {
				stopForwarding(f)
			}
			return
		}
	}
}

func readConfig(filename string) (Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	config := Config{}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}

	return config, nil
}

func runForwarding(f Forwarding) {
	localAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", f.LocalAddress, f.LocalPort))
	if err != nil {
		fmt.Printf("Failed to resolve local address: %v\n", err)
		return
	}

	remoteAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", f.RemoteAddress, f.RemotePort))
	if err != nil {
		fmt.Printf("Failed to resolve remote address: %v\n", err)
		return
	}
	listener, err := net.ListenTCP("tcp", localAddr)
	if err != nil {
		fmt.Printf("Failed to listen on local address: %v\n", err)
		return
	}
	defer listener.Close()

	fmt.Printf("Started forwarding %s:%d to %s:%d\n", f.LocalAddress, f.LocalPort, f.RemoteAddress, f.RemotePort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Failed to accept connection: %v\n", err)
			continue
		}

		go func() {
			defer conn.Close()

			remoteConn, err := net.DialTCP("tcp", nil, remoteAddr)
			if err != nil {
				fmt.Printf("Failed to connect to remote address: %v\n", err)
				return
			}
			defer remoteConn.Close()

			fmt.Printf("Forwarding %s -> %s\n", conn.RemoteAddr(), remoteConn.RemoteAddr())

			// Forward data from a local connection to a remote connection
			go copyConn(conn, remoteConn)

			// Forward data from remote connections back to local connections
			copyConn(remoteConn, conn)
		}()
	}
}

func stopForwarding(f Forwarding) {
	conn, err := net.DialTCP("tcp", nil, &net.TCPAddr{
		IP:   net.ParseIP(f.LocalAddress),
		Port: f.LocalPort,
	})
	if err != nil {
		fmt.Printf("Failed to stop forwarding %s:%d: %v\n", f.LocalAddress, f.LocalPort, err)
		return
	}
	conn.Close()
}

func copyConn(src net.Conn, dst net.Conn) {
	buf := make([]byte, 1024)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}
