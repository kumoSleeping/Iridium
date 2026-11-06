package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// Migration 9 keeps what push_settings said: a workspace with nothing on is off, one with the
// defaults says nothing, any other is on with its events.
func TestPushRulesFromSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iridium.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := db.Exec(schema[i]); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, 8))
	for _, q := range []string{
		`INSERT INTO push_settings VALUES (0, 0, 0, 0, 0)`,
		`INSERT INTO push_settings VALUES (1, 1, 1, 0, 0)`,
		`INSERT INTO push_settings VALUES (2, 1, 0, 1, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rules, err := s.PushRules()
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules: %+v", rules)
	}
	if r := rules[0]; r.Scope != PushWorkspace || r.Target != "0" || !r.Off || r.Events != nil {
		t.Errorf("nothing on: %+v", r)
	}
	if r := rules[1]; r.Target != "2" || r.Off || r.Events == nil || *r.Events != (PushEvents{NeedsYou: true, TurnEnd: true}) {
		t.Errorf("its own: %+v", r)
	}

	// Set again replaces; deleted, it is gone.
	must(t, s.SetPushRule(PushRule{Scope: PushWorkspace, Target: "0"}))
	must(t, s.SetPushRule(PushRule{Scope: PushFolder, Target: "/x", Off: true}))
	must(t, s.DeletePushRule(PushWorkspace, "2"))
	rules, _ = s.PushRules()
	if len(rules) != 2 || rules[0].Scope != PushFolder || rules[1].Off || rules[1].Events != nil {
		t.Errorf("after: %+v", rules)
	}
}
