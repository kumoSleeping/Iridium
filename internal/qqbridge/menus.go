package qqbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/kumoSleeping/iridium/internal/qq"
)

// The bot's menus, all private chats' (QQ has one of each per bot): the row under the chat, the
// sessions first, and the / panel of Iridium's commands. They take the place of what another
// service (Rhodium's slots) left there. Pushed as QQ and Iridium are both up, only when changed
// (QQ takes 5 changes a minute).

// Menu is the row: at most 10 buttons, a fold at most 5.
var Menu = []qq.MenuItem{
	{Name: "会话", Command: "/use"},
	{Name: "停止", Command: "/stop"},
	{Name: "状态", Command: "/status"},
	{Name: "更多", Sub: []qq.MenuItem{
		{Name: "会话列表", Command: "/ls"},
		{Name: "模型", Command: "/model"},
		{Name: "上下文", Command: "/context"},
		{Name: "用量", Command: "/usage"},
	}},
}

// panelRemark marks Iridium's panel; panelsOver, the ones it takes over.
const panelRemark = "iridium"

var panelsOver = []string{"rhodium"}

func (b *Bridge) commands(ctx context.Context) []qq.PanelItem {
	chat := "c2c:-"
	b.mu.Lock()
	if len(b.Config.Owners) > 0 {
		chat = chatKey(b.Config.Owners[0])
	}
	b.mu.Unlock()
	var l []ChatCommand
	if err := b.IR.call(ctx, "GET", chatPath(chat)+"/commands", nil, &l); err != nil {
		b.logf("menus: commands: %v", err)
		return nil
	}
	var out []qq.PanelItem
	for _, c := range l {
		if c.Iridium && len(out) < 20 {
			out = append(out, qq.PanelItem{Name: strings.TrimPrefix(c.Name, "/"), Desc: c.Description})
		}
	}
	return out
}

// menus pushes the row and the panel if they changed since the last push.
func (b *Bridge) menus(ctx context.Context) {
	b.menuMu.Lock()
	defer b.menuMu.Unlock()
	b.mu.Lock()
	up := b.status.QQ && b.status.Iridium
	b.mu.Unlock()
	if !up {
		return
	}
	panel := b.commands(ctx)
	if panel == nil {
		return
	}
	j, _ := json.Marshal(struct {
		Menu  []qq.MenuItem
		Panel []qq.PanelItem
	}{Menu, panel})
	sum := sha256.Sum256(j)
	mark := hex.EncodeToString(sum[:8])
	b.mu.Lock()
	done := b.state.Menus == mark
	b.mu.Unlock()
	if done {
		return
	}
	if err := b.QQ.SetMenu(ctx, Menu); err != nil {
		b.logf("menus: the row: %v", err)
		return
	}
	if err := b.QQ.SetPanel(ctx, panelRemark, panelsOver, panel); err != nil {
		b.logf("menus: the / panel: %v", err)
		return
	}
	b.mu.Lock()
	b.state.Menus = mark
	b.mu.Unlock()
	b.save()
	b.logf("menus: set")
}
