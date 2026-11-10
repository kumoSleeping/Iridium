package satori

import (
	"strconv"

	"github.com/kumoSleeping/iridium/internal/api"
)

// A transcript block as a message's content (docs/satori.md "记录"): standard elements for what
// Satori has (text; a file as <img>/<video>/<audio>/<file> by its internal: URL), elements of
// Iridium's own, prefixed with the adapter's name (advanced/internal.md), for the rest. Lossless:
// the web reads the block back from it (web/src/satori.ts; testdata/blocks.json is the table both
// sides test). A client that leaves out unknown elements still sees the words of people and agents.
//
//	user       <iridium:user id from terminal length/>text files
//	assistant  [<iridium:assistant length/>]text files
//	thinking   <iridium:thinking length>text</iridium:thinking>
//	tool       <iridium:tool id name length error><iridium:input>…</iridium:input>
//	             <iridium:result at time length error>text <iridium:image/>…</iridium:result></iridium:tool> files
//	result     <iridium:result id length error>text <iridium:image/>…</iridium:result>
//	system     <iridium:system event from terminal id length>text <iridium:compact auto before after ms/></iridium:system>
//
// <iridium:image mime width height unsent/> is a picture of a result, n its place among them.
// The message's id is the block's at, created_at its time.

const (
	elUser    = Adapter + ":user"
	elAgent   = Adapter + ":assistant"
	elInput   = Adapter + ":input"
	elResult  = Adapter + ":result"
	elImage   = Adapter + ":image"
	elCompact = Adapter + ":compact"
)

// blockContent is block b of session id as content, its files as login of app sees them (none if
// login is ""), its text through mask.
func blockContent(b api.Block, app, login, id string, mask func(string) string) string {
	attrs := []Attr{}
	add := func(k, v string) {
		if v != "" {
			attrs = append(attrs, Attr{Key: k, Value: v})
		}
	}
	num := func(k string, n int64) {
		if n != 0 {
			add(k, strconv.FormatInt(n, 10))
		}
	}
	add("id", b.ID)
	add("name", mask(b.Name))
	add("event", string(b.Event))
	add("from", mask(b.From))
	add("terminal", b.FromTerminal)
	num("length", int64(b.Length))
	if b.Error {
		attrs = append(attrs, Attr{Key: "error", Bare: true})
	}
	var kids []Element
	if b.Input != "" {
		kids = append(kids, Tag(elInput, nil, Text(mask(b.Input))))
	}
	if r := b.Result; r != nil {
		rk := []Element{}
		if r.Text != "" {
			rk = append(rk, Text(mask(r.Text)))
		}
		rk = append(rk, imageElements(r.Images)...)
		ra := []string{"at", r.At}
		if r.Time != 0 {
			ra = append(ra, "time", strconv.FormatInt(r.Time, 10))
		}
		if r.Length != 0 {
			ra = append(ra, "length", strconv.Itoa(r.Length))
		}
		e := Tag(elResult, ra, rk...)
		if r.Error {
			e.Attrs = append(e.Attrs, Attr{Key: "error", Bare: true})
		}
		kids = append(kids, e)
	}
	kids = append(kids, imageElements(b.Images)...)
	if c := b.Compact; c != nil {
		e := Tag(elCompact, nil)
		if c.Auto {
			e.Attrs = append(e.Attrs, Attr{Key: "auto", Bare: true})
		}
		for _, kv := range []struct {
			k string
			n int64
		}{{"before", int64(c.Before)}, {"after", int64(c.After)}, {"ms", c.Ms}} {
			if kv.n != 0 {
				e.Attrs = append(e.Attrs, Attr{Key: kv.k, Value: strconv.FormatInt(kv.n, 10)})
			}
		}
		kids = append(kids, e)
	}
	var els []Element
	switch b.Kind {
	case api.BlockKindUser, api.BlockKindAssistant:
		// The words at the top, as any client reads them; the rest in the mark.
		typ := elUser
		if b.Kind == api.BlockKindAssistant {
			typ = elAgent
		}
		if typ == elUser || len(attrs) > 0 || len(kids) > 0 {
			els = append(els, Element{Type: typ, Attrs: attrs, Children: kids})
		}
		if b.Text != "" {
			els = append(els, Text(mask(b.Text)))
		}
	default:
		if b.Text != "" {
			kids = append([]Element{Text(mask(b.Text))}, kids...)
		}
		els = append(els, Element{Type: Adapter + ":" + string(b.Kind), Attrs: attrs, Children: kids})
	}
	s := String(els)
	if login != "" {
		s += fileElements(app, login, id, b.Files)
	}
	return s
}

func imageElements(is []api.Image) []Element {
	var out []Element
	for _, im := range is {
		e := Tag(elImage, nil)
		if im.Mime != "" {
			e.Attrs = append(e.Attrs, Attr{Key: "mime", Value: im.Mime})
		}
		if im.W != 0 {
			e.Attrs = append(e.Attrs, Attr{Key: "width", Value: strconv.Itoa(im.W)})
		}
		if im.H != 0 {
			e.Attrs = append(e.Attrs, Attr{Key: "height", Value: strconv.Itoa(im.H)})
		}
		if im.Unsent {
			e.Attrs = append(e.Attrs, Attr{Key: "unsent", Bare: true})
		}
		out = append(out, e)
	}
	return out
}
