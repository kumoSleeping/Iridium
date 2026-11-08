#!/usr/bin/env python3
# A minimal executor (docs/exec.md) in plain Python, standard library only, its WebSocket too: a
# developer's test that the protocol is all an outside program needs, not shipped. It pairs once
# (a code to allow on the Iridium's page), then lets the Iridium run the agents it finds here.
#
#   python3 -I scripts/dev/exec-mini.py http://127.0.0.1:7811 [name] [agent ...]   (default: pi)
import base64, hashlib, json, os, platform, shutil, signal, socket, ssl, struct, subprocess
import re, sys, threading, time, urllib.parse, urllib.request

URL = sys.argv[1].rstrip("/")
NAME = sys.argv[2] if len(sys.argv) > 2 else "exec-mini"
AGENTS = sys.argv[3:] or ["pi"]
PROGRAM = "exec-mini.py 0.1"
OS = {"win32": "windows"}.get(sys.platform, sys.platform)
UA = {"user-agent": PROGRAM.replace(" ", "/")}  # a proxy may turn Python's own away (Cloudflare 1010)
# Each Iridium its own TOKEN and holders, under its host and port as ir join names them.
KEY = re.sub(r"[^a-z0-9.-]+", "-", urllib.parse.urlsplit(URL).netloc.lower()).strip("-.")
STATE = os.path.join(os.path.expanduser("~"), ".iridium-exec-mini", KEY)
# Never given: its TOKEN, ~/.ssh. Its scratch (launch files) is where the Iridium writes.
DENIED = [os.path.realpath(os.path.join(STATE, "token")), os.path.realpath(os.path.expanduser("~/.ssh"))]


def say(m):
    print(time.strftime("%H:%M:%S"), m, flush=True)


class Refused(OSError):
    """The Iridium turns the TOKEN down (401, 403): its Bot was removed."""


class Fail(Exception):
    def __init__(self, code, msg):
        super().__init__(msg)
        self.code = code


# ---- agents here ----

def find_agent(name):
    path = shutil.which(name)
    if not path:
        return None
    real = os.path.realpath(path)
    argv, version = [real], ""
    with open(real, "rb") as f:
        first = f.readline()
    if first.startswith(b"#!") and b"node" in first and shutil.which("node"):
        argv = [shutil.which("node"), real]
    d = os.path.dirname(real)
    while d != os.path.dirname(d):
        try:
            with open(os.path.join(d, "package.json")) as f:
                version = json.load(f).get("version", "")
            break
        except (OSError, ValueError):
            d = os.path.dirname(d)
    return {"agent": name, "argv": argv, "version": version}


def agents():
    return [a for a in map(find_agent, AGENTS) if a]


# ---- a WebSocket client (RFC 6455), just enough ----

class WS:
    def __init__(self, url, token):
        u = urllib.parse.urlsplit(url)
        port = u.port or (443 if u.scheme == "https" else 80)
        s = socket.create_connection((u.hostname, port), timeout=30)
        if u.scheme == "https":
            s = ssl.create_default_context().wrap_socket(s, server_hostname=u.hostname)
        key = base64.b64encode(os.urandom(16)).decode()
        s.sendall((f"GET /api/exec HTTP/1.1\r\nHost: {u.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                   f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nUser-Agent: {UA['user-agent']}\r\nAuthorization: Bearer {token}\r\n\r\n").encode())
        self.r, self.s, self.lock = s.makefile("rb"), s, threading.Lock()
        status = self.r.readline()
        while self.r.readline() not in (b"\r\n", b""):
            pass
        if b" 401 " in status or b" 403 " in status:
            raise Refused(status.decode().strip())
        if b" 101 " not in status:
            raise OSError(f"no upgrade: {status.decode().strip()}")
        s.settimeout(None)

    def send(self, obj, op=1):
        data = obj if isinstance(obj, bytes) else json.dumps(obj).encode()
        n, mask = len(data), os.urandom(4)
        head = bytes([0x80 | op]) + (bytes([0x80 | n]) if n < 126 else
                                     bytes([0xFE]) + struct.pack(">H", n) if n < 65536 else
                                     bytes([0xFF]) + struct.pack(">Q", n))
        with self.lock:
            self.s.sendall(head + mask + _mask(data, mask))

    def recv(self):
        """The next text message; None once closed. Pings are answered here."""
        msg = b""
        while True:
            h = self.r.read(2)
            if len(h) < 2:
                return None
            fin, op, n = h[0] & 0x80, h[0] & 0x0F, h[1] & 0x7F
            if n == 126:
                n = struct.unpack(">H", self.r.read(2))[0]
            elif n == 127:
                n = struct.unpack(">Q", self.r.read(8))[0]
            mask = self.r.read(4) if h[1] & 0x80 else None
            data = self.r.read(n)
            if mask:
                data = _mask(data, mask)
            if op == 8:
                return None
            if op == 9:
                self.send(data, op=10)
            elif op in (0, 1, 2):
                msg += data
                if fin:
                    return msg.decode()

    def close(self):
        try:
            self.s.close()
        except OSError:
            pass


def _mask(data, mask):
    n = len(data)
    return (int.from_bytes(data, "big") ^ int.from_bytes((mask * (n // 4 + 1))[:n], "big")).to_bytes(n, "big")


# ---- holders: an agent process, its out.log, its exit ----

class Hold:
    def __init__(self, hid):
        self.id, self.dir = hid, os.path.join(STATE, "holds", hid)
        self.proc, self.in_seq, self.exit, self.gen = None, 0, None, 0
        self.ended, self.mu = threading.Event(), threading.Lock()

    def out(self):
        return os.path.join(self.dir, "out.log")

    def state(self):
        size = os.path.getsize(self.out()) if os.path.exists(self.out()) else 0
        return {"hold": self.id, "outSize": size, "inSeq": self.in_seq, "exit": self.exit}

    def start(self, p):
        os.makedirs(self.dir, mode=0o700, exist_ok=True)
        out, err = open(self.out(), "ab"), open(os.path.join(self.dir, "err.log"), "ab")
        self.grace = (p.get("stopGraceMs") or 5000) / 1000
        env = dict(kv.split("=", 1) for kv in p.get("env") or [] if "=" in kv)
        try:
            self.proc = subprocess.Popen(p["argv"], env=env, cwd=p.get("cwd") or None, stdin=subprocess.PIPE,
                                         stdout=out, stderr=err, start_new_session=os.name != "nt")
        except OSError as e:
            err.write(f"cannot start {p['argv'][0]}: {e}\n".encode())
            self.finish({"from": "agent", "code": 127, "signal": None})
            return {"pid": 0}
        finally:
            out.close()
            err.close()
        threading.Thread(target=self.wait, daemon=True).start()
        say(f"{self.id}: started pid {self.proc.pid}")
        return {"pid": self.proc.pid}

    def wait(self):
        rc = self.proc.wait()
        sig = signal.Signals(-rc).name[3:] if rc < 0 else None
        self.finish({"from": "agent", "code": None if sig else rc, "signal": sig})

    def finish(self, x):
        say(f"{self.id}: ended {x}")
        self.exit = x
        self.ended.set()

    def write(self, p):
        with self.mu:
            if p["seq"] > self.in_seq and self.proc and self.proc.poll() is None:
                self.proc.stdin.write(p["line"].encode() + b"\n")
                self.proc.stdin.flush()
                self.in_seq = p["seq"]
        return {"outSize": self.state()["outSize"]}

    def tail(self, ws, at):
        self.gen += 1
        gen = self.gen

        def run():
            while gen == self.gen:
                ended = self.ended.is_set()
                data = b""
                if os.path.exists(self.out()):
                    with open(self.out(), "rb") as f:
                        f.seek(at_[0])
                        data = f.read(1 << 20)
                    i = data.rfind(b"\n")
                    data = data[:i + 1] if i >= 0 else (data if ended else b"")
                try:
                    if data:
                        ws.send({"event": "out", "params": {"hold": self.id, "off": at_[0], "data": data.decode("utf-8", "replace")}})
                        at_[0] += len(data)
                        continue
                    if ended:
                        tail = open(os.path.join(self.dir, "err.log"), "rb").read()[-2048:].decode("utf-8", "replace")
                        ws.send({"event": "exit", "params": {"hold": self.id, **self.exit, "errTail": tail}})
                        return
                except OSError:
                    return
                time.sleep(0.05)

        at_ = [at]
        threading.Thread(target=run, daemon=True).start()
        return {}

    def stop(self, hard):
        if self.proc and self.proc.poll() is None:
            if not hard:
                try:
                    self.proc.stdin.close()
                    self.proc.wait(self.grace)
                    return {}
                except (OSError, subprocess.TimeoutExpired):
                    pass
            try:
                os.killpg(self.proc.pid, signal.SIGKILL) if os.name != "nt" else self.proc.kill()
            except OSError:
                pass
        return {}


holds = {}


# ---- files ----

def allowed(path):
    real = os.path.realpath(path)
    if any(real == d or real.startswith(d + os.sep) for d in DENIED):
        raise Fail("denied", path)
    return path


def stat(path, head=0):
    st = os.stat(allowed(path))
    kind = "link" if os.path.islink(path) else "dir" if os.path.isdir(path) else "file" if os.path.isfile(path) else "other"
    out = {"size": st.st_size, "mtime": int(st.st_mtime * 1000), "type": kind}
    if head and kind == "file":
        with open(path, "rb") as f:
            out["head"] = hashlib.sha256(f.read(head)).hexdigest()
    return out


def fs(method, p):
    path = p["path"]
    if method == "fs.stat":
        return stat(path, p.get("head", 0))
    if method == "fs.list":
        out = []
        for e in os.scandir(allowed(path)):
            try:
                out.append({"name": e.name, **stat(e.path)})
            except (OSError, Fail):
                pass  # gone, or not given
        return {"entries": out}
    if method == "fs.read":
        with open(allowed(path), "rb") as f:
            f.seek(p.get("offset", 0))
            return {"data": base64.b64encode(f.read(min(p.get("length", 4 << 20), 4 << 20))).decode()}
    if method == "fs.write":
        data = base64.b64decode(p["data"])
        if hashlib.sha256(data).hexdigest() != p["sha256"]:
            raise Fail("bad_request", "sha256 does not match")
        if os.path.exists(allowed(path)) and not p.get("replace"):
            raise Fail("exists", path)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        tmp = f"{path}.{time.time_ns()}.tmp"
        with open(tmp, "wb") as f:
            f.write(data)
        os.replace(tmp, path)
        return {}
    if method == "fs.mkdir":
        os.makedirs(allowed(path), exist_ok=True)
        return {}
    raise Fail("unsupported", method)


# ---- one connection ----

def handle(ws, method, p):
    if method.startswith("fs."):
        return fs(method, p)
    if method == "hold.start":
        h = holds.get(p["hold"])
        if h:
            return {"pid": h.proc.pid if h.proc else 0}
        argv = p.get("argv") or []
        if not any(a["argv"] == argv[:len(a["argv"])] for a in agents()):
            raise Fail("denied", "only its agents start here")
        h = holds[p["hold"]] = Hold(p["hold"])
        return h.start(p)
    h = holds.get(p.get("hold", ""))
    if not h:
        raise Fail("not_found", p.get("hold", ""))
    if method == "hold.write":
        return h.write(p)
    if method == "hold.tail":
        return h.tail(ws, p.get("from", 0))
    if method in ("hold.stop", "hold.kill"):
        say(f"{h.id}: {method}")
        return h.stop(method == "hold.kill")
    if method == "hold.forget":
        holds.pop(h.id, None)
        shutil.rmtree(h.dir, ignore_errors=True)
        return {}
    raise Fail("unsupported", method)


def answer(ws, f):
    try:
        out = {"id": f["id"], "result": handle(ws, f["method"], f.get("params") or {})}
    except Fail as e:
        out = {"id": f["id"], "error": {"code": e.code, "message": str(e)}}
    except FileNotFoundError as e:
        out = {"id": f["id"], "error": {"code": "not_found", "message": str(e)}}
    except PermissionError as e:
        out = {"id": f["id"], "error": {"code": "denied", "message": str(e)}}
    except Exception as e:  # noqa: BLE001: the Iridium hears every failure
        out = {"id": f["id"], "error": {"code": "failed", "message": f"{type(e).__name__}: {e}"}}
    try:
        ws.send(out)
    except OSError:
        pass


def serve(token):
    ws = WS(URL, token)
    scratch = os.path.join(STATE, "scratch")
    os.makedirs(scratch, exist_ok=True)
    ws.send({"id": 1, "method": "hello", "params": {
        "protocol": 1, "program": PROGRAM, "os": OS,
        "arch": platform.machine(), "home": os.path.expanduser("~"), "sep": os.sep, "scratch": scratch,
        "env": [f"{k}={v}" for k, v in os.environ.items()], "agents": agents(), "noShell": True,
        "holds": [h.state() for h in holds.values()]}})
    alive = [True]

    def ping():
        while alive[0]:
            time.sleep(25)
            try:
                ws.send({"event": "ping"})
            except OSError:
                return

    threading.Thread(target=ping, daemon=True).start()
    try:
        while (m := ws.recv()) is not None:
            f = json.loads(m)
            if f.get("id") == 1 and "method" not in f:
                say(f"connected as {(f.get('result') or {}).get('name')}" if "result" in f else f"hello: {f.get('error')}")
            elif f.get("method"):
                # stop waits for its grace: off the reader; the rest in order.
                if f["method"] == "hold.stop":
                    threading.Thread(target=answer, args=(ws, f), daemon=True).start()
                else:
                    answer(ws, f)
    finally:
        alive[0] = False
        ws.close()


def pair():
    body = json.dumps({"name": NAME, "os": OS, "arch": platform.machine(), "program": PROGRAM,
                       "agents": [{"agent": a["agent"], "version": a["version"]} for a in agents()]}).encode()
    while True:
        req = urllib.request.Request(URL + "/api/exec/pair", data=body, headers={"content-type": "application/json", **UA})
        r = json.load(urllib.request.urlopen(req))
        say(f'pairing code {r["code"]}: allow "{NAME}" on the Iridium page')
        while True:
            time.sleep(2)
            try:
                st = json.load(urllib.request.urlopen(urllib.request.Request(URL + "/api/exec/pair/" + r["request"], headers=UA)))
            except urllib.error.HTTPError as e:
                st = {"status": "expired" if e.code == 404 else "pending"}
            if st["status"] == "allowed" and st.get("token"):
                return st["token"]
            if st["status"] == "denied":
                sys.exit("declined")
            if st["status"] == "expired":
                break


def main():
    os.makedirs(STATE, mode=0o700, exist_ok=True)
    # One run per Iridium: a second would take the same TOKEN and the two would push each other off.
    lock = open(os.path.join(STATE, "lock"), "w")
    try:
        import fcntl
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except ImportError:
        pass  # Windows: no lock
    except OSError:
        sys.exit(f"already running for {URL}")
    tf = os.path.join(STATE, "token")
    say(f"agents: {', '.join(a['agent'] + ' ' + a['version'] for a in agents()) or 'none'}")
    while True:
        if not os.path.exists(tf):
            token = pair()
            fd = os.open(tf, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            os.write(fd, token.encode())
            os.close(fd)
            say("paired")
        try:
            serve(open(tf).read().strip())
            say("disconnected")
        except Refused as e:
            say(f"the Iridium no longer takes this machine ({e}): pairing again")
            os.remove(tf)
            continue
        except OSError as e:
            say(f"connect: {e}")
        time.sleep(2)


if __name__ == "__main__":
    main()
