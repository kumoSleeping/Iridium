package session

import "github.com/kumoSleeping/iridium/internal/ctx/assemble"

// What the tests name of Iridium's words for agents, as the assembly layer makes them.

// SystemPrompt is the system prompt of a session hosted here.
func SystemPrompt(lang string) string {
	in, err := assemble.System(assemble.Where{Session: "s1", Lang: lang})
	if err != nil {
		panic(err)
	}
	return in.String()
}
