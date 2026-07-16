package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

type commandRequest struct {
	Operation string   `json:"operation"`
	Args      []string `json:"args"`
}

type commandResponse struct {
	Success bool   `json:"success"`
	Mounted bool   `json:"mounted"`
	Error   string `json:"error,omitempty"`
}

func main() {
	sockFile := "/var/run/oss-server.sock"

	_, err := os.Stat(sockFile)
	if err != nil && !os.IsNotExist(err) {
		panic(err)
	}

	if err == nil {
		if err = os.Remove(sockFile); err != nil {
			panic(err)
		}
	}

	l, err := net.Listen("unix", sockFile)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				panic(err)
			}

			go handleConnection(conn)
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	<-sigs

	fmt.Println("cleanup...")
	os.Exit(0)
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	request := commandRequest{}
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		writeResponse(conn, commandResponse{Error: "invalid command request"})
		return
	}
	writeResponse(conn, executeRequest(request))
}

func executeRequest(request commandRequest) commandResponse {
	switch request.Operation {
	case "is-mounted":
		if len(request.Args) != 1 {
			return commandResponse{Error: "is-mounted requires one target path"}
		}
		mounted, err := isMountPoint(request.Args[0])
		if err != nil {
			return commandResponse{Error: "read mount information"}
		}
		return commandResponse{Success: true, Mounted: mounted}
	case "mount":
		return runCommand("s3fs", request.Args)
	case "unmount":
		if len(request.Args) != 1 {
			return commandResponse{Error: "unmount requires one target path"}
		}
		return runCommand("umount", request.Args)
	default:
		return commandResponse{Error: "unsupported operation"}
	}
}

func runCommand(name string, args []string) commandResponse {
	if err := exec.Command(name, args...).Run(); err != nil {
		fmt.Printf("%s failed: %v\n", name, err)
		return commandResponse{Error: name + " failed"}
	}
	return commandResponse{Success: true}
}

func isMountPoint(targetPath string) (bool, error) {
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	wanted := filepath.Clean(targetPath)
	for _, line := range strings.Split(string(mountInfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if filepath.Clean(unescapeMountInfoPath(fields[4])) == wanted {
			return true, nil
		}
	}
	return false, nil
}

func unescapeMountInfoPath(value string) string {
	replacer := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	return replacer.Replace(value)
}

func writeResponse(conn net.Conn, response commandResponse) {
	if err := json.NewEncoder(conn).Encode(response); err != nil {
		fmt.Println(err)
	}
}
