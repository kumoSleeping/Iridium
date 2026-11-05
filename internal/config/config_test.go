package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iridium", "config.json")
	c, err := Load(path)
	if err != nil || !reflect.DeepEqual(c, Default()) {
		t.Fatalf("no file: %+v %v", c, err)
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(`{"listen":"127.0.0.1:9000","idleReap":"0","programs":{"claude":"/opt/claude"}}`), 0o600)
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.IdleReap != 0 || c.StopGrace.D() != 5*time.Second || c.Programs["claude"] != "/opt/claude" || c.Programs["pi"] != "pi" {
		t.Fatalf("loaded: %+v", c)
	}
	c.IdleReap = Duration(90 * time.Minute)
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil || !reflect.DeepEqual(back, c) {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	os.WriteFile(path, []byte(`{"listne":"x"}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("took an unknown field")
	}
}
