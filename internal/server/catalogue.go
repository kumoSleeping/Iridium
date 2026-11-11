package server

import "github.com/kumoSleeping/iridium/internal/api"

// catalogue is the official apps Iridium lists (GET /v1/internal/catalogue, draft 二「接入」): what each is
// and the commands that install it, by system. Iridium downloads, installs and runs none of them:
// each pairs like any other app. {origin} in a command is this Iridium's address, as the page
// fills it in.
var catalogue = []api.CatalogueEntry{
	{
		ID: "computer", Name: "ir join", Type: "computer",
		Description: api.Text{Zh: "另一台电脑：在那里运行 agent 和命令行", En: "Another computer: runs agents and command lines there"},
		Install: []api.InstallStep{
			{OS: "linux", Command: "curl -fL " + releases + "iridium-linux-amd64.tar.gz | tar xz && ./iridium join {origin} --install"},
			{OS: "darwin", Command: "curl -fL " + releases + "iridium-darwin-arm64.tar.gz | tar xz && ./iridium join {origin} --install"},
		},
		URL: "https://github.com/kumoSleeping/Iridium/blob/main/docs/exec.md",
	},
	{
		ID: "dsh", Name: "dsh-remote-iridium", Type: "plugin",
		Description: api.Text{Zh: "dsh web 的插件：在那台电脑上运行 dsh 会话", En: "A dsh web plugin: runs dsh sessions on that computer"},
		Install: []api.InstallStep{
			{Command: "dsh plugin --profile web add dsh-remote-iridium"},
		},
		URL: "https://www.npmjs.com/package/dsh-remote-iridium",
	},
	{
		ID: "qq", Name: "iridium-qq", Type: "bot",
		Description: api.Text{Zh: "QQ Bot：在 QQ 私聊里用会话", En: "A QQ bot: sessions in QQ private chats"},
		Install: []api.InstallStep{
			{OS: "linux", Command: "curl -fL " + releases + "iridium-qq-linux-amd64.tar.gz | tar xz && ./iridium-qq setup"},
			{OS: "darwin", Command: "curl -fL " + releases + "iridium-qq-darwin-arm64.tar.gz | tar xz && ./iridium-qq setup"},
		},
		URL: "https://github.com/kumoSleeping/Iridium/blob/main/docs/qq.md",
	},
}

const releases = "https://github.com/kumoSleeping/Iridium/releases/latest/download/"
