package main

import (
	"encoding/json"
	"net"
	"testing"
)

func TestExecuteRequestRejectsUnsupportedOperations(t *testing.T) {
	response := executeRequest(commandRequest{Operation: "sh", Args: []string{"-c", "id"}})
	if response.Success || response.Error != "unsupported operation" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestUnescapeMountInfoPath(t *testing.T) {
	got := unescapeMountInfoPath("/var/lib/kubelet/pods/a\\040b")
	if got != "/var/lib/kubelet/pods/a b" {
		t.Fatalf("unescapeMountInfoPath() = %q", got)
	}
}

func TestHandleConnectionUsesStructuredRequests(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go handleConnection(server)

	if err := json.NewEncoder(client).Encode(commandRequest{Operation: "sh", Args: []string{"-c", "id"}}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response := commandResponse{}
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.Success || response.Error != "unsupported operation" {
		t.Fatalf("unexpected response: %+v", response)
	}
}
