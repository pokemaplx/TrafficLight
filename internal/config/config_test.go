package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultsWithoutAFile(t *testing.T) {
	config, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if config.Output != "web" || config.Port != 3335 || config.WebPort != 3336 || config.WebMaxRecords != 5000 || config.WebMaxMemoryMB != 128 {
		t.Errorf("defaults are %+v", config)
	}
}

func TestTheEnvironmentWinsOverTheFile(t *testing.T) {
	path := write(t, `
port = 4000
output = "print"
web_password = "from the file"
web_open_browser = false
webhook = "an option that is gone"
`)
	t.Setenv("TRAFFICLIGHT_PORT", "5000")
	t.Setenv("trafficlight_web_max_memory_mb", "64")

	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Port != 5000 {
		t.Errorf("the port is %d, the environment should win", config.Port)
	}
	if config.Output != "print" || config.WebPassword != "from the file" || config.WebOpenBrowser {
		t.Errorf("the file isn't read: %+v", config)
	}
	if config.WebMaxMemoryMB != 64 {
		t.Errorf("a lower case variable isn't read: %d", config.WebMaxMemoryMB)
	}
}

func TestTheDatabaseIsOptional(t *testing.T) {
	config, err := Load(write(t, ``))
	if err != nil || config.DatabaseURL != "" {
		t.Errorf("without the option it is %q, %v", config.DatabaseURL, err)
	}
	config, err = Load(write(t, `database_url = " mariadb://user:secret@db/trafficlight "`))
	if err != nil || config.DatabaseURL != "mariadb://user:secret@db/trafficlight" {
		t.Errorf("with it, it is %q, %v", config.DatabaseURL, err)
	}
}

func TestNumbersInQuotesAreTaken(t *testing.T) {
	config, err := Load(write(t, `web_port = "8080"`))
	if err != nil || config.WebPort != 8080 {
		t.Errorf("got %v, %v", config, err)
	}
}

func TestWhatCanNotWorkIsRefused(t *testing.T) {
	for content, reason := range map[string]string{
		`output = "ui"`:           "gone",
		`output = "discord"`:      "gone",
		`output = "nonsense"`:     "doesn't exist",
		`port = "many"`:           "not a number",
		`web_port = 99999`:        "not a port",
		`web_max_records = 0`:     "at least 1",
		`web_max_memory_mb = -1`:  "negative",
		`web_open_browser = "eh"`: "neither true nor false",
		`this is not toml`:        "config.toml",
	} {
		_, err := Load(write(t, content))
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: got %v, expected it to say %q", content, err, reason)
		}
	}
}
