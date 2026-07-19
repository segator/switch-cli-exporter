package main

import "testing"

func TestParseCPU(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  float64
		isErr bool
	}{
		{name: "switch output", input: "CPU utilization\r\n---------------\r\nCurrent: 5%\r\nSwitch#", want: 5},
		{name: "ANSI output", input: "\x1b[H\x1b[JCurrent: 37.5%\r\nSwitch#", want: 37.5},
		{name: "missing", input: "Switch#", isErr: true},
		{name: "out of range", input: "Current: 101%", isErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCPU(test.input)
			if test.isErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}
