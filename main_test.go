package main

import (
	"encoding/json"
	"testing"
)

func TestParseCPUMemory(t *testing.T) {
	var response cpuMemoryResponse
	if err := json.Unmarshal([]byte(`{"data":{"cpu":2,"mem":39}}`), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.CPU != 2 || response.Data.Mem != 39 || response.Logout {
		t.Fatalf("got cpu=%v mem=%v logout=%v", response.Data.CPU, response.Data.Mem, response.Logout)
	}
}

func TestParseLogout(t *testing.T) {
	var response cpuMemoryResponse
	if err := json.Unmarshal([]byte(`{"logout":true,"reason":"notAuth"}`), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Logout || response.Reason != "notAuth" {
		t.Fatalf("got logout=%v reason=%q", response.Logout, response.Reason)
	}
}
