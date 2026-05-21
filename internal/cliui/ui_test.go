package cliui

import "testing"

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		12:              "12B",
		1024:            "1.0KB",
		1024 * 1024:     "1.0MB",
		5 * 1024 * 1024: "5.0MB",
	}
	for size, want := range cases {
		if got := FormatBytes(size); got != want {
			t.Fatalf("FormatBytes(%d) = %s, want %s", size, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[int64]string{
		12:   "12s",
		65:   "1m05s",
		3660: "1h01m",
	}
	for seconds, want := range cases {
		if got := FormatDuration(seconds); got != want {
			t.Fatalf("FormatDuration(%d) = %s, want %s", seconds, got, want)
		}
	}
}

func TestFormatMemoryUsage(t *testing.T) {
	got := FormatMemoryUsage(128*1024*1024, 1024)
	if got != "128.0MB / 1024MB" {
		t.Fatalf("unexpected memory usage: %s", got)
	}
}

func TestFormatExitCode(t *testing.T) {
	if got := FormatExitCode(-1); got != "-" {
		t.Fatalf("unexpected running exit code: %s", got)
	}
	if got := FormatExitCode(137); got != "137" {
		t.Fatalf("unexpected stopped exit code: %s", got)
	}
}
