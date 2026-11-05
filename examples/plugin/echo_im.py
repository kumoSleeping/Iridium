#!/usr/bin/env python3
"""A minimal Iridium plugin: a fake IM on stdin/stdout (docs/api.md "插件").

Each stdin line is a message from one chat window, "fake:stdin"; Iridium's events come back
on stdout. A card prints its options numbered: answer with "#<n>" (one number per question).

  IR_URL=http://127.0.0.1:7810 IR_TOKEN=ir_... python3 echo_im.py

Standard library only. It keeps its Last-Event-ID in ./.echo_im.last, so a restart reads on.
"""
import json
import os
import sys
import threading
import time
import urllib.error
import urllib.request

BASE = os.environ.get("IR_URL", "http://127.0.0.1:7810").rstrip("/")
TOKEN = os.environ.get("IR_TOKEN", "")
CHAT = "fake:stdin"
LAST = ".echo_im.last"
cards = {}  # the last card shown: its id and options
lock = threading.Lock()


def call(method, path, body=None, headers=None):
    req = urllib.request.Request(BASE + path, method=method, data=None if body is None else json.dumps(body).encode())
    req.add_header("Content-Type", "application/json")
    if TOKEN:
        req.add_header("Authorization", "Bearer " + TOKEN)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    return urllib.request.urlopen(req, timeout=None if path == "/api/events" else 60)


def post(path, body):
    try:
        with call("POST", path, body) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        say("! " + json.load(e).get("error", str(e)))
        return {}


def say(text):
    with lock:
        print(text, flush=True)


def show_card(card):
    cards["last"] = card
    lines = ["? " + card["title"]] + ["  " + s for s in card.get("summary", [])]
    for i, o in enumerate(card["options"], 1):
        lines.append("  #%d %s%s" % (i, o["text"], " *" if o.get("primary") else ""))
    say("\n".join(lines))


def show_reply(r):
    if r.get("reply"):
        say(r["reply"]["text"])
        for f in r["reply"].get("files", []):
            say("  [file] %s (%d bytes) %s/api/chat/%s/files/%s" % (f["name"], f["size"], BASE, CHAT, f["ref"]))
    if r.get("card"):
        show_card(r["card"])


def events():
    while True:
        last = open(LAST).read().strip() if os.path.exists(LAST) else ""
        try:
            with call("GET", "/api/events", headers={"Last-Event-ID": last} if last else {}) as r:
                kind, data = None, None
                for raw in r:
                    line = raw.decode().rstrip("\n")
                    if line.startswith("id: "):
                        last = line[4:]
                    elif line.startswith("event: "):
                        kind = line[7:]
                    elif line.startswith("data: "):
                        data = json.loads(line[6:])
                    elif line == "" and kind:
                        handle(kind, data)
                        open(LAST, "w").write(last)
                        kind = None
        except (urllib.error.URLError, OSError) as e:
            say("! events: %s; again in 2s" % e)
            time.sleep(2)


def handle(kind, e):
    if kind == "progress":
        say("… " + e["text"])
    elif kind == "reply":
        show_reply(e)
    elif kind == "card":
        show_card(e["card"])
    elif kind == "card_done":
        say("(answered: %s)" % e["cardId"])
    elif kind == "error":
        say("! " + e["text"])


def main():
    threading.Thread(target=events, daemon=True).start()
    for line in sys.stdin:
        text = line.strip()
        if not text:
            continue
        card = cards.get("last")
        if text.startswith("#") and card:
            picks = [card["options"][int(n) - 1]["id"] for n in text[1:].split()]
            show_reply(post("/api/answer", {"card": card["id"], "choices": picks}))
            continue
        show_reply(post("/api/chat/%s/input" % CHAT, {"text": text, "sender": os.environ.get("USER", "")}))


if __name__ == "__main__":
    main()
