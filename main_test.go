package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestBinary compiles the binary with a custom config path for testing.
func buildTestBinary(t *testing.T, configPath string) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "prompt-sudo-discord-test")
	cmd := exec.Command("go", "build",
		"-ldflags", fmt.Sprintf("-X main.configPath=%s", configPath),
		"-o", binPath,
		".",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build test binary: %v\n%s", err, out)
	}
	return binPath
}

// writeTestConfig writes a test config file and returns its path.
func writeTestConfig(t *testing.T) string {
	t.Helper()
	cfg := Config{
		DiscordToken:   "Bot fake-token",
		ApproverIDs:    []string{"123"},
		TimeoutSeconds: 10,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestShowStdinFlag(t *testing.T) {
	configPath := writeTestConfig(t)
	binPath := buildTestBinary(t, configPath)

	t.Run("without --show-stdin does not consume stdin", func(t *testing.T) {
		// Without --show-stdin, binary will fail at Discord connect (no valid token)
		// but the important thing is it doesn't hang reading stdin.
		cmd := exec.Command(binPath, "--channel", "12345", "--", "echo", "hello")
		cmd.Stdin = strings.NewReader("this should not be read")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("expected error (no valid Discord token), got success")
		}
		output := string(out)
		// Should fail at Discord connection, not at stdin reading
		if strings.Contains(output, "Error reading stdin") {
			t.Fatal("should not read stdin without --show-stdin")
		}
	})

	t.Run("with --show-stdin reads stdin before Discord connect", func(t *testing.T) {
		stdinContent := "line1\nline2\nline3"
		cmd := exec.Command(binPath, "--show-stdin", "--channel", "12345", "--", "echo", "hello")
		cmd.Stdin = strings.NewReader(stdinContent)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("expected error (no valid Discord token), got success")
		}
		output := string(out)
		// Should fail at Discord connection, not at stdin reading
		if strings.Contains(output, "Error reading stdin") {
			t.Fatalf("failed to read stdin: %s", output)
		}
	})

	t.Run("with --show-stdin and empty stdin", func(t *testing.T) {
		cmd := exec.Command(binPath, "--show-stdin", "--channel", "12345", "--", "echo", "hello")
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("expected error (no valid Discord token), got success")
		}
		output := string(out)
		if strings.Contains(output, "Error reading stdin") {
			t.Fatalf("failed to read empty stdin: %s", output)
		}
	})
}

func TestShowStdinExecution(t *testing.T) {
	// Test that stdin data is correctly piped to the command via bytes.NewReader
	t.Run("bytes.NewReader pipes data to command", func(t *testing.T) {
		stdinContent := []byte("hello from stdin\nline 2\n")

		cmd := exec.Command("cat")
		cmd.Stdin = bytes.NewReader(stdinContent)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("cat command failed: %v", err)
		}

		if stdout.String() != string(stdinContent) {
			t.Fatalf("expected %q, got %q", string(stdinContent), stdout.String())
		}
	})
}

func TestFormatCommand(t *testing.T) {
	tests := []struct {
		args     []string
		expected string
	}{
		{[]string{"echo", "hello"}, "echo hello"},
		{[]string{"ls", "-la", "/tmp"}, "ls -la /tmp"},
		{[]string{"single"}, "single"},
	}
	for _, tt := range tests {
		got := formatCommand(tt.args)
		if got != tt.expected {
			t.Errorf("formatCommand(%v) = %q, want %q", tt.args, got, tt.expected)
		}
	}
}

func TestIsApprover(t *testing.T) {
	ids := []string{"111", "222", "333"}
	if !isApprover("222", ids) {
		t.Error("expected 222 to be an approver")
	}
	if isApprover("999", ids) {
		t.Error("expected 999 not to be an approver")
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := map[string]interface{}{
			"discord_token":   "Bot test-token",
			"approver_ids":    []string{"123"},
			"timeout_seconds": 60,
		}
		data, _ := json.Marshal(cfg)
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, data, 0644)

		config, err := loadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if config.DiscordToken != "Bot test-token" {
			t.Errorf("token = %q, want %q", config.DiscordToken, "Bot test-token")
		}
		if config.TimeoutSeconds != 60 {
			t.Errorf("timeout = %d, want 60", config.TimeoutSeconds)
		}
	})

	t.Run("missing token", func(t *testing.T) {
		cfg := map[string]interface{}{
			"approver_ids": []string{"123"},
		}
		data, _ := json.Marshal(cfg)
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, data, 0644)

		_, err := loadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "discord_token") {
			t.Fatalf("expected discord_token error, got: %v", err)
		}
	})

	t.Run("default timeout when zero", func(t *testing.T) {
		cfg := map[string]interface{}{
			"discord_token": "Bot test-token",
			"approver_ids":  []string{"123"},
		}
		data, _ := json.Marshal(cfg)
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, data, 0644)

		config, err := loadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if config.TimeoutSeconds != defaultTimeout {
			t.Errorf("timeout = %d, want %d", config.TimeoutSeconds, defaultTimeout)
		}
	})
}
